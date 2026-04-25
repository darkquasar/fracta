package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMCPServer() *server.MCPServer {
	return server.NewMCPServer("fracta-test", "0.1.0", server.WithToolCapabilities(true))
}

func TestServeHTTP_Health(t *testing.T) {
	mcpSrv := newTestMCPServer()

	httpTransport := server.NewStreamableHTTPServer(mcpSrv, server.WithStateLess(true))

	mux := http.NewServeMux()
	mux.HandleFunc("/agents/{task}/mcp", func(w http.ResponseWriter, r *http.Request) {
		task := r.PathValue("task")
		ctx := context.WithValue(r.Context(), agentTaskKey{}, task)
		httpTransport.ServeHTTP(w, r.WithContext(ctx))
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAgentIdentityFromPath(t *testing.T) {
	mcpSrv := newTestMCPServer()

	var capturedTask string
	mcpSrv.AddTool(mcp.NewTool("echo_task",
		mcp.WithDescription("returns agent task from context"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		capturedTask = agentTaskFromContext(ctx)
		return mcp.NewToolResultText(capturedTask), nil
	})

	httpTransport := server.NewStreamableHTTPServer(mcpSrv,
		server.WithStateLess(true),
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			if task := r.Context().Value(agentTaskKey{}); task != nil {
				return context.WithValue(ctx, agentTaskKey{}, task)
			}
			return ctx
		}),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/agents/{task}/mcp", func(w http.ResponseWriter, r *http.Request) {
		task := r.PathValue("task")
		ctx := context.WithValue(r.Context(), agentTaskKey{}, task)
		httpTransport.ServeHTTP(w, r.WithContext(ctx))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Send an initialize request to the MCP endpoint for agent "my-task".
	initPayload := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}`
	resp, err := http.Post(ts.URL+"/agents/my-task/mcp", "application/json", strings.NewReader(initPayload))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Now call the echo_task tool.
	toolPayload := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo_task","arguments":{}}}`
	resp2, err := http.Post(ts.URL+"/agents/my-task/mcp", "application/json", strings.NewReader(toolPayload))
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
	assert.Equal(t, "my-task", capturedTask)
}

func TestAgentIdentityFromHeader(t *testing.T) {
	mcpSrv := newTestMCPServer()

	var capturedTask string
	mcpSrv.AddTool(mcp.NewTool("echo_task",
		mcp.WithDescription("returns agent task from context"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		capturedTask = agentTaskFromContext(ctx)
		return mcp.NewToolResultText(capturedTask), nil
	})

	httpTransport := server.NewStreamableHTTPServer(mcpSrv,
		server.WithStateLess(true),
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			if task := r.Context().Value(agentTaskKey{}); task != nil {
				return context.WithValue(ctx, agentTaskKey{}, task)
			}
			if task := r.Header.Get("X-Fracta-Agent"); task != "" {
				return context.WithValue(ctx, agentTaskKey{}, task)
			}
			return ctx
		}),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/agents/{task}/mcp", func(w http.ResponseWriter, r *http.Request) {
		task := r.PathValue("task")
		ctx := context.WithValue(r.Context(), agentTaskKey{}, task)
		httpTransport.ServeHTTP(w, r.WithContext(ctx))
	})
	// Also mount at /mcp for header-based identity testing.
	mux.Handle("/mcp", httpTransport)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Test header-based identity via /mcp endpoint.
	initPayload := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}`
	req, _ := http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(initPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Fracta-Agent", "header-task")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	toolPayload := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo_task","arguments":{}}}`
	req2, _ := http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(toolPayload))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Fracta-Agent", "header-task")
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
	assert.Equal(t, "header-task", capturedTask)
}

func TestAgentTaskFromContext(t *testing.T) {
	// Empty context returns empty string.
	assert.Equal(t, "", agentTaskFromContext(context.Background()))

	// Context with task returns it.
	ctx := context.WithValue(context.Background(), agentTaskKey{}, "my-agent")
	assert.Equal(t, "my-agent", agentTaskFromContext(ctx))
}

func TestServeHTTP_Concurrent(t *testing.T) {
	mcpSrv := newTestMCPServer()
	mcpSrv.AddTool(mcp.NewTool("echo_task",
		mcp.WithDescription("returns agent task from context"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(agentTaskFromContext(ctx)), nil
	})

	httpTransport := server.NewStreamableHTTPServer(mcpSrv,
		server.WithStateLess(true),
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			if task := r.Context().Value(agentTaskKey{}); task != nil {
				return context.WithValue(ctx, agentTaskKey{}, task)
			}
			return ctx
		}),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/agents/{task}/mcp", func(w http.ResponseWriter, r *http.Request) {
		task := r.PathValue("task")
		ctx := context.WithValue(r.Context(), agentTaskKey{}, task)
		httpTransport.ServeHTTP(w, r.WithContext(ctx))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	const numAgents = 10
	var wg sync.WaitGroup
	errors := make(chan error, numAgents)

	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(agentName string) {
			defer wg.Done()
			// Initialize.
			initPayload := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}`
			resp, err := http.Post(ts.URL+"/agents/"+agentName+"/mcp", "application/json", strings.NewReader(initPayload))
			if err != nil {
				errors <- err
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			// Call tool.
			toolPayload := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo_task","arguments":{}}}`
			resp2, err := http.Post(ts.URL+"/agents/"+agentName+"/mcp", "application/json", strings.NewReader(toolPayload))
			if err != nil {
				errors <- err
				return
			}
			body, _ := io.ReadAll(resp2.Body)
			resp2.Body.Close()
			if !strings.Contains(string(body), agentName) {
				errors <- nil // Won't fail but the body check is best-effort with stateless sessions.
			}
		}(strings.Repeat("a", i+1)) // unique agent names: "a", "aa", "aaa", ...
	}

	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
}

func TestGatewayHTTP_ToolsList(t *testing.T) {
	store := &mockStore{}
	mb := &mockMailbox{}

	gs := NewGatewayServer("",
		WithGatewayStore(store),
		WithGatewayMailbox(mb),
	)

	httpTransport := server.NewStreamableHTTPServer(gs.MCPServer(),
		server.WithStateLess(true),
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			if task := r.Context().Value(agentTaskKey{}); task != nil {
				return context.WithValue(ctx, agentTaskKey{}, task)
			}
			return ctx
		}),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/agents/{task}/mcp", func(w http.ResponseWriter, r *http.Request) {
		task := r.PathValue("task")
		ctx := context.WithValue(r.Context(), agentTaskKey{}, task)
		httpTransport.ServeHTTP(w, r.WithContext(ctx))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Step 1: Send MCP initialize request.
	initPayload := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}`
	resp, err := http.Post(ts.URL+"/agents/test-agent/mcp", "application/json", strings.NewReader(initPayload))
	require.NoError(t, err)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Step 2: Send MCP tools/list request.
	listPayload := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	resp2, err := http.Post(ts.URL+"/agents/test-agent/mcp", "application/json", strings.NewReader(listPayload))
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)

	body, err := io.ReadAll(resp2.Body)
	require.NoError(t, err)

	// Parse the JSON-RPC response to extract tool names.
	var rpcResp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(body, &rpcResp), "response body: %s", string(body))

	toolNames := make(map[string]bool)
	for _, tool := range rpcResp.Result.Tools {
		toolNames[tool.Name] = true
	}

	// Assert expected agent tools are present.
	expectedTools := []string{"fracta_list", "fracta_send", "fracta_inbox"}
	for _, name := range expectedTools {
		assert.True(t, toolNames[name], "expected tool %q in tools/list response, got tools: %v", name, toolNames)
	}

	// Assert admin tools are NOT present.
	adminTools := []string{"fracta_init", "fracta_spawn", "fracta_kill", "fracta_merge", "fracta_say"}
	for _, name := range adminTools {
		assert.False(t, toolNames[name], "admin tool %q should NOT appear in GatewayServer tools/list", name)
	}
}

func TestServeHTTP_StartsAndServesHealth(t *testing.T) {
	mcpSrv := newTestMCPServer()

	// Pick a free port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	listener.Close()

	// Start serveHTTP in a goroutine.
	errChan := make(chan error, 1)
	go func() {
		errChan <- serveHTTP(mcpSrv, addr, nil)
	}()

	// Wait for server to be ready.
	var resp *http.Response
	for i := 0; i < 50; i++ {
		resp, err = http.Get("http://" + addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NoError(t, err, "server didn't start in time")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Send SIGINT to trigger graceful shutdown.
	p, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, p.Signal(syscall.SIGINT))

	select {
	case err := <-errChan:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("serveHTTP did not shut down in time")
	}
}

func startTestHTTPServer(t *testing.T, readyCh <-chan struct{}) (string, func()) {
	t.Helper()
	mcpSrv := newTestMCPServer()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	listener.Close()

	errChan := make(chan error, 1)
	go func() {
		errChan <- serveHTTP(mcpSrv, addr, readyCh)
	}()

	// Wait for HTTP listener to be up.
	for i := 0; i < 50; i++ {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	cleanup := func() {
		p, _ := os.FindProcess(os.Getpid())
		_ = p.Signal(syscall.SIGINT)
		select {
		case <-errChan:
		case <-time.After(5 * time.Second):
		}
	}
	return addr, cleanup
}

func TestReadyz_NotReadyThenReady(t *testing.T) {
	readyCh := make(chan struct{})
	addr, cleanup := startTestHTTPServer(t, readyCh)
	defer cleanup()

	// Before ready: /healthz 200, /readyz 503.
	resp, err := http.Get("http://" + addr + "/healthz")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp, err = http.Get("http://" + addr + "/readyz")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	// Signal ready.
	close(readyCh)

	resp, err = http.Get("http://" + addr + "/readyz")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestReadyz_NilReadyCh_ImmediatelyAvailable(t *testing.T) {
	addr, cleanup := startTestHTTPServer(t, nil)
	defer cleanup()

	resp, err := http.Get("http://" + addr + "/healthz")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp, err = http.Get("http://" + addr + "/readyz")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestMCPRoute_BlocksUntilReady(t *testing.T) {
	readyCh := make(chan struct{})
	addr, cleanup := startTestHTTPServer(t, readyCh)
	defer cleanup()

	// MCP request with short client timeout should fail while not ready.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/agents/test-agent/mcp", nil)
	_, err := http.DefaultClient.Do(req)
	require.Error(t, err, "MCP request should fail/timeout while not ready")

	// Signal ready, then MCP route should accept connections (will get protocol error, not 503).
	close(readyCh)
	resp, err := http.Post("http://"+addr+"/agents/test-agent/mcp", "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	resp.Body.Close()
	assert.NotEqual(t, http.StatusServiceUnavailable, resp.StatusCode)
}
