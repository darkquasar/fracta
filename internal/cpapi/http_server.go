package cpapi

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/darkquasar/fracta/internal/auth/credentials"
	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/state"
)

// HTTPServer serves the control-plane API over HTTP.
// It wraps a ControlPlaneClient and exposes lifecycle operations as a JSON API.
// The route group is separate from the gateway MCP routes (spec S3.3, S11 rule 3).
type HTTPServer struct {
	server *http.Server
	client ControlPlaneClient
}

// HTTPServerOption configures optional HTTPServer behavior.
type HTTPServerOption func(*httpServerOptions)

type httpServerOptions struct {
	stager        credentials.CredentialStager
	sseHub        *events.SSEHub
	eventStore    *events.EventStore
	eventReader   state.EventReader
	snapshotStore *events.SnapshotStore
	gatewayURL    string
}

// WithCredentialStager registers a CredentialStager for the staging API endpoint.
// When set, POST /api/v1/credentials/stage is enabled.
func WithCredentialStager(stager credentials.CredentialStager) HTTPServerOption {
	return func(o *httpServerOptions) { o.stager = stager }
}

// WithGatewayProxy enables the operator debug proxy at
// GET /api/v1/debug/gateway-policy. The CP API forwards each request to
// <gatewayURL>/debug/policy and returns the response verbatim. Use the
// in-cluster gateway Service URL (e.g. "http://fracta-gateway.fracta.svc:8080").
// When unset, the route is not registered.
func WithGatewayProxy(gatewayURL string) HTTPServerOption {
	return func(o *httpServerOptions) { o.gatewayURL = gatewayURL }
}

// WithSSE sets the SSEHub and EventStore for the watch endpoint.
// When set, GET /api/v1/agents/{name}/watch is enabled.
func WithSSE(hub *events.SSEHub, store *events.EventStore, reader state.EventReader, snapStore *events.SnapshotStore) HTTPServerOption {
	return func(o *httpServerOptions) {
		o.sseHub = hub
		o.eventStore = store
		o.eventReader = reader
		o.snapshotStore = snapStore
	}
}

// NewHTTPServer creates a new control-plane API HTTP server.
// The listen address should be in host:port format (e.g. ":8090").
func NewHTTPServer(listenAddr string, client ControlPlaneClient, opts ...HTTPServerOption) *HTTPServer {
	var options httpServerOptions
	for _, o := range opts {
		o(&options)
	}

	h := &handler{client: client}
	mux := http.NewServeMux()

	// Health check.
	mux.HandleFunc("GET /healthz", h.handleHealthz)

	// Agent lifecycle.
	mux.HandleFunc("POST /api/v1/agents", h.handleSpawn)
	mux.HandleFunc("GET /api/v1/agents", h.handleListAgents)
	mux.HandleFunc("GET /api/v1/agents/{name}", h.handleGetAgent)
	mux.HandleFunc("DELETE /api/v1/agents/{name}", h.handleKill)
	mux.HandleFunc("POST /api/v1/agents/{name}/say", h.handleSay)
	mux.HandleFunc("GET /api/v1/agents/{name}/peek", h.handlePeek)
	mux.HandleFunc("GET /api/v1/agents/{name}/logs", h.handleGetLogs)
	mux.HandleFunc("GET /api/v1/agents/{name}/mission", h.handleGetMission)
	mux.HandleFunc("POST /api/v1/agents/{name}/merge", h.handleMerge)

	// Event ingest and query (spec-35 observability).
	mux.HandleFunc("POST /api/v1/agents/{name}/events", h.handleIngestEvents)
	mux.HandleFunc("GET /api/v1/agents/{name}/events", h.handleQueryEvents)

	// Dry-run.
	mux.HandleFunc("POST /api/v1/agents/dry-run", h.handleDryRunSpawn)

	// Graph operations (spec-37).
	mux.HandleFunc("POST /api/v1/graph/query", h.handleGraphQuery)
	mux.HandleFunc("POST /api/v1/graph/update", h.handleGraphUpdate)
	mux.HandleFunc("POST /api/v1/graph/schema", h.handleGraphSchema)
	mux.HandleFunc("POST /api/v1/graph/path", h.handleGraphPath)
	mux.HandleFunc("POST /api/v1/graph/neighbors", h.handleGraphNeighbors)

	// Objectives.
	mux.HandleFunc("POST /api/v1/objectives", h.handleCreateObjective)
	mux.HandleFunc("GET /api/v1/objectives", h.handleListObjectives)
	mux.HandleFunc("GET /api/v1/objectives/{id}", h.handleGetObjective)
	mux.HandleFunc("POST /api/v1/objectives/{id}/unfreeze", h.handleUnfreezeObjective)

	// SSE watch endpoint (optional — only when SSEHub is provided).
	if options.sseHub != nil {
		wh := &watchHandler{hub: options.sseHub, eventStore: options.eventStore, eventReader: options.eventReader, snapshotStore: options.snapshotStore}
		mux.HandleFunc("GET /api/v1/agents/{name}/watch", wh.handleWatch)
	}

	// Credential staging (optional — only when stager is provided).
	if options.stager != nil {
		sh := &stagingHandler{stager: options.stager}
		mux.HandleFunc("POST /api/v1/credentials/stage", sh.handleStageCredential)
		mux.HandleFunc("GET /api/v1/credentials/stage/{ref}", sh.handleFetchStagedCredential)
	}

	// Operator debug proxy (optional — only when gateway URL is provided).
	// Forwards GET /api/v1/debug/gateway-policy → GET <gateway>/debug/policy
	// so thin-client operators can read gateway policy state without needing
	// cluster-internal DNS or kubectl port-forward.
	if options.gatewayURL != "" {
		dh := newDebugProxyHandler(options.gatewayURL)
		mux.HandleFunc("GET /api/v1/debug/gateway-policy", dh.handleGatewayPolicy)
	}

	return &HTTPServer{
		server: &http.Server{
			Addr:    listenAddr,
			Handler: mux,
		},
		client: client,
	}
}

// Start begins serving in a background goroutine.
// Returns after the listener is bound. Call Shutdown to stop.
func (s *HTTPServer) Start() error {
	log := fractalog.Component("cpapi")

	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return fmt.Errorf("cpapi: listen %s: %w", s.server.Addr, err)
	}

	log.Info("control-plane API listening", "addr", ln.Addr().String())
	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Error("control-plane API server error", "error", err)
		}
	}()
	return nil
}

// Shutdown gracefully stops the HTTP server.
func (s *HTTPServer) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// Handler returns the underlying http.Handler for testing with httptest.
func (s *HTTPServer) Handler() http.Handler {
	return s.server.Handler
}
