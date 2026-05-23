package mcpserver

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/darkquasar/fracta/internal/ctxkeys"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/mark3labs/mcp-go/server"
)

// mcpReadyWaitTimeout bounds how long /agents/{task}/mcp blocks waiting for
// the gateway reconciler to finish its initial pass.
const mcpReadyWaitTimeout = 90 * time.Second

// serveHTTP starts an HTTP server that routes MCP requests through the
// StreamableHTTPServer. Agent identity is extracted from the URL path
// /agents/{task}/mcp and injected into context for tool handlers.
//
// readyCh gates MCP endpoints: /agents/{task}/mcp blocks until readyCh closes
// (or a server-side timeout expires). /healthz always responds 200 (liveness).
// /readyz responds 200 only after readyCh closes (readiness).
// Pass nil for readyCh to make all endpoints immediately available.
//
// debugHandlers optionally registers extra read-only HTTP handlers (e.g.
// /debug/policy). Keys are URL paths; values are handlers. Pass nil to
// register no extra endpoints. Keeps gateway-internal types out of http.go.
//
// The server also exposes /healthz and /readyz for K8s probes and performs
// graceful shutdown on SIGTERM/SIGINT.
func serveHTTP(mcp *server.MCPServer, addr string, readyCh <-chan struct{}, debugHandlers map[string]http.HandlerFunc) error {
	if readyCh == nil {
		ch := make(chan struct{})
		close(ch)
		readyCh = ch
	}

	log := fractalog.Component("http")

	httpTransport := server.NewStreamableHTTPServer(mcp,
		server.WithStateLess(true),
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			// The agent task is injected by the outer mux handler before
			// delegating to the StreamableHTTPServer. Propagate it.
			if task, ok := ctxkeys.AgentTask(r.Context()); ok {
				return ctxkeys.WithAgentTask(ctx, task)
			}
			// Fallback: extract from header (for non-Claude hosts that can set custom headers).
			if task := r.Header.Get("X-Fracta-Agent"); task != "" {
				return ctxkeys.WithAgentTask(ctx, task)
			}
			return ctx
		}),
	)

	mux := http.NewServeMux()

	// Agent-scoped path: /agents/{task}/mcp — Go 1.22 path patterns.
	// Blocks until readyCh closes (initial reconciliation pass completed).
	mux.HandleFunc("/agents/{task}/mcp", func(w http.ResponseWriter, r *http.Request) {
		timer := time.NewTimer(mcpReadyWaitTimeout)
		defer timer.Stop()
		select {
		case <-readyCh:
		case <-r.Context().Done():
			http.Error(w, "gateway not ready", http.StatusServiceUnavailable)
			return
		case <-timer.C:
			http.Error(w, "gateway readiness timeout", http.StatusServiceUnavailable)
			return
		}
		task := r.PathValue("task")
		ctx := ctxkeys.WithAgentTask(r.Context(), task)
		httpTransport.ServeHTTP(w, r.WithContext(ctx))
	})

	// Liveness probe — always returns 200 if the process is running.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Readiness probe — returns 200 only after initial reconciliation completes.
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-readyCh:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})

	// Optional read-only debug endpoints (e.g. /debug/policy). The map is
	// supplied by the caller so http.go stays free of gateway-internal types.
	for path, h := range debugHandlers {
		mux.HandleFunc(path, h)
	}

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Graceful shutdown on SIGTERM/SIGINT.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	errChan := make(chan error, 1)
	go func() {
		log.Info("starting HTTP server", "addr", addr)
		errChan <- srv.ListenAndServe()
	}()

	select {
	case err := <-errChan:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case sig := <-sigChan:
		log.Info("received signal, shutting down", "signal", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			return err
		}
		return nil
	}
}
