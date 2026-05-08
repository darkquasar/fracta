package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/darkquasar/fracta/internal/auth/credentials"
	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/contract"
	"github.com/darkquasar/fracta/internal/controlplane"
	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/gateway"
	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/loaders"
	"github.com/darkquasar/fracta/internal/mcpclient"
	"github.com/darkquasar/fracta/internal/mcpserver"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/oauth"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/objective"
	"github.com/darkquasar/fracta/internal/orchestrator"
	"github.com/darkquasar/fracta/internal/proposal"
	"github.com/darkquasar/fracta/internal/queue"
	"github.com/darkquasar/fracta/internal/registry"
	"github.com/darkquasar/fracta/internal/resolve"
	"github.com/darkquasar/fracta/internal/runtime"
	"github.com/darkquasar/fracta/internal/schema"
	"github.com/darkquasar/fracta/internal/staging"
	"github.com/darkquasar/fracta/internal/state"
	"github.com/darkquasar/fracta/internal/state/pgstore"
	"github.com/darkquasar/fracta/internal/state/sqlitestore"
	"github.com/darkquasar/fracta/internal/strategy"
	"github.com/darkquasar/fracta/internal/worker"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/spf13/cobra"
)

var (
	serveAgentMode           bool
	serveGatewayMode         bool
	serveControlPlaneAPIOnly bool
	serveTransport           string
	serveListen              string
	serveGraphAddr           string
	serveStrategyDir         string
	serveSchemaDir           string
	serveBindingPath         string
	serveStrategySocketMode  string
	// Agent-mode objective context flags (set by worker via MCP server args).
	serveAgentTask   string
	serveObjectiveID string
	serveMissionID   int64
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start fracta as an MCP server",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().BoolVar(&serveAgentMode, "agent-mode", false, "run in agent mode (restricted tools for spawned agents)")
	serveCmd.Flags().BoolVar(&serveGatewayMode, "gateway-mode", false, "run as centralized gateway (agent+graph+strategy+proxy tools, no admin)")
	serveCmd.Flags().BoolVar(&serveControlPlaneAPIOnly, "control-plane-api-only", false, "run as control-plane API server only (lifecycle HTTP API + workers, no MCP stdio server)")
	serveCmd.Flags().StringVar(&serveTransport, "transport", "", "transport protocol: 'stdio' (default) or 'http'")
	serveCmd.Flags().StringVar(&serveListen, "listen", "", "HTTP listen address (e.g. ':8080'); only used with --transport http")
	serveCmd.Flags().StringVar(&serveGraphAddr, "graph-addr", "", "FalkorDB address (e.g. localhost:6379). Enables graph tools when set.")
	serveCmd.Flags().StringVar(&serveStrategyDir, "strategy-dir", "", "Path to strategy directory. Enables strategy tools when set.")
	serveCmd.Flags().StringVar(&serveSchemaDir, "schema-dir", "", "Path to graph-schema/ directory. Loads schema and seeds graph at startup when set.")
	serveCmd.Flags().StringVar(&serveBindingPath, "binding", "", "Path to binding.yaml for data source resolution.")
	serveCmd.Flags().StringVar(&serveStrategySocketMode, "strategy-socket-mode", "", "Strategy runner socket mode: 'external' (connect to sidecar socket) or '' (spawn subprocess)")
	serveCmd.Flags().StringVar(&serveAgentTask, "agent-task", "", "Agent task name (agent-mode only, set by worker)")
	serveCmd.Flags().StringVar(&serveObjectiveID, "objective-id", "", "Objective ID for agent context (agent-mode only, set by worker)")
	serveCmd.Flags().Int64Var(&serveMissionID, "mission-id", 0, "Mission ID for agent context (agent-mode only, set by worker)")
	rootCmd.AddCommand(serveCmd)
}

// resolveTransport returns the effective transport and listen address.
// Precedence: CLI flag > environment variable > config file > default.
func resolveTransport(rc *resolvedConfig) (transport, listen string) {
	// Transport: CLI > env > config > default
	transport = serveTransport
	if transport == "" {
		transport = os.Getenv("FRACTA_TRANSPORT")
	}
	if transport == "" && rc.fullConfig != nil {
		transport = rc.fullConfig.Runtime.Transport
	}
	if transport == "" {
		transport = "stdio"
	}

	// Listen address: CLI > env > config > default
	listen = serveListen
	if listen == "" {
		listen = os.Getenv("FRACTA_LISTEN_ADDR")
	}
	if listen == "" && rc.fullConfig != nil {
		if rc.fullConfig.Gateway.Listen != "" {
			listen = rc.fullConfig.Gateway.Listen
		} else if rc.fullConfig.Runtime.ListenAddr != "" {
			listen = rc.fullConfig.Runtime.ListenAddr
		}
	}
	if listen == "" {
		listen = ":8080"
	}
	return
}

// resolvedConfig holds connection and runtime details extracted from config + CLI overrides.
type resolvedConfig struct {
	graphAddr     string
	graphName     string // FalkorDB graph name (default: fracta_knowledge)
	elasticURL    string
	elasticAPIKey string
	runtime       config.RuntimeConfig
	fullConfig    *config.Config // full parsed config, nil when no config file
}

func resolveConfig() (*resolvedConfig, error) {
	rc := &resolvedConfig{
		graphAddr: serveGraphAddr,
		runtime:   config.DefaultRuntime(),
	}

	if configFlag != "" {
		cfg, err := config.LoadConfig(configFlag)
		if err != nil {
			return nil, fmt.Errorf("loading config: %w", err)
		}
		rc.fullConfig = cfg

		// Extract falkordb address and graph name from config if not overridden by CLI
		if conn, ok := cfg.Connections["falkordb"]; ok {
			if rc.graphAddr == "" && conn.URL != "" {
				// Strip redis:// prefix for go-redis which expects host:port
				addr := conn.URL
				addr = strings.TrimPrefix(addr, "redis://")
				addr = strings.TrimPrefix(addr, "rediss://")
				rc.graphAddr = addr
			}
			if conn.GraphName != "" {
				rc.graphName = conn.GraphName
			}
		}

		// Extract elastic config
		if conn, ok := cfg.Connections["elastic_main"]; ok {
			rc.elasticURL = conn.URL
			rc.elasticAPIKey = conn.APIKey
		}

		// Extract runtime config
		if cfg.Runtime.Backend != "" {
			rc.runtime = cfg.Runtime
		}
	}

	return rc, nil
}

// strategyParts holds the resolved Python/runner paths and sidecar options
// shared between single-sidecar (agent mode) and pool (server mode) construction.
type strategyParts struct {
	pythonBin  string
	runnerPath string
	dir        string
	opts       []strategy.SidecarOption
}

func resolveStrategyParts(rc *resolvedConfig) (*strategyParts, error) {
	uvBin, uvErr := exec.LookPath("uv")
	pythonBin, pyErr := exec.LookPath("python3")
	if uvErr != nil && pyErr != nil {
		return nil, fmt.Errorf("neither uv nor python3 found in PATH")
	}

	// CLI flag overrides config.
	dir := serveStrategyDir
	if dir == "" && rc.fullConfig != nil {
		dir = rc.fullConfig.Strategy.Dir
	}

	var opts []strategy.SidecarOption
	if uvErr == nil {
		opts = append(opts, strategy.WithUVBin(uvBin))
	}
	if rc.graphAddr != "" {
		opts = append(opts, strategy.WithGraphAddr(rc.graphAddr))
	}
	if rc.graphName != "" {
		opts = append(opts, strategy.WithGraphName(rc.graphName))
	}
	if rc.elasticURL != "" {
		opts = append(opts, strategy.WithElasticURL(rc.elasticURL))
	}
	if rc.elasticAPIKey != "" {
		opts = append(opts, strategy.WithElasticAPIKey(rc.elasticAPIKey))
	}
	if rc.runtime.StagingDir != "" {
		opts = append(opts, strategy.WithStagingDir(rc.runtime.StagingDir))
	}

	return &strategyParts{
		pythonBin:  pythonBin,
		runnerPath: dir + "/runner.py",
		dir:        dir,
		opts:       opts,
	}, nil
}

func createSidecar(rc *resolvedConfig) (*strategy.Sidecar, error) {
	sp, err := resolveStrategyParts(rc)
	if err != nil {
		return nil, err
	}
	return strategy.NewSidecar(sp.pythonBin, sp.runnerPath, sp.dir, sp.opts...)
}

func createStrategyRunner(rc *resolvedConfig) (strategy.Runner, error) {
	sp, err := resolveStrategyParts(rc)
	if err != nil {
		return nil, err
	}
	poolSize := 1
	if rc.fullConfig != nil {
		poolSize = rc.fullConfig.Strategy.EffectivePoolSize()
	}

	if serveStrategySocketMode == "external" {
		// K8s sidecar mode: connect to external socket(s), no subprocess spawn.
		externalOpts := append(sp.opts, strategy.WithExternalMode())
		if poolSize <= 1 {
			return strategy.NewSidecar(sp.pythonBin, sp.runnerPath, sp.dir, externalOpts...)
		}
		return strategy.NewSidecarPool(poolSize, sp.pythonBin, sp.runnerPath, sp.dir, externalOpts...)
	}

	// Local mode: spawn subprocess (existing path).
	if poolSize <= 1 {
		return strategy.NewSidecar(sp.pythonBin, sp.runnerPath, sp.dir, sp.opts...)
	}
	return strategy.NewSidecarPool(poolSize, sp.pythonBin, sp.runnerPath, sp.dir, sp.opts...)
}

func runServe(cmd *cobra.Command, args []string) error {
	rc, err := resolveConfig()
	if err != nil {
		return err
	}

	// Attach log file if configured.
	if rc.fullConfig != nil && rc.fullConfig.Logging.File != "" {
		if err := fractalog.AttachFile(rc.fullConfig.Logging.File, rc.fullConfig.Logging.Level); err != nil {
			return fmt.Errorf("attaching log file: %w", err)
		}
	}
	log := fractalog.Component("serve")

	if serveAgentMode {
		root := projectRoot
		if root == "" {
			root, err = FindProjectRoot("")
			if err != nil {
				return err
			}
		}
		// --config takes precedence for store/profile resolution.
		// --root is only the workspace directory for file operations.
		var cfg *config.Config
		if rc.fullConfig != nil {
			cfg = rc.fullConfig
		} else {
			var err error
			cfg, err = loadConfigOrDefault(root)
			if err != nil {
				return err
			}
		}
		profile := controlplane.ResolveProfile(cfg, root)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		store, err := controlplane.BuildStore(ctx, profile)
		if err != nil {
			return fmt.Errorf("opening agent state database: %w", err)
		}
		defer store.Close()
		var opts []mcpserver.AgentServerOption
		opts = append(opts, mcpserver.WithAgentStore(store))
		opts = append(opts, mcpserver.WithAgentMailbox(store.Mailbox()))
		if rc.graphAddr != "" {
			var gcOpts []graph.FalkorDBOption
			if rc.graphName != "" {
				gcOpts = append(gcOpts, graph.WithGraphName(rc.graphName))
			}
			gc := graph.NewFalkorDBClient(rc.graphAddr, gcOpts...)
			opts = append(opts, mcpserver.WithAgentGraphClient(gc))
		}
		agentStrategyDir := serveStrategyDir
		if agentStrategyDir == "" && rc.fullConfig != nil {
			agentStrategyDir = rc.fullConfig.Strategy.Dir
		}
		if agentStrategyDir != "" {
			sc, err := createSidecar(rc)
			if err != nil {
				return fmt.Errorf("strategy sidecar: %w", err)
			}
			opts = append(opts, mcpserver.WithAgentStrategyRunner(sc))
		}
		// Wire objective context when the agent belongs to an objective.
		if serveObjectiveID != "" && serveAgentTask != "" {
			objStore, propStore, err := buildObjectiveStores(store, profile)
			if err != nil {
				return fmt.Errorf("building objective stores: %w", err)
			}
			opts = append(opts,
				mcpserver.WithAgentObjectiveStore(objStore),
				mcpserver.WithAgentProposalStore(propStore),
				mcpserver.WithAgentObjectiveContext(serveAgentTask, serveObjectiveID, serveMissionID),
			)
		}
		return mcpserver.NewAgentServer(root, opts...).Serve()
	}

	if serveGatewayMode {
		return runServeGateway(rc, log)
	}

	if serveControlPlaneAPIOnly {
		return runServeControlPlaneAPI(rc, log)
	}

	// Default path: thin client. Ensure the control plane daemon is running,
	// then create a RemoteControlPlaneClient that talks to it over HTTP.
	root, _ := FindProjectRoot("")
	var cfg *config.Config
	if rc.fullConfig != nil {
		cfg = rc.fullConfig
	} else {
		var err error
		cfg, err = loadConfigOrDefault(root)
		if err != nil {
			return err
		}
	}

	cpURL := cfg.ControlPlaneAPI.URL
	if cpURL == "" {
		// No explicit URL — local mode. Auto-start daemon.
		listenAddr := cfg.ControlPlaneAPI.Listen
		if listenAddr == "" {
			listenAddr = ":9090"
		}
		cpURL = "http://localhost" + listenAddr

		if err := ensureDaemonRunning(root, configFlag); err != nil {
			return fmt.Errorf("ensuring control plane daemon: %w", err)
		}
	}

	// Create thin RemoteControlPlaneClient → CP daemon.
	remoteClient := cpapi.NewRemoteControlPlaneClient(cpURL)
	if err := remoteClient.Validate(context.Background()); err != nil {
		return fmt.Errorf("control plane at %s not reachable: %w", cpURL, err)
	}
	log.Info("connected to control plane", "url", cpURL)

	var opts []mcpserver.ServerOption
	opts = append(opts, mcpserver.WithControlPlaneClient(remoteClient))
	opts = append(opts, mcpserver.WithRuntimeRegistry(buildRuntimeRegistry()))

	return mcpserver.New(root, opts...).Serve()
}

// runServeControlPlaneAPI runs as a control-plane API server only.
// It builds the full control plane (backend, queue, reaper, admission), starts the
// CP API HTTP server, starts in-process workers, and blocks on signal.
// It does NOT start the stdio MCP server — this mode is designed for K8s deployments
// where the control plane runs headless.
func runServeControlPlaneAPI(rc *resolvedConfig, log *slog.Logger) error {
	root, _ := FindProjectRoot("")

	cpLog := fractalog.Component("serve")
	var cpConfig *config.Config
	if rc.fullConfig != nil {
		cpConfig = rc.fullConfig
		cpLog.Info("control-plane config loaded",
			"runtimes_count", len(cpConfig.EffectiveRuntimes()),
			"runtimes_keys", configRuntimeKeys(cpConfig),
		)
	} else {
		var err error
		cpConfig, err = loadConfigOrDefault(root)
		if err != nil {
			return err
		}
		cpLog.Warn("control-plane config from fallback (no --config or LoadConfig failed)",
			"runtimes_count", len(cpConfig.EffectiveRuntimes()),
			"root", root,
		)
	}

	listenAddr := cpConfig.ControlPlaneAPI.Listen
	if listenAddr == "" {
		return fmt.Errorf("--control-plane-api-only requires control_plane_api.listen in config")
	}

	cp, err := controlplane.NewControlPlane(cpConfig, root)
	if err != nil {
		return fmt.Errorf("creating control plane: %w", err)
	}
	defer cp.Close()

	// Resolve agent wiring paths (same as normal serve path).
	var configPath, graphAddr, graphName, strategyDir string
	if configFlag != "" {
		configPath = configFlag
	}
	if rc.graphAddr != "" {
		graphAddr = rc.graphAddr
	}
	if rc.graphName != "" {
		graphName = rc.graphName
	}
	// Fallback: resolve from cpConfig when rc didn't populate graphAddr
	// (happens when loadConfigOrDefault is used instead of --config).
	if graphAddr == "" {
		if fdb, ok := cpConfig.Connections["falkordb"]; ok && fdb.URL != "" {
			addr := fdb.URL
			addr = strings.TrimPrefix(addr, "redis://")
			addr = strings.TrimPrefix(addr, "rediss://")
			graphAddr = addr
		}
	}
	if graphName == "" {
		if fdb, ok := cpConfig.Connections["falkordb"]; ok && fdb.GraphName != "" {
			graphName = fdb.GraphName
		}
	}
	if serveStrategyDir != "" {
		strategyDir = serveStrategyDir
	} else if cpConfig.Strategy.Dir != "" {
		strategyDir = cpConfig.Strategy.Dir
	}

	// Build shared ProcessRegistry and LocalControlPlaneClient.
	sharedRegistry := orchestrator.NewProcessRegistry()
	clientOpts := []cpapi.LocalClientOption{
		cpapi.WithProcessRegistry(sharedRegistry),
		cpapi.WithRuntimeRegistry(buildRuntimeRegistry()),
		cpapi.WithAgentWiring(configPath, graphAddr, strategyDir),
	}

	// Wire GraphClient into LocalControlPlaneClient when FalkorDB is configured.
	if graphAddr != "" {
		var gcOpts []graph.FalkorDBOption
		if graphName != "" {
			gcOpts = append(gcOpts, graph.WithGraphName(graphName))
		}
		gc := graph.NewFalkorDBClient(graphAddr, gcOpts...)
		clientOpts = append(clientOpts, cpapi.WithGraphClient(gc))
		defer gc.Close()
	}

	if cp.ObjectiveStore != nil {
		clientOpts = append(clientOpts, cpapi.WithObjectiveStore(cp.ObjectiveStore))
	}
	if cp.SnapshotStore != nil {
		clientOpts = append(clientOpts, cpapi.WithSnapshotStore(cp.SnapshotStore))
	}
	if cp.EventStore != nil {
		clientOpts = append(clientOpts, cpapi.WithEventStore(cp.EventStore))
	}
	if er, ok := cp.Store.(state.EventReader); ok {
		clientOpts = append(clientOpts, cpapi.WithEventReader(er))
	}
	cpClient := cpapi.NewLocalControlPlaneClient(cp, root, clientOpts...)

	// Wire event bus into runtime backend.
	wireBackendEvents(cp.Backend, cp.Events)

	// Reconcile orphaned queued agents from previous crashes.
	if cp.Queue != nil {
		reconcileOrphanedQueuedAgents(context.Background(), cp.Store, cp.Queue)
	}

	// Shared credential stager for this process.
	sharedStager := credentials.NewInMemoryCredentialStager()

	// Start CP API HTTP server.
	cpAPIServerOpts := []cpapi.HTTPServerOption{
		cpapi.WithCredentialStager(sharedStager),
	}
	if cp.SSEHub != nil && cp.EventStore != nil {
		var er state.EventReader
		if r, ok := cp.Store.(state.EventReader); ok {
			er = r
		}
		cpAPIServerOpts = append(cpAPIServerOpts, cpapi.WithSSE(cp.SSEHub, cp.EventStore, er, cp.SnapshotStore))
	}
	cpAPIServer := cpapi.NewHTTPServer(listenAddr, cpClient, cpAPIServerOpts...)
	if err := cpAPIServer.Start(); err != nil {
		return fmt.Errorf("starting control-plane API: %w", err)
	}
	defer cpAPIServer.Shutdown(context.Background())
	log.Info("control-plane API started", "listen", listenAddr)

	// Start in-process workers when queue is configured.
	if cp.Queue != nil {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		startInProcessWorkers(workerCtx, cp, buildRuntimeRegistry(), cpConfig, sharedStager)
	}

	// Block on signal — no MCP stdio server in this mode.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	log.Info("control-plane API shutting down")
	return nil
}

// runServeGateway constructs and runs a GatewayServer (agent + graph + strategy + gateway proxy).
// No admin tools (no spawn/kill/merge/say/init). Designed for centralized K8s deployment.
func runServeGateway(rc *resolvedConfig, log *slog.Logger) error {
	_, listen := resolveTransport(rc)

	root, _ := FindProjectRoot("")

	// Use explicit config, auto-discovered fracta.yaml, or defaults.
	var cpConfig *config.Config
	if rc.fullConfig != nil {
		cpConfig = rc.fullConfig
	} else {
		var err error
		cpConfig, err = loadConfigOrDefault(root)
		if err != nil {
			return err
		}
	}

	// Build minimal control plane for gateway: store + mailbox + registry only.
	// No backend, queue, reaper, or admission — the gateway doesn't manage agent lifecycle.
	cp, err := controlplane.NewGatewayControlPlane(cpConfig, root)
	if err != nil {
		return fmt.Errorf("creating gateway control plane: %w", err)
	}
	defer cp.Close()

	var gwOpts []mcpserver.GatewayServerOption
	gwOpts = append(gwOpts, mcpserver.WithGatewayStore(cp.Store))
	gwOpts = append(gwOpts, mcpserver.WithGatewayMailbox(cp.Mailbox))
	gwOpts = append(gwOpts, mcpserver.WithGatewayListenAddr(listen))
	if cp.SnapshotStore != nil {
		gwOpts = append(gwOpts, mcpserver.WithGatewaySnapshotStore(cp.SnapshotStore))
	}

	// Objective/proposal stores for objective-aware agent tools.
	if cp.ObjectiveStore != nil {
		gwOpts = append(gwOpts, mcpserver.WithGatewayObjectiveStore(cp.ObjectiveStore))
	}
	if cp.ProposalStore != nil {
		gwOpts = append(gwOpts, mcpserver.WithGatewayProposalStore(cp.ProposalStore))
	}
	// Lifecycle events.
	gwOpts = append(gwOpts, mcpserver.WithGatewayEvents(cp.Events))

	// Graph client
	var gc *graph.FalkorDBClient
	if rc.graphAddr != "" {
		var gcOpts []graph.FalkorDBOption
		if rc.graphName != "" {
			gcOpts = append(gcOpts, graph.WithGraphName(rc.graphName))
		}
		gc = graph.NewFalkorDBClient(rc.graphAddr, gcOpts...)
		gwOpts = append(gwOpts, mcpserver.WithGatewayGraphClient(gc))

		// Multi-schema path
		if cpConfig != nil && len(cpConfig.Ontology.Schemas) > 0 {
			reg, rules, err := loadMultiSchema(cpConfig.Ontology.Schemas)
			if err != nil {
				return fmt.Errorf("multi-schema load: %w", err)
			}
			if err := applySchemaToGraph(gc, reg); err != nil {
				return fmt.Errorf("graph schema apply: %w", err)
			}
			gwOpts = append(gwOpts, mcpserver.WithGatewaySchemaRegistry(reg))
			gwOpts = append(gwOpts, mcpserver.WithGatewayCheckpointRules(rules))
		} else if serveSchemaDir != "" {
			if err := loadAndApplySchema(gc, serveSchemaDir); err != nil {
				return fmt.Errorf("graph schema: %w", err)
			}
		}

		// Run graph migration before reconciler starts.
		if err := graph.MigrateGraph(context.Background(), gc); err != nil {
			log.Warn("graph migration failed (non-fatal)", "error", err)
		}

		resolver := resolve.NewResolver(&graphQuerierAdapter{gc: gc})
		gwOpts = append(gwOpts, mcpserver.WithGatewayResolver(resolver))
	}

	if serveBindingPath != "" {
		bs, err := contract.ParseBindingFile(serveBindingPath)
		if err != nil {
			return fmt.Errorf("loading binding: %w", err)
		}
		gwOpts = append(gwOpts, mcpserver.WithGatewayBinding(bs))
	}

	// MCP client pool + gateway + reconciler
	var mcpFetcher *loaders.MCPFetcher
	if rc.fullConfig != nil && len(rc.fullConfig.MCPServers.Servers) > 0 {
		var poolOpts []mcpclient.PoolOption
		if rc.fullConfig.TokenStore.Driver != "" || true { // always provide factory
			poolOpts = append(poolOpts, mcpclient.WithCredentialStoreFactory(
				&oauthCredStoreFactoryAdapter{cfg: rc.fullConfig.TokenStore},
			))
		}
		pool := mcpclient.NewPool(rc.fullConfig.MCPServers, rc.runtime.Backend, poolOpts...)
		pool.SetEventBus(cp.Events)
		mcpFetcher = loaders.NewMCPFetcher(pool)
		gwOpts = append(gwOpts, mcpserver.WithGatewayPool(pool))

		gw := gateway.New(pool, gc)
		gw.SetEventBus(cp.Events)
		gwOpts = append(gwOpts, mcpserver.WithGatewayGateway(gw))

		if cp.RegistryStore != nil {
			interval := parseReconcileInterval(rc.fullConfig.Registry.ReconcileInterval)
			var graphClient graph.GraphClient
			if gc != nil {
				graphClient = gc
			}
			rec := registry.NewReconciler(cp.RegistryStore, pool, gw, graphClient, interval)
			rec.SetEventBus(cp.Events)
			gw.SetReconcilerActive(true) // single-writer: gateway defers graph writes to reconciler
			pool.SetToolsChangedHandler(func(server string) {
				rec.Trigger(server)
			})
			gwOpts = append(gwOpts, mcpserver.WithGatewayReconciler(rec))
			log.Info("reconciler wired", "interval", interval)
		}

		log.Info("created MCP client pool + gateway", "servers", len(rc.fullConfig.MCPServers.Servers))
	}

	// Strategy runner
	effectiveStrategyDir := serveStrategyDir
	if effectiveStrategyDir == "" && rc.fullConfig != nil {
		effectiveStrategyDir = rc.fullConfig.Strategy.Dir
	}
	if effectiveStrategyDir != "" {
		runner, err := createStrategyRunner(rc)
		if err != nil {
			return fmt.Errorf("strategy runner: %w", err)
		}
		gwOpts = append(gwOpts, mcpserver.WithGatewayStrategyRunner(runner))
		if rc.fullConfig != nil {
			gwOpts = append(gwOpts, mcpserver.WithGatewayAutoPromote(rc.fullConfig.Strategy.AutoPromote))
		}

		stagingDir := rc.runtime.StagingDir
		if stagingDir == "" {
			stagingDir = staging.DefaultStagingDir
		}
		sessions := strategy.NewStagingSessionStore(stagingDir)
		sessionCtx, sessionCancel := context.WithCancel(context.Background())
		sessions.StartJanitor(sessionCtx)
		defer sessionCancel()
		gwOpts = append(gwOpts, mcpserver.WithGatewayStagingSessionStore(sessions))

		// Persistent staging run store (spec-26 async staging).
		stagingRunStore, err := buildStagingRunStore(cp.Store)
		if err != nil {
			log.Warn("staging run store unavailable (degraded)", "error", err)
		} else {
			gwOpts = append(gwOpts, mcpserver.WithGatewayStagingRunStore(stagingRunStore))

			// Periodic reap of terminal/stale runs (replaces janitor for persistent store).
			reapCtx, reapCancel := context.WithCancel(context.Background())
			defer reapCancel()
			go func() {
				ticker := time.NewTicker(5 * time.Minute)
				defer ticker.Stop()
				for {
					select {
					case <-reapCtx.Done():
						return
					case <-ticker.C:
						reaped, err := stagingRunStore.Reap(context.Background(), 24*time.Hour, 2*time.Hour)
						if err != nil {
							fractalog.Component("serve").Warn("staging run reap error", "error", err)
						} else if reaped > 0 {
							fractalog.Component("serve").Info("reaped staging runs", "count", reaped)
						}
					}
				}
			}()

			// Recover interrupted runs from prior pod lifecycle.
			if err := strategy.RecoverActiveRuns(context.Background(), mcpFetcher, runner, stagingRunStore); err != nil {
				log.Warn("staging run recovery failed (non-fatal)", "error", err)
			}
		}
	}

	log.Info("starting gateway server", "listen", listen)
	return mcpserver.NewGatewayServer(root, gwOpts...).Serve()
}

// graphQuerierAdapter adapts graph.GraphClient (returns []graph.Record) to
// resolve.GraphQuerier (returns []map[string]interface{}).
type graphQuerierAdapter struct {
	gc graph.GraphClient
}

func (a *graphQuerierAdapter) Query(ctx context.Context, cypher string, params map[string]any) ([]map[string]interface{}, error) {
	records, err := a.gc.Query(ctx, cypher, params)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]interface{}, len(records))
	for i, r := range records {
		result[i] = map[string]interface{}(r)
	}
	return result, nil
}

// loadMultiSchema loads and merges multiple schema sets from config entries,
// returning the merged registry and aggregated checkpoint rules.
func loadMultiSchema(entries []config.OntologySchemaEntry) (*schema.SchemaRegistry, []schema.CheckpointRule, error) {
	log := fractalog.Component("serve")
	sets := make([]*schema.SchemaSet, 0, len(entries))
	for _, entry := range entries {
		ss, err := schema.LoadSchemaSet(entry.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("loading schema set %q: %w", entry.Path, err)
		}
		sets = append(sets, ss)
		log.Info("loaded schema set", "name", ss.Name, "version", ss.Version, "checkpoints", len(ss.Checkpoint))
	}

	merged, err := schema.MergeSchemas(sets...)
	if err != nil {
		return nil, nil, fmt.Errorf("merging schemas: %w", err)
	}

	var allRules []schema.CheckpointRule
	for _, ss := range sets {
		allRules = append(allRules, ss.Checkpoint...)
	}

	log.Info("merged graph schema", "nodes", len(merged.Nodes), "edges", len(merged.Edges),
		"semantics", len(merged.Semantics), "checkpoint_rules", len(allRules))
	return merged, allRules, nil
}

// applySchemaToGraph applies index and seed statements from a merged registry to FalkorDB.
func applySchemaToGraph(gc *graph.FalkorDBClient, reg *schema.SchemaRegistry) error {
	log := fractalog.Component("serve")
	ctx := context.Background()
	total := 0

	for _, stmt := range reg.GenerateIndexCypher() {
		if err := gc.Update(ctx, stmt, nil); err != nil {
			log.Warn("schema index statement (may already exist)", "error", err, "stmt", stmt)
		}
		total++
	}

	for _, stmt := range reg.GenerateSeedCypher() {
		if err := gc.Update(ctx, stmt, nil); err != nil {
			return fmt.Errorf("seed statement: %w", err)
		}
		total++
	}

	log.Info("graph schema applied", "statements", total)
	return nil
}

// loadAndApplySchema loads graph-schema/ YAML files and applies indexes + seeds to FalkorDB.
// Legacy path for --schema-dir flag.
func loadAndApplySchema(gc *graph.FalkorDBClient, dir string) error {
	log := fractalog.Component("serve")
	registry, err := schema.LoadSchema(dir)
	if err != nil {
		return fmt.Errorf("loading schema: %w", err)
	}

	ctx := context.Background()
	total := 0

	for _, stmt := range registry.GenerateIndexCypher() {
		if err := gc.Update(ctx, stmt, nil); err != nil {
			log.Warn("schema index statement (may already exist)", "error", err, "stmt", stmt)
		}
		total++
	}

	for _, stmt := range registry.GenerateSeedCypher() {
		if err := gc.Update(ctx, stmt, nil); err != nil {
			return fmt.Errorf("seed statement: %w", err)
		}
		total++
	}

	log.Info("graph schema applied", "statements", total)
	return nil
}

// reconcileOrphanedQueuedAgents checks for agents stuck in Queued status whose
// missions no longer exist in the queue (e.g., after a crash with MemoryQueue).
func reconcileOrphanedQueuedAgents(ctx context.Context, store state.Store, q queue.MissionQueue) {
	log := fractalog.Component("serve")
	st, err := store.Load(ctx)
	if err != nil {
		log.Error("reconciliation: failed to load state", "error", err)
		return
	}
	orphaned := 0
	for _, agent := range st.Agents {
		if agent.Mode != "queued" || agent.Status != model.StatusQueued {
			continue
		}
		if agent.MissionID == 0 {
			store.UpdateAgentStatus(ctx, agent.Task, model.StatusFailed, "orphaned: no mission ID")
			orphaned++
			continue
		}
		_, err := q.Status(ctx, agent.MissionID)
		if err != nil {
			store.UpdateAgentStatus(ctx, agent.Task, model.StatusFailed,
				"orphaned: mission not found in queue after restart")
			orphaned++
		}
	}
	if orphaned > 0 {
		log.Info("reconciled orphaned queued agents", "count", orphaned)
	}
}

// startInProcessWorkers launches worker goroutines that share the serve process's
// HostRegistry, Store, and Queue. Workers run until ctx is cancelled.
func startInProcessWorkers(ctx context.Context, cp *controlplane.ControlPlane, reg host.HostRegistry, cfg *config.Config, stager credentials.CredentialStager) {
	log := fractalog.Component("serve")
	numWorkers := cfg.Runtime.Queue.Workers
	if numWorkers <= 0 {
		numWorkers = 2
	}

	wsBase := cfg.Runtime.Queue.WorkspaceBase

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "serve"
	}

	log.Info("starting in-process workers", "count", numWorkers, "workspace_base", wsBase)
	for i := 0; i < numWorkers; i++ {
		id := fmt.Sprintf("%s-worker-%d", hostname, i)
		workerOpts := []worker.Option{
			worker.WithConfig(cfg),
			worker.WithBackend(cp.Backend),
			worker.WithEvents(cp.Events),
			worker.WithLifecycle(cp.Lifecycle),
		}
		if stager != nil {
			workerOpts = append(workerOpts, worker.WithStager(stager))
		}
		w := worker.New(id, cp.Queue, cp.Store, reg, wsBase, workerOpts...)
		go func() {
			if err := w.Run(ctx); err != nil && ctx.Err() == nil {
				log.Error("in-process worker exited", "id", id, "error", err)
			}
		}()
	}
}

// parseReconcileInterval parses a duration string for the reconciler interval.
// Returns 60s on empty or invalid input.
func parseReconcileInterval(s string) time.Duration {
	if s == "" {
		return 60 * time.Second
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		fractalog.Component("serve").Warn("invalid reconcile_interval, using default", "value", s, "error", err)
		return 60 * time.Second
	}
	return d
}

// wireBackendEvents attaches the event bus to the runtime backend if it
// supports the SetEventBus method. Both KubernetesBackend and LocalBackend
// implement SetEventBus; the Backend interface does not require it.
func wireBackendEvents(backend runtime.Backend, bus events.Bus) {
	type eventBusSetter interface {
		SetEventBus(events.Bus)
	}
	if s, ok := backend.(eventBusSetter); ok {
		s.SetEventBus(bus)
	}
}

// buildObjectiveStores constructs ObjectiveStore and ProposalStore from the
// given state store, sharing the same underlying database connection.
func buildObjectiveStores(store state.Store, profile controlplane.Profile) (objective.ObjectiveStore, proposal.ProposalStore, error) {
	switch s := store.(type) {
	case *pgstore.PostgresStore:
		return objective.NewPostgresStore(s.Pool()), proposal.NewPostgresStore(s.Pool()), nil
	case *sqlitestore.SQLiteStore:
		return objective.NewSQLiteStore(s.DB()), proposal.NewSQLiteStore(s.DB()), nil
	default:
		return nil, nil, fmt.Errorf("unsupported store type for objective stores: %T", store)
	}
}

// buildStagingRunStore constructs a StagingRunStore from the given state store,
// sharing the same underlying database connection.
func buildStagingRunStore(store state.Store) (strategy.StagingRunStore, error) {
	switch s := store.(type) {
	case *pgstore.PostgresStore:
		return pgstore.NewPgStagingRunStore(s.Pool()), nil
	case *sqlitestore.SQLiteStore:
		return sqlitestore.NewStagingRunStore(s.DB())
	default:
		return nil, fmt.Errorf("unsupported store type for staging run store: %T", store)
	}
}

// oauthCredStoreFactoryAdapter adapts oauth.CredentialStoreFactory to mcpclient.CredentialStoreFactory.
type oauthCredStoreFactoryAdapter struct {
	cfg config.TokenStoreConfig
}

func (a *oauthCredStoreFactoryAdapter) Build() (mcpclient.OAuthCredentialStore, error) {
	factory := oauth.NewCredentialStoreFactory(a.cfg)
	store, err := factory.Build()
	if err != nil {
		return nil, err
	}
	return &oauthStoreAdapter{store: store}, nil
}

type oauthStoreAdapter struct {
	store oauth.OAuthCredentialStore
}

func (a *oauthStoreAdapter) GetToken(ctx context.Context, server string) (*transport.Token, error) {
	return a.store.GetToken(ctx, server)
}

func (a *oauthStoreAdapter) SaveToken(ctx context.Context, server string, token *transport.Token) error {
	return a.store.SaveToken(ctx, server, token)
}

func (a *oauthStoreAdapter) GetClientRegistration(ctx context.Context, server string) (*mcpclient.OAuthClientRegistration, error) {
	reg, err := a.store.GetClientRegistration(ctx, server)
	if err != nil {
		return nil, err
	}
	return &mcpclient.OAuthClientRegistration{
		ClientID:     reg.ClientID,
		ClientSecret: reg.ClientSecret,
	}, nil
}
