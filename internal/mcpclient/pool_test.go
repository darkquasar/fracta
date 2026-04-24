package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/events"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/mark3labs/mcp-go/server"
)

// newTestPool creates a pool backed by the mcptest in-process server.
// It installs a custom transport factory so the pool uses the test server
// instead of launching real subprocesses.
func newTestPool(t *testing.T, tools ...server.ServerTool) (*Pool, *mcptest.Server) {
	t.Helper()
	ts, err := mcptest.NewServer(t, tools...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ts.Close)

	// We cannot use the pool's normal transport creation for in-process servers.
	// Instead, we pre-populate the serverEntry with the mcptest client.
	pool := NewPool(config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"test": {},
		},
	}, "local")

	// Pre-populate entry with the test client so CallTool bypasses connect
	entry := newServerEntry()
	entry.state = ConnReady
	entry.client = ts.Client()

	// Build tools index from server
	toolsResult, err := ts.Client().ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	entry.tools = indexTools(toolsResult.Tools)

	pool.servers["test"] = entry

	return pool, ts
}

func TestCallTool_JSONResponse(t *testing.T) {
	pool, _ := newTestPool(t, server.ServerTool{
		Tool: mcp.NewTool("get_data", mcp.WithDescription("returns JSON data")),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(`{"items": [1, 2, 3]}`), nil
		},
	})

	result, err := pool.CallTool(context.Background(), "test", "get_data", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Error("expected IsError=false")
	}
	if result.Text != `{"items": [1, 2, 3]}` {
		t.Errorf("got Text=%q, want JSON", result.Text)
	}
}

func TestCallTool_ErrorResponse(t *testing.T) {
	pool, _ := newTestPool(t, server.ServerTool{
		Tool: mcp.NewTool("fail_tool", mcp.WithDescription("returns error")),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultError("something went wrong"), nil
		},
	})

	result, err := pool.CallTool(context.Background(), "test", "fail_tool", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected IsError=true")
	}
	if result.Text != "something went wrong" {
		t.Errorf("got Text=%q", result.Text)
	}
}

func TestCallTool_UnknownServer(t *testing.T) {
	pool := NewPool(config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{},
	}, "local")

	_, err := pool.CallTool(context.Background(), "nonexistent", "tool", nil)
	if err == nil {
		t.Fatal("expected error for unknown server")
	}
}

func TestCallTool_UnknownTool(t *testing.T) {
	pool, _ := newTestPool(t, server.ServerTool{
		Tool: mcp.NewTool("real_tool", mcp.WithDescription("exists")),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(`{}`), nil
		},
	})

	_, err := pool.CallTool(context.Background(), "test", "nonexistent_tool", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestCallTool_ConcurrentFirstUse(t *testing.T) {
	var connectCount atomic.Int32

	pool, _ := newTestPool(t, server.ServerTool{
		Tool: mcp.NewTool("counter", mcp.WithDescription("counts")),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			connectCount.Add(1)
			return mcp.NewToolResultText(`{"n": 1}`), nil
		},
	})

	// Run 10 concurrent CallTool calls
	var wg sync.WaitGroup
	errors := make([]error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := pool.CallTool(context.Background(), "test", "counter", nil)
			errors[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errors {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
}

func TestCallTool_PassesArguments(t *testing.T) {
	pool, _ := newTestPool(t, server.ServerTool{
		Tool: mcp.NewTool("echo_args", mcp.WithDescription("echoes args")),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			data, _ := json.Marshal(args)
			return mcp.NewToolResultText(string(data)), nil
		},
	})

	result, err := pool.CallTool(context.Background(), "test", "echo_args", map[string]any{
		"query": "test",
		"limit": 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(result.Text), &got); err != nil {
		t.Fatal(err)
	}
	if got["query"] != "test" {
		t.Errorf("expected query=test, got %v", got["query"])
	}
}

// --- transport selection tests ---

func TestNewTransport_RemoteStreamableHTTP(t *testing.T) {
	tr, err := newTransport(config.MCPServerEntry{
		Remote: &config.MCPServerRemote{URL: "http://example.test/mcp"},
	}, "local")
	if err != nil {
		t.Fatalf("newTransport: %v", err)
	}
	if _, ok := tr.(*transport.StreamableHTTP); !ok {
		t.Fatalf("transport type = %T, want *transport.StreamableHTTP", tr)
	}
}

func TestNewTransport_RemoteSSE(t *testing.T) {
	tr, err := newTransport(config.MCPServerEntry{
		Remote: &config.MCPServerRemote{URL: "http://example.test/sse", Transport: "sse"},
	}, "local")
	if err != nil {
		t.Fatalf("newTransport: %v", err)
	}
	if _, ok := tr.(*transport.SSE); !ok {
		t.Fatalf("transport type = %T, want *transport.SSE", tr)
	}
}

func TestNewTransport_LocalStdio(t *testing.T) {
	tr, err := newTransport(config.MCPServerEntry{
		Local: config.MCPServerLocal{Command: "example-mcp"},
	}, "kubernetes")
	if err != nil {
		t.Fatalf("newTransport: %v", err)
	}
	if _, ok := tr.(*transport.Stdio); !ok {
		t.Fatalf("transport type = %T, want *transport.Stdio", tr)
	}
}

func TestNewTransport_RemoteWinsOverLocal(t *testing.T) {
	tr, err := newTransport(config.MCPServerEntry{
		Local:  config.MCPServerLocal{Command: "example-mcp"},
		Remote: &config.MCPServerRemote{URL: "http://example.test/mcp"},
	}, "local")
	if err != nil {
		t.Fatalf("newTransport: %v", err)
	}
	if _, ok := tr.(*transport.StreamableHTTP); !ok {
		t.Fatalf("transport type = %T, want *transport.StreamableHTTP", tr)
	}
}

func TestNewTransport_KubernetesAliasFallback(t *testing.T) {
	tr, err := newTransport(config.MCPServerEntry{
		Kubernetes: config.MCPServerRemote{URL: "http://example.test/mcp"},
	}, "local")
	if err != nil {
		t.Fatalf("newTransport: %v", err)
	}
	if _, ok := tr.(*transport.StreamableHTTP); !ok {
		t.Fatalf("transport type = %T, want *transport.StreamableHTTP", tr)
	}
}

func TestNewTransport_LocalWinsOverKubernetesAlias(t *testing.T) {
	tr, err := newTransport(config.MCPServerEntry{
		Local:      config.MCPServerLocal{Command: "example-mcp"},
		Kubernetes: config.MCPServerRemote{URL: "http://example.test/mcp"},
	}, "local")
	if err != nil {
		t.Fatalf("newTransport: %v", err)
	}
	if _, ok := tr.(*transport.Stdio); !ok {
		t.Fatalf("transport type = %T, want *transport.Stdio", tr)
	}
}

func TestNewTransport_InvalidRemoteTransport(t *testing.T) {
	_, err := newTransport(config.MCPServerEntry{
		Remote: &config.MCPServerRemote{URL: "http://example.test/mcp", Transport: "websocket"},
	}, "local")
	if err == nil {
		t.Fatal("expected invalid transport error")
	}
	if !strings.Contains(err.Error(), `unknown remote transport: "websocket"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestNewTransport_MissingConfig(t *testing.T) {
	_, err := newTransport(config.MCPServerEntry{}, "local")
	if err == nil {
		t.Fatal("expected missing transport error")
	}
	if !strings.Contains(err.Error(), "no MCP transport configured") {
		t.Fatalf("error = %v", err)
	}
}

// --- normalizeResult unit tests ---

func TestNormalizeResult_ValidJSON(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: `{"data": [1,2,3]}`},
		},
	}
	tr, err := normalizeResult(result)
	if err != nil {
		t.Fatal(err)
	}
	if tr.IsError {
		t.Error("expected IsError=false")
	}
	if tr.Text != `{"data": [1,2,3]}` {
		t.Errorf("got %q", tr.Text)
	}
}

func TestNormalizeResult_ErrorResult(t *testing.T) {
	result := &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: "bad request"},
		},
	}
	tr, err := normalizeResult(result)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.IsError {
		t.Error("expected IsError=true")
	}
	if tr.Text != "bad request" {
		t.Errorf("got %q", tr.Text)
	}
}

func TestNormalizeResult_NonJSONText(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: "this is not json"},
		},
	}
	_, err := normalizeResult(result)
	if err == nil {
		t.Fatal("expected error for non-JSON text")
	}
}

func TestNormalizeResult_EmptyContent(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{},
	}
	_, err := normalizeResult(result)
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestNormalizeResult_MultipleTextBlocks_FirstValidJSONWins(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: "not json"},
			mcp.TextContent{Type: "text", Text: `{"valid": true}`},
			mcp.TextContent{Type: "text", Text: `{"also_valid": true}`},
		},
	}
	tr, err := normalizeResult(result)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Text != `{"valid": true}` {
		t.Errorf("expected first valid JSON, got %q", tr.Text)
	}
}

func TestNormalizeResult_NonTextContentSkipped(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.ImageContent{Type: "image", Data: "base64data", MIMEType: "image/png"},
			mcp.TextContent{Type: "text", Text: `{"data": "ok"}`},
		},
	}
	tr, err := normalizeResult(result)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Text != `{"data": "ok"}` {
		t.Errorf("got %q", tr.Text)
	}
}

func TestPool_Close(t *testing.T) {
	pool, _ := newTestPool(t, server.ServerTool{
		Tool: mcp.NewTool("noop", mcp.WithDescription("noop")),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(`{}`), nil
		},
	})

	// Don't actually close the test server client through pool.Close()
	// since mcptest manages its own cleanup. Just verify the method doesn't panic.
	// Reset the client to nil before pool.Close to avoid double-close.
	pool.servers["test"].client = nil
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPool_ToolsListChangedNotification(t *testing.T) {
	// Set up a real pipe-based MCP server so we can trigger notifications.
	serverReader, clientWriter := io.Pipe()
	clientReader, serverWriter := io.Pipe()

	mcpSrv := server.NewMCPServer("test-srv", "1.0.0")
	mcpSrv.AddTools(server.ServerTool{
		Tool: mcp.NewTool("tool_a", mcp.WithDescription("initial tool")),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(`{}`), nil
		},
	})

	stdioSrv := server.NewStdioServer(mcpSrv)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go stdioSrv.Listen(ctx, serverReader, serverWriter)

	// Create pool with injected IO transport so connect() exercises the real path.
	received := make(chan string, 1)
	pool := NewPool(config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"backend-x": {},
		},
	}, "local")
	pool.SetToolsChangedHandler(func(srv string) {
		received <- srv
	})
	pool.newTransportFn = func(_ config.MCPServerEntry, _ string) (transport.Interface, error) {
		return transport.NewIO(clientReader, clientWriter, io.NopCloser(&bytes.Buffer{})), nil
	}

	// Trigger lazy connect through getOrConnect (which calls connect → OnNotification).
	entry, err := pool.getOrConnect(ctx, "backend-x")
	if err != nil {
		t.Fatalf("getOrConnect: %v", err)
	}
	if entry.state != ConnReady {
		t.Fatalf("expected ConnReady, got %d", entry.state)
	}

	// Add a tool to the server — this fires tools/list_changed notification.
	mcpSrv.AddTools(server.ServerTool{
		Tool: mcp.NewTool("tool_b", mcp.WithDescription("added later")),
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(`{}`), nil
		},
	})

	select {
	case got := <-received:
		if got != "backend-x" {
			t.Errorf("handler called with server=%q, want %q", got, "backend-x")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout: tools/list_changed notification handler was not called")
	}
}

// --- Subprocess diagnostics tests ---

// buildTestBinary compiles a Go main package from testdata and returns the path
// to the built binary. The binary is cached in a temp dir for the test run.
func buildTestBinary(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, name)
	src := filepath.Join("testdata", name)
	cmd := exec.Command("go", "build", "-o", bin, "./"+src)
	cmd.Dir = "."
	// Inherit Go module context from the test process
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building %s: %v\n%s", name, err, out)
	}
	return bin
}

func TestEnrichError_ExitCode(t *testing.T) {
	// Unit test: enrichError extracts exit code from exec.ExitError
	origErr := errors.New("transport closed")
	// Simulate an exec.ExitError with exit code 1 by running a failing command
	cmd := exec.Command("sh", "-c", "exit 42")
	exitErr := cmd.Run() // this will be an *exec.ExitError

	result := enrichError("initialize", "test-server", origErr, exitErr, "")
	errMsg := result.Error()

	if !strings.Contains(errMsg, "exit code 42") {
		t.Errorf("expected exit code in error, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, `initialize "test-server"`) {
		t.Errorf("expected stage+server in error, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "transport closed") {
		t.Errorf("expected original error wrapped, got: %s", errMsg)
	}
}

func TestEnrichError_Stderr(t *testing.T) {
	// Unit test: enrichError includes stderr snippet
	origErr := errors.New("transport closed")

	result := enrichError("list tools", "my-server", origErr, nil, "permission denied: /usr/bin/foo")
	errMsg := result.Error()

	if !strings.Contains(errMsg, "stderr: permission denied: /usr/bin/foo") {
		t.Errorf("expected stderr in error, got: %s", errMsg)
	}
}

func TestEnrichError_ExitCodeAndStderr(t *testing.T) {
	// Unit test: enrichError includes both exit code and stderr
	origErr := errors.New("transport closed")
	cmd := exec.Command("sh", "-c", "exit 1")
	exitErr := cmd.Run()

	result := enrichError("initialize", "srv", origErr, exitErr, "config not found")
	errMsg := result.Error()

	if !strings.Contains(errMsg, "exit code 1") {
		t.Errorf("expected exit code, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "stderr: config not found") {
		t.Errorf("expected stderr, got: %s", errMsg)
	}
}

func TestEnrichError_NilCloseErr(t *testing.T) {
	// Unit test: enrichError with nil closeErr and empty stderr
	origErr := errors.New("connection refused")
	result := enrichError("initialize", "srv", origErr, nil, "")
	errMsg := result.Error()

	if !strings.Contains(errMsg, `initialize "srv"`) {
		t.Errorf("expected stage+server, got: %s", errMsg)
	}
	if strings.Contains(errMsg, "exit code") {
		t.Errorf("should not contain exit code when closeErr is nil, got: %s", errMsg)
	}
	if strings.Contains(errMsg, "stderr") {
		t.Errorf("should not contain stderr when empty, got: %s", errMsg)
	}
}

func TestConnect_FailingBinary_ExitCodeAndStderr(t *testing.T) {
	// Integration test: real subprocess that exits non-zero with stderr.
	// Exercises the Initialize() failure path in connect() (T17b).
	bin := buildTestBinary(t, "failserver")

	pool := NewPool(config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"bad-server": {
				Local: config.MCPServerLocal{
					Command: bin,
				},
			},
		},
	}, "local")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := pool.getOrConnect(ctx, "bad-server")
	if err == nil {
		t.Fatal("expected error from failing binary")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "exit code 42") {
		t.Errorf("expected exit code 42 in error, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "config file not found") {
		t.Errorf("expected stderr content in error, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, `initialize "bad-server"`) {
		t.Errorf("expected initialize stage in error, got: %s", errMsg)
	}
}

func TestConnect_NonexistentBinary(t *testing.T) {
	// Integration test: nonexistent binary fails at Start(), not Initialize().
	// The error should come from transport creation, not enrichError.
	pool := NewPool(config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"missing-server": {
				Local: config.MCPServerLocal{
					Command: "/nonexistent/binary/path",
				},
			},
		},
	}, "local")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := pool.getOrConnect(ctx, "missing-server")
	if err == nil {
		t.Fatal("expected error from nonexistent binary")
	}

	// Nonexistent binary fails at cmd.Start() inside transport.Start(),
	// which surfaces as "start client" error before Initialize() is called.
	errMsg := err.Error()
	if !strings.Contains(errMsg, "start client") && !strings.Contains(errMsg, "no such file") {
		t.Errorf("expected start/not-found error, got: %s", errMsg)
	}
}

func TestConnect_EchoServer_Success(t *testing.T) {
	// Integration test: working echoserver connects successfully.
	// Proves enrichError is not applied to the success path.
	bin := buildTestBinary(t, "echoserver")

	pool := NewPool(config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"echo": {
				Local: config.MCPServerLocal{
					Command: bin,
				},
			},
		},
	}, "local")
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	entry, err := pool.getOrConnect(ctx, "echo")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}

	entry.mu.Lock()
	state := entry.state
	toolCount := len(entry.tools)
	entry.mu.Unlock()

	if state != ConnReady {
		t.Errorf("expected ConnReady, got %d", state)
	}
	if toolCount != 2 {
		t.Errorf("expected 2 tools (echo, fail), got %d", toolCount)
	}
}

// recordingBus captures emitted events for test assertions.
type recordingBus struct {
	mu     sync.Mutex
	events []events.Event
}

func (r *recordingBus) Emit(_ context.Context, e events.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recordingBus) captured() []events.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]events.Event, len(r.events))
	copy(cp, r.events)
	return cp
}

func TestPool_EmitsBackendConnectFailed(t *testing.T) {
	// Use a failing binary so the connect path emits a connect_attempt failure event.
	bin := buildTestBinary(t, "failserver")

	rec := &recordingBus{}
	pool := NewPool(config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"fail-srv": {
				Local: config.MCPServerLocal{Command: bin},
			},
		},
	}, "local")
	pool.SetEventBus(rec)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := pool.getOrConnect(ctx, "fail-srv")
	if err == nil {
		t.Fatal("expected error from failing binary")
	}

	captured := rec.captured()
	if len(captured) != 1 {
		t.Fatalf("expected 1 event, got %d", len(captured))
	}

	e := captured[0]
	if e.Component != "mcpclient" {
		t.Errorf("Component = %q, want %q", e.Component, "mcpclient")
	}
	if e.Category != "backend" {
		t.Errorf("Category = %q, want %q", e.Category, "backend")
	}
	if e.Resource != "mcp_server:fail-srv" {
		t.Errorf("Resource = %q, want %q", e.Resource, "mcp_server:fail-srv")
	}
	if e.Action != "connect_attempt" {
		t.Errorf("Action = %q, want %q", e.Action, "connect_attempt")
	}
	if e.Outcome != "failure" {
		t.Errorf("Outcome = %q, want %q", e.Outcome, "failure")
	}
	if e.Severity != "warn" {
		t.Errorf("Severity = %q, want %q", e.Severity, "warn")
	}
	if e.Detail == "" {
		t.Error("Detail should contain error message")
	}
}
