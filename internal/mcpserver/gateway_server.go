package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"fmt"

	"github.com/darkquasar/fracta/internal/contract"
	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/gateway"
	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/loaders"
	"github.com/darkquasar/fracta/internal/mailbox"
	"github.com/darkquasar/fracta/internal/mcpclient"
	"github.com/darkquasar/fracta/internal/objective"
	"github.com/darkquasar/fracta/internal/proposal"
	"github.com/darkquasar/fracta/internal/registry"
	"github.com/darkquasar/fracta/internal/resolve"
	"github.com/darkquasar/fracta/internal/schema"
	"github.com/darkquasar/fracta/internal/state"
	"github.com/darkquasar/fracta/internal/strategy"
	"github.com/mark3labs/mcp-go/server"
)

// GatewayServer is an HTTP-facing MCP server that exposes agent tools +
// graph + strategy + gateway proxy tools. It does NOT expose admin tools
// (no spawn/kill/merge/say/init). Designed to run centrally in K8s.
type GatewayServer struct {
	mcp             *server.MCPServer
	store           state.Store
	mailbox         mailbox.Mailbox
	graph           graph.GraphClient
	strategy        strategy.Runner
	mcpPool         *mcpclient.Pool
	mcpFetcher      *loaders.MCPFetcher
	gateway         *gateway.Gateway
	reconciler      *registry.Reconciler
	resolver        *resolve.Resolver
	binding         *contract.BindingSpec
	schemaRegistry  *schema.SchemaRegistry
	checkpointRules []schema.CheckpointRule
	sessions        *strategy.StagingSessionStore
	stagingRunStore strategy.StagingRunStore
	objStore        objective.ObjectiveStore
	proposalStore   proposal.ProposalStore
	events          events.Bus
	snapshotStore   *events.SnapshotStore
	autoPromote     bool
	listenAddr      string
}

// GatewayServerOption configures a GatewayServer.
type GatewayServerOption func(*GatewayServer)

// WithGatewayStore sets the state store.
func WithGatewayStore(s state.Store) GatewayServerOption {
	return func(gs *GatewayServer) { gs.store = s }
}

// WithGatewayMailbox sets the mailbox backend.
func WithGatewayMailbox(m mailbox.Mailbox) GatewayServerOption {
	return func(gs *GatewayServer) { gs.mailbox = m }
}

// WithGatewaySnapshotStore sets the snapshot store for enriched agent observability.
func WithGatewaySnapshotStore(s *events.SnapshotStore) GatewayServerOption {
	return func(gs *GatewayServer) { gs.snapshotStore = s }
}

// WithGatewayGraphClient attaches a graph database client.
func WithGatewayGraphClient(gc graph.GraphClient) GatewayServerOption {
	return func(gs *GatewayServer) { gs.graph = gc }
}

// WithGatewayStrategyRunner attaches a strategy runner.
func WithGatewayStrategyRunner(r strategy.Runner) GatewayServerOption {
	return func(gs *GatewayServer) { gs.strategy = r }
}

// WithGatewayPool attaches the MCP client pool and constructs an MCPFetcher.
func WithGatewayPool(pool *mcpclient.Pool) GatewayServerOption {
	return func(gs *GatewayServer) {
		gs.mcpPool = pool
		gs.mcpFetcher = loaders.NewMCPFetcher(pool)
	}
}

// WithGatewayGateway attaches the MCP gateway for proxied backend tools.
func WithGatewayGateway(gw *gateway.Gateway) GatewayServerOption {
	return func(gs *GatewayServer) {
		gs.gateway = gw
		gw.SetMCPServer(gs.mcp)
	}
}

// WithGatewayReconciler attaches the registry reconciler for trickle-in tool registration.
func WithGatewayReconciler(r *registry.Reconciler) GatewayServerOption {
	return func(gs *GatewayServer) { gs.reconciler = r }
}

// WithGatewayResolver attaches the data resolution engine.
func WithGatewayResolver(r *resolve.Resolver) GatewayServerOption {
	return func(gs *GatewayServer) { gs.resolver = r }
}

// WithGatewayBinding attaches a deployment-specific binding.
func WithGatewayBinding(b *contract.BindingSpec) GatewayServerOption {
	return func(gs *GatewayServer) { gs.binding = b }
}

// WithGatewaySchemaRegistry attaches the graph schema for validation.
func WithGatewaySchemaRegistry(reg *schema.SchemaRegistry) GatewayServerOption {
	return func(gs *GatewayServer) { gs.schemaRegistry = reg }
}

// WithGatewayCheckpointRules attaches checkpoint rules for graph validation.
func WithGatewayCheckpointRules(rules []schema.CheckpointRule) GatewayServerOption {
	return func(gs *GatewayServer) { gs.checkpointRules = rules }
}

// WithGatewayStagingSessionStore attaches a staging session store.
func WithGatewayStagingSessionStore(ss *strategy.StagingSessionStore) GatewayServerOption {
	return func(gs *GatewayServer) { gs.sessions = ss }
}

// WithGatewayStagingRunStore attaches the persistent staging run store.
func WithGatewayStagingRunStore(s strategy.StagingRunStore) GatewayServerOption {
	return func(gs *GatewayServer) { gs.stagingRunStore = s }
}

// WithGatewayAutoPromote enables automatic strategy promotion.
func WithGatewayAutoPromote(enabled bool) GatewayServerOption {
	return func(gs *GatewayServer) { gs.autoPromote = enabled }
}

// WithGatewayListenAddr sets the HTTP listen address (e.g. ":8080").
func WithGatewayListenAddr(addr string) GatewayServerOption {
	return func(gs *GatewayServer) { gs.listenAddr = addr }
}

// WithGatewayObjectiveStore sets the objective store for objective-aware tools.
func WithGatewayObjectiveStore(s objective.ObjectiveStore) GatewayServerOption {
	return func(gs *GatewayServer) { gs.objStore = s }
}

// WithGatewayProposalStore sets the proposal store for mission proposals.
func WithGatewayProposalStore(s proposal.ProposalStore) GatewayServerOption {
	return func(gs *GatewayServer) { gs.proposalStore = s }
}

// WithGatewayEvents sets the lifecycle event bus.
func WithGatewayEvents(b events.Bus) GatewayServerOption {
	return func(gs *GatewayServer) { gs.events = b }
}

// NewGatewayServer creates a gateway-mode MCP server.
// Tools registered: agent + graph + strategy + gateway proxy. NO admin tools.
func NewGatewayServer(root string, opts ...GatewayServerOption) *GatewayServer {
	gs := &GatewayServer{
		mcp: server.NewMCPServer(
			"fracta",
			"0.1.0",
			server.WithToolCapabilities(true),
		),
		listenAddr: ":8080",
	}
	for _, opt := range opts {
		opt(gs)
	}

	// Agent tools (shared surface)
	if gs.store != nil && gs.mailbox != nil {
		var agentOpts []agentToolsOption
		if gs.snapshotStore != nil {
			agentOpts = append(agentOpts, WithAgentToolsSnapshotStore(gs.snapshotStore))
		}
		registerAgentTools(gs.mcp, gs.store, gs.mailbox, agentOpts...)
	}

	// Graph tools
	if gs.graph != nil {
		registerGraphTools(gs.mcp, gs.graph)
		registerCheckpointTool(gs.mcp, gs.graph, gs.checkpointRules)
	}

	// Strategy tools
	if gs.strategy != nil {
		registerStrategyTools(gs.mcp, gs.strategy, gs.graph, gs.autoPromote, gs.resolver, gs.binding, gs.mcpFetcher, gs.sessions, gs.stagingRunStore, gs.events)
	}

	// Objective tools (per-request context resolution via StoreResolver)
	if gs.store != nil && gs.objStore != nil && gs.proposalStore != nil {
		resolver := &StoreResolver{Store: gs.store}
		registerObjectiveTools(gs.mcp, resolver, gs.objStore, gs.proposalStore)
	}

	// Gateway proxy tools (MCP backend proxying)
	if gs.gateway != nil && gs.schemaRegistry != nil {
		gs.gateway.SetSchemaRegistry(gs.schemaRegistry)
	}
	if gs.gateway != nil {
		log := fractalog.Component("gateway-server")
		if gs.reconciler != nil {
			gs.reconciler.Start()
			log.Info("reconciler started — gateway tools will trickle in")
		} else {
			gwCtx, gwCancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := gs.gateway.RegisterAll(gwCtx); err != nil {
				log.Error("gateway registration failed (degraded mode)", "error", err)
			}
			gwCancel()
		}

		// Build initial visibility set and register the tool filter.
		gs.gateway.BuildVisibleSet(context.Background())
		server.WithToolFilter(gs.gateway.FilterToolsForAgent)(gs.mcp)

		if gs.graph != nil {
			registerSearchTool(gs.mcp, gs.graph, gs.gateway)
		}
	}

	return gs
}

// MCPServer returns the underlying MCPServer for HTTP transport wiring.
func (gs *GatewayServer) MCPServer() *server.MCPServer {
	return gs.mcp
}

// ListenAddr returns the configured listen address.
func (gs *GatewayServer) ListenAddr() string {
	return gs.listenAddr
}

// Serve starts the GatewayServer on HTTP. Delegates to serveHTTP from
// the HTTP transport layer. Cleans up resources on shutdown.
func (gs *GatewayServer) Serve() error {
	var readyCh <-chan struct{}
	if gs.reconciler != nil {
		readyCh = gs.reconciler.Ready()
	}

	// Emit gateway-ready event after reconciliation completes.
	// With reconciler: async goroutine waits on readyCh then emits.
	// Without reconciler: emit immediately (gateway is ready now).
	if gs.events != nil {
		if readyCh != nil {
			go func() {
				<-readyCh
				fractalog.Component("gateway-server").Info("reconciler ready — MCP endpoints unblocked")
				gs.emitReadyEvent()
			}()
		} else {
			gs.emitReadyEvent()
		}
	}

	if gs.reconciler != nil {
		defer gs.reconciler.Stop()
	}
	if gs.graph != nil {
		defer gs.graph.Close()
	}
	if gs.strategy != nil {
		defer gs.strategy.Close()
	}
	if gs.mcpPool != nil {
		defer gs.mcpPool.Close()
	}

	debugHandlers := map[string]http.HandlerFunc{}
	if gs.gateway != nil {
		debugHandlers["/debug/policy"] = gs.handleDebugPolicy
	}
	return serveHTTP(gs.mcp, gs.listenAddr, readyCh, debugHandlers)
}

// handleDebugPolicy returns a JSON snapshot of the gateway's tool-policy and
// visibility state. Supports ?verbose=1 to include a per-tool breakdown with
// the reason each non-visible tool was filtered. No auth — same exposure
// model as /healthz and /readyz.
func (gs *GatewayServer) handleDebugPolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	verbose := r.URL.Query().Get("verbose") == "1"
	state := gs.gateway.PolicyStateSnapshot(verbose)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(state); err != nil {
		fractalog.Component("gateway-server").Warn("debug/policy encode failed", "error", err)
	}
}

func (gs *GatewayServer) emitReadyEvent() {
	e := events.Info("gateway", "status_change")
	e.Category = "gateway"
	e.Resource = "gateway:fracta-gateway"
	e.Detail = fmt.Sprintf("listen=%s", gs.listenAddr)
	e.Attrs = map[string]string{"status": "ready"}
	gs.events.Emit(context.Background(), e)
}
