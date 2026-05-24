package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/darkquasar/fracta/internal/contract"
	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/gateway"
	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/loaders"
	"github.com/darkquasar/fracta/internal/mailbox"
	"github.com/darkquasar/fracta/internal/mcpclient"
	"github.com/darkquasar/fracta/internal/orchestrator"
	"github.com/darkquasar/fracta/internal/registry"
	"github.com/darkquasar/fracta/internal/resolve"
	"github.com/darkquasar/fracta/internal/schema"
	"github.com/darkquasar/fracta/internal/state"
	"github.com/darkquasar/fracta/internal/strategy"
	"github.com/mark3labs/mcp-go/server"
)

type Server struct {
	root         string
	mcp          *server.MCPServer
	registry     *orchestrator.ProcessRegistry
	cpClient     cpapi.ControlPlaneClient
	hostRegistry host.HostRegistry
	store        state.Store
	mailbox      mailbox.Mailbox
	graph        graph.GraphClient
	strategy     strategy.Runner
	resolver     *resolve.Resolver
	binding      *contract.BindingSpec
	mcpFetcher   *loaders.MCPFetcher
	mcpPool      *mcpclient.Pool
	gateway      *gateway.Gateway
	reconciler   *registry.Reconciler
	// Schema runtime state for graph validation and checkpoint.
	schemaRegistry  *schema.SchemaRegistry
	checkpointRules []schema.CheckpointRule
	// Strategy staging session store for run-scoped staging.
	sessions *strategy.StagingSessionStore
	// Strategy governance: auto-promote validated->promoted when thresholds met.
	autoPromote bool
	// Agent-mode wiring: threaded into orchestrator for .mcp.json flags.
	configPath  string
	graphAddr   string
	strategyDir string
}

// ServerOption configures a Server.
type ServerOption func(*Server)

// WithGraphClient attaches a graph database client to the server.
func WithGraphClient(gc graph.GraphClient) ServerOption {
	return func(s *Server) { s.graph = gc }
}

// WithStrategyRunner attaches a strategy runner to the server.
func WithStrategyRunner(r strategy.Runner) ServerOption {
	return func(s *Server) { s.strategy = r }
}

// WithStore sets the state store for agent tools (list, send, inbox).
func WithStore(store state.Store) ServerOption {
	return func(s *Server) { s.store = store }
}

// WithMailbox sets the mailbox for agent tools (send, inbox).
func WithMailbox(mb mailbox.Mailbox) ServerOption {
	return func(s *Server) { s.mailbox = mb }
}

// WithResolver attaches a data resolution engine for resolving strategy
// data requirements from the knowledge graph.
func WithResolver(r *resolve.Resolver) ServerOption {
	return func(s *Server) { s.resolver = r }
}

// WithBinding attaches a deployment-specific binding for data source resolution.
func WithBinding(b *contract.BindingSpec) ServerOption {
	return func(s *Server) { s.binding = b }
}

// WithMCPClientPool attaches an MCP client pool and constructs an MCPFetcher
// for fracta_mcp_gateway fetch mode in strategy_resolve.
func WithMCPClientPool(pool *mcpclient.Pool) ServerOption {
	return func(s *Server) {
		s.mcpPool = pool
		s.mcpFetcher = loaders.NewMCPFetcher(pool)
	}
}

// WithGateway attaches an MCP gateway that proxies tools from backend servers.
// The gateway receives the MCPServer reference for AddTool calls.
func WithGateway(gw *gateway.Gateway) ServerOption {
	return func(s *Server) {
		s.gateway = gw
		gw.SetMCPServer(s.mcp)
	}
}

// WithControlPlaneClient sets the ControlPlaneClient used by admin tool handlers
// (spawn, say, kill, list, peek, logs) instead of constructing transient orchestrators.
func WithControlPlaneClient(c cpapi.ControlPlaneClient) ServerOption {
	return func(s *Server) { s.cpClient = c }
}

// WithProcessRegistry sets a shared ProcessRegistry for live stream sessions.
// When set, this replaces the default registry created by New().
// Use this to share a single authoritative ProcessRegistry between the Server
// and any ControlPlaneClient that needs live stream session ownership.
func WithProcessRegistry(r *orchestrator.ProcessRegistry) ServerOption {
	return func(s *Server) { s.registry = r }
}

// WithRuntimeRegistry sets the runtime registry for resolving host implementations.
func WithRuntimeRegistry(reg host.RuntimeRegistry) ServerOption {
	return func(s *Server) { s.hostRegistry = reg }
}

// WithHostRegistry is a deprecated alias for WithRuntimeRegistry.
var WithHostRegistry = WithRuntimeRegistry

// WithAgentWiring sets paths threaded into orchestrator → prepareSpawn → .mcp.json
// for agent-mode shared state discovery and graph/strategy tool access.
func WithAgentWiring(configPath, graphAddr, strategyDir string) ServerOption {
	return func(s *Server) {
		s.configPath = configPath
		s.graphAddr = graphAddr
		s.strategyDir = strategyDir
	}
}

// WithSchemaRegistry attaches the merged graph schema for runtime validation.
func WithSchemaRegistry(reg *schema.SchemaRegistry) ServerOption {
	return func(s *Server) { s.schemaRegistry = reg }
}

// WithCheckpointRules attaches YAML-defined checkpoint rules for graph validation.
func WithCheckpointRules(rules []schema.CheckpointRule) ServerOption {
	return func(s *Server) { s.checkpointRules = rules }
}

// WithStagingSessionStore attaches a staging session store for run-scoped staging.
func WithStagingSessionStore(ss *strategy.StagingSessionStore) ServerOption {
	return func(s *Server) { s.sessions = ss }
}

// WithAutoPromote enables automatic validated->promoted transitions when thresholds are met.
func WithAutoPromote(enabled bool) ServerOption {
	return func(s *Server) { s.autoPromote = enabled }
}

// WithReconciler attaches a registry reconciler that drives the gateway from
// registry state. When set, the reconciler replaces gateway.RegisterAll for
// startup discovery — tools trickle in as backends connect.
func WithReconciler(r *registry.Reconciler) ServerOption {
	return func(s *Server) { s.reconciler = r }
}

func New(root string, opts ...ServerOption) *Server {
	s := &Server{
		root: root,
		mcp: server.NewMCPServer(
			"fracta",
			"0.1.0",
			server.WithToolCapabilities(true),
		),
		registry: orchestrator.NewProcessRegistry(),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.registerTools()
	if s.cpClient != nil {
		s.registerObjectiveTools()
	}
	if s.graph != nil {
		registerGraphTools(s.mcp, s.graph)
		registerCheckpointTool(s.mcp, s.graph, s.checkpointRules)
	} else if s.cpClient != nil {
		// Probe: only register graph tools if the remote CP actually has graph configured.
		// GraphSchema is a lightweight introspection call — if it fails with
		// "graph not configured" the CP has no FalkorDB, so skip registration.
		log := fractalog.Component("mcpserver")
		probeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.cpClient.GraphSchema(probeCtx, cpapi.GraphSchemaRequest{}); err == nil {
			registerCPGraphTools(s.mcp, s.cpClient)
			log.Info("graph tools registered via control plane proxy")
		} else if strings.Contains(err.Error(), "not configured") {
			log.Info("graph not configured on control plane, skipping graph tools")
		} else {
			log.Warn("graph tools unavailable — probe failed (may be transient)", "error", err)
		}
		// CP proxy path does NOT register checkpoint (AC 9).
	}
	if s.strategy != nil {
		registerStrategyTools(s.mcp, s.strategy, s.graph, s.autoPromote, s.resolver, s.binding, s.mcpFetcher, s.sessions, nil, nil)
	}
	if s.gateway != nil && s.schemaRegistry != nil {
		s.gateway.SetSchemaRegistry(s.schemaRegistry)
	}
	if s.gateway != nil {
		log := fractalog.Component("mcpserver")
		if s.reconciler != nil {
			// Reconciler-driven startup: tools trickle in as backends connect.
			s.reconciler.Start()
			log.Info("reconciler started — gateway tools will trickle in")
		} else {
			// Legacy path: synchronous RegisterAll at startup.
			gwCtx, gwCancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := s.gateway.RegisterAll(gwCtx); err != nil {
				log.Error("gateway registration failed (degraded mode)", "error", err)
			}
			gwCancel()
		}
		if s.graph != nil {
			registerSearchTool(s.mcp, s.graph, s.gateway)
		}
	}
	return s
}

func (s *Server) Serve() error {
	defer s.registry.CloseAll()
	if s.reconciler != nil {
		defer s.reconciler.Stop()
	}
	if s.graph != nil {
		defer s.graph.Close()
	}
	if s.strategy != nil {
		defer s.strategy.Close()
	}
	if s.mcpPool != nil {
		defer s.mcpPool.Close()
	}
	return server.ServeStdio(s.mcp)
}

func (s *Server) requireRoot() error {
	if s.root == "" {
		return fmt.Errorf("fracta not initialized; call fracta_init first")
	}
	return nil
}
