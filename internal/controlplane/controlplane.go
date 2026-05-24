package controlplane

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/darkquasar/fracta/internal/admission"
	"github.com/darkquasar/fracta/internal/agentlifecycle"
	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/mailbox"
	"github.com/darkquasar/fracta/internal/objective"
	"github.com/darkquasar/fracta/internal/proposal"
	"github.com/darkquasar/fracta/internal/queue"
	"github.com/darkquasar/fracta/internal/registry"
	"github.com/darkquasar/fracta/internal/registry/pgregistry"
	"github.com/darkquasar/fracta/internal/registry/sqliteregistry"
	"github.com/darkquasar/fracta/internal/runtime"
	"github.com/darkquasar/fracta/internal/state"
	"github.com/darkquasar/fracta/internal/state/pgstore"
	"github.com/darkquasar/fracta/internal/state/sqlitestore"
	"github.com/darkquasar/fracta/internal/workspace"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ControlPlane holds the runtime backend, state store, workspace, and reaper.
// It is the single point of construction for all infrastructure components.
type ControlPlane struct {
	Backend        runtime.Backend
	Store          state.Store
	RegistryStore  registry.Store
	Mailbox        mailbox.Mailbox
	Workspace      workspace.Workspace
	Queue          queue.MissionQueue // nil when queue not configured
	Profile        Profile            // resolved profile — authoritative for backend type, workspace type, paths
	Reaper         *Reaper
	Admission      *admission.AdmissionController // nil when queue not configured
	ObjectiveStore objective.ObjectiveStore       // nil when queue not configured
	ProposalStore  proposal.ProposalStore         // nil when queue not configured
	Config         *config.Config
	Events         events.Bus             // lifecycle event bus (always set — NoopBus when unconfigured)
	Lifecycle      *agentlifecycle.Writer // lifecycle transition coordinator

	// Observability stores (spec-35).
	SnapshotStore *events.SnapshotStore // in-memory per-agent state projected from events
	EventStore    *events.EventStore    // per-agent ring buffer of recent events
	SSEHub        *events.SSEHub        // subscriber management for SSE watch
	SnapshotSink  *events.SnapshotSink  // sink that projects events to snapshots

	mu             sync.RWMutex
	logger         *slog.Logger
	admissionStop  context.CancelFunc // cancels the admission controller goroutine
	obsMaintenStop context.CancelFunc // cancels the observability maintenance goroutine
}

// NewControlPlane constructs a ControlPlane from config using the resolved
// profile to select appropriate implementations for each subsystem.
// root is the project root directory for resolving relative paths.
func NewControlPlane(cfg *config.Config, root string) (*ControlPlane, error) {
	profile := ResolveProfile(cfg, root)

	backend, err := buildBackend(cfg, profile)
	if err != nil {
		return nil, fmt.Errorf("controlplane: building backend: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := BuildStore(ctx, profile)
	if err != nil {
		return nil, fmt.Errorf("controlplane: building store: %w", err)
	}

	// Non-gateway processes must not bootstrap the registry: they don't own
	// MCP server config, so SyncConfigServers with their (typically empty)
	// MCPServers would disable all config-owned registry rows.
	regStore, err := buildRegistryStore(ctx, profile, false)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("controlplane: building registry store: %w", err)
	}

	mb := buildMailbox(store)
	ws := buildWorkspace(profile)

	q, err := buildQueue(cfg, store)
	if err != nil {
		store.Close()
		if regStore != nil {
			regStore.Close()
		}
		return nil, fmt.Errorf("controlplane: building queue: %w", err)
	}

	// Construct event bus with appropriate sinks.
	bus, obs := buildEventBus(store, cfg)

	// Attach K8sEventSink if the backend provides a recorder.
	attachK8sEventSink(backend, bus)

	// Build reaper (needed as admission checker for lifecycle writer).
	reaper := NewReaper(store, backend, cfg.Reaper)
	if sb, ok := backend.(runtime.StreamBackend); ok {
		reaper.SetStreamBackend(sb)
	}
	if q != nil {
		reaper.SetQueue(q)
	}
	reaper.SetMailbox(mb)
	reaper.SetEventBus(bus)

	// Build lifecycle writer with admission check wired to reaper's concurrency limit.
	lifecycle := agentlifecycle.New(store, bus,
		agentlifecycle.WithAdmissionCheck(reaper.CheckSpawnAllowed),
	)
	reaper.SetLifecycle(lifecycle)
	reaper.Start()

	cp := &ControlPlane{
		Backend:       backend,
		Store:         store,
		RegistryStore: regStore,
		Mailbox:       mb,
		Workspace:     ws,
		Queue:         q,
		Profile:       profile,
		Reaper:        reaper,
		Config:        cfg,
		Events:        bus,
		Lifecycle:     lifecycle,
		SnapshotStore: obs.snapshotStore,
		EventStore:    obs.eventStore,
		SSEHub:        obs.sseHub,
		SnapshotSink:  obs.snapshotSink,
		logger:        fractalog.Component("controlplane"),
	}

	// Start observability maintenance (unresponsive checks + TTL cleanup).
	if obs.snapshotSink != nil {
		// Resolve interval on the calling goroutine to avoid racing with Reconfigure.
		maintInterval := 15 * time.Second
		if cfg != nil && cfg.Observability.HeartbeatInterval.Duration > 0 {
			maintInterval = cfg.Observability.HeartbeatInterval.Duration
		}
		obsCtx, obsCancel := context.WithCancel(context.Background())
		cp.obsMaintenStop = obsCancel
		go cp.runObservabilityMaintenance(obsCtx, obs, maintInterval)
	}

	// Build admission controller when queue is configured.
	if q != nil {
		objStore, propStore, reader := buildAdmissionStores(store, profile)
		if objStore != nil {
			cp.ObjectiveStore = objStore
			cp.ProposalStore = propStore
			reaper.SetObjectiveStore(objStore)
			ac := admission.New(propStore, objStore, reader, q, store, mb)
			cp.Admission = ac
			admCtx, admCancel := context.WithCancel(context.Background())
			cp.admissionStop = admCancel
			go ac.Run(admCtx)
		}
	}

	return cp, nil
}

// Reconfigure updates the control plane with new config via SIGHUP.
// Only the reaper config is hot-reloadable. The following are NOT
// re-resolved on reload and require a full restart:
//   - profile / runtime.backend (backend, workspace type)
//   - runtime.state.path (database location)
//   - auth (provider, region, secret)
//   - connections (elasticsearch, falkordb, snowflake)
func (cp *ControlPlane) Reconfigure(cfg *config.Config) error {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	cp.Reaper.Reconfigure(cfg.Reaper)
	cp.Config = cfg
	cp.logger.Info("reconfigured",
		"max_concurrent", cfg.Reaper.MaxConcurrent,
		"max_age", cfg.Reaper.MaxAge.Duration,
		"interval", cfg.Reaper.Interval.Duration,
	)
	return nil
}

// NewGatewayControlPlane constructs a minimal ControlPlane for gateway mode.
// It builds Store, Mailbox, RegistryStore, Events, and ObjectiveStore/ProposalStore
// (for objective-aware agent tools). No Backend, Queue, Reaper, AdmissionController,
// or Workspace. The gateway serves agent tools and proxied MCP backends; it does not
// spawn, kill, or manage agent lifecycle.
func NewGatewayControlPlane(cfg *config.Config, root string) (*ControlPlane, error) {
	profile := ResolveProfile(cfg, root)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store, err := BuildStore(ctx, profile)
	if err != nil {
		return nil, fmt.Errorf("controlplane: building store: %w", err)
	}

	regStore, err := buildRegistryStore(ctx, profile, profile.RegistryBootstrap)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("controlplane: building registry store: %w", err)
	}

	mb := buildMailbox(store)

	// Gateway control plane: minimal bus with LogSink + StoreSink only.
	// No K8sEventSink — gateway has no backend/runtime config.
	bus := buildGatewayEventBus(store)

	// Build objective/proposal stores so gateway can serve objective tools.
	// The gateway doesn't need the admission controller or mission reader —
	// just the stores for per-request objective context resolution.
	objStore, propStore, _ := buildAdmissionStores(store, profile)

	return &ControlPlane{
		Store:          store,
		RegistryStore:  regStore,
		Mailbox:        mb,
		ObjectiveStore: objStore,
		ProposalStore:  propStore,
		Profile:        profile,
		Config:         cfg,
		Events:         bus,
		logger:         fractalog.Component("controlplane"),
	}, nil
}

// Close stops the reaper, admission controller, observability maintenance, and releases store resources.
func (cp *ControlPlane) Close() {
	if cp.obsMaintenStop != nil {
		cp.obsMaintenStop()
	}
	if cp.admissionStop != nil {
		cp.admissionStop()
	}
	if cp.Reaper != nil {
		cp.Reaper.Stop()
	}
	if cp.Queue != nil {
		cp.Queue.Close()
	}
	if cp.RegistryStore != nil {
		cp.RegistryStore.Close()
	}
	cp.Store.Close()
}

// runObservabilityMaintenance periodically runs unresponsive checks and TTL
// cleanup for snapshot and event stores. Runs until ctx is cancelled.
func (cp *ControlPlane) runObservabilityMaintenance(ctx context.Context, obs observabilityStores, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			obs.snapshotSink.CheckAllUnresponsive(now)
			obs.snapshotStore.Cleanup(now)
			obs.eventStore.Cleanup(now)
		}
	}
}

// observabilityStores holds the in-memory stores created by buildEventBus.
type observabilityStores struct {
	snapshotStore *events.SnapshotStore
	eventStore    *events.EventStore
	sseHub        *events.SSEHub
	snapshotSink  *events.SnapshotSink
}

// buildEventBus constructs the FanoutBus for a full ControlPlane.
// Sinks: LogSink (always) + StoreSink (when store implements EventInserter)
// + SnapshotSink + RingBufferSink (observability, spec-35).
// K8sEventSink is added separately by attachK8sEventSink after the backend
// is available, since the recorder comes from the K8s runtime backend.
func buildEventBus(store state.Store, cfg *config.Config) (*events.FanoutBus, observabilityStores) {
	sinks := []events.Sink{events.NewLogSink()}

	if inserter, ok := store.(events.EventInserter); ok {
		sinks = append(sinks, events.NewStoreSink(inserter))
	}

	// Observability sinks (spec-35).
	heartbeatInterval := 15 * time.Second
	ringSize := events.DefaultRingSize
	if cfg != nil {
		if cfg.Observability.HeartbeatInterval.Duration > 0 {
			heartbeatInterval = cfg.Observability.HeartbeatInterval.Duration
		}
		if cfg.Observability.RingSize > 0 {
			ringSize = cfg.Observability.RingSize
		}
	}

	snapshotStore := events.NewSnapshotStore(0)
	eventStore := events.NewEventStore(ringSize, 0)
	sseHub := events.NewSSEHub(0)
	snapshotSink := events.NewSnapshotSink(snapshotStore, heartbeatInterval)
	ringBufferSink := events.NewRingBufferSink(eventStore, sseHub)

	sinks = append(sinks, snapshotSink, ringBufferSink)

	return events.NewFanoutBus(sinks...), observabilityStores{
		snapshotStore: snapshotStore,
		eventStore:    eventStore,
		sseHub:        sseHub,
		snapshotSink:  snapshotSink,
	}
}

// attachK8sEventSink adds a K8sEventSink to the bus if the backend provides
// a K8sEventRecorder. This is called after both the backend and bus are
// constructed in NewControlPlane, keeping all sink assembly in the control plane.
func attachK8sEventSink(backend runtime.Backend, bus *events.FanoutBus) {
	type recorderProvider interface {
		NewEventRecorder() events.K8sEventRecorder
	}
	rp, ok := backend.(recorderProvider)
	if !ok {
		return
	}
	bus.AddSink(events.NewK8sEventSink(rp.NewEventRecorder()))
}

// buildGatewayEventBus constructs a minimal FanoutBus for gateway mode.
// Sinks: LogSink + StoreSink only. No K8sEventSink.
func buildGatewayEventBus(store state.Store) events.Bus {
	sinks := []events.Sink{events.NewLogSink()}

	if inserter, ok := store.(events.EventInserter); ok {
		sinks = append(sinks, events.NewStoreSink(inserter))
	}

	return events.NewFanoutBus(sinks...)
}

// buildBackend constructs the runtime backend using the profile's resolved backend type.
func buildBackend(cfg *config.Config, profile Profile) (runtime.Backend, error) {
	switch profile.BackendType {
	case "local", "":
		return runtime.NewLocalBackend(profile.ProjectRoot), nil
	case "kubernetes":
		return buildK8sBackend(cfg)
	default:
		return nil, fmt.Errorf("unknown runtime backend: %q", cfg.Runtime.Backend)
	}
}

// buildK8sBackend constructs a Kubernetes backend using client-go's
// standard config resolution (in-cluster first, then kubeconfig).
func buildK8sBackend(cfg *config.Config) (runtime.Backend, error) {
	ns := cfg.Runtime.Kubernetes.Namespace
	if ns == "" {
		ns = "fracta"
	}

	restCfg, err := rest.InClusterConfig()
	if err != nil {
		// Fall back to kubeconfig
		rules := clientcmd.NewDefaultClientConfigLoadingRules()
		restCfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			rules, &clientcmd.ConfigOverrides{},
		).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("building kubernetes config: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("building kubernetes clientset: %w", err)
	}

	k8s := cfg.Runtime.Kubernetes
	jobCfg := runtime.KubernetesJobConfig{
		Image:           k8s.Image,
		ImagePullPolicy: k8s.ImagePullPolicy,
		ServiceAccount:  k8s.ServiceAccount,
		PVC:             k8s.PVC,
		PVCMountPath:    k8s.PVCMountPath,
		Labels:          k8s.Labels,
		Annotations:     k8s.Annotations,
		NodeSelector:    k8s.NodeSelector,
		Resources: runtime.ResourceRequirements{
			CPURequest:    k8s.Resources.CPURequest,
			CPULimit:      k8s.Resources.CPULimit,
			MemoryRequest: k8s.Resources.MemoryRequest,
			MemoryLimit:   k8s.Resources.MemoryLimit,
		},
		JobTTLSeconds:     int32(k8s.JobTTLSeconds),
		ExtraVolumes:      k8s.ExtraVolumes,
		ExtraVolumeMounts: k8s.ExtraVolumeMounts,
	}

	return runtime.NewKubernetesBackend(clientset, ns, jobCfg), nil
}

// buildMailbox returns the mailbox associated with the store.
func buildMailbox(store state.Store) mailbox.Mailbox {
	return store.Mailbox()
}

// BuildStore constructs the state store using the profile's driver setting.
// ctx is used for Postgres connection setup (ping, migration).
// Exported so cmd/serve.go agent-mode can use the same dispatch logic.
func BuildStore(ctx context.Context, profile Profile) (state.Store, error) {
	switch profile.StateDriver {
	case "postgres":
		var opts []pgstore.Option
		if profile.PostgresConfig.MaxConns > 0 {
			opts = append(opts, pgstore.WithMaxConns(profile.PostgresConfig.MaxConns))
		}
		if profile.PostgresConfig.MinConns > 0 {
			opts = append(opts, pgstore.WithMinConns(profile.PostgresConfig.MinConns))
		}
		if profile.PostgresConfig.MaxConnLifetime != "" {
			d, err := time.ParseDuration(profile.PostgresConfig.MaxConnLifetime)
			if err != nil {
				return nil, fmt.Errorf("invalid max_conn_lifetime %q: %w", profile.PostgresConfig.MaxConnLifetime, err)
			}
			opts = append(opts, pgstore.WithMaxConnLifetime(d))
		}
		if profile.PostgresConfig.MaxConnIdleTime != "" {
			d, err := time.ParseDuration(profile.PostgresConfig.MaxConnIdleTime)
			if err != nil {
				return nil, fmt.Errorf("invalid max_conn_idle_time %q: %w", profile.PostgresConfig.MaxConnIdleTime, err)
			}
			opts = append(opts, pgstore.WithMaxConnIdleTime(d))
		}
		return pgstore.New(ctx, profile.PostgresConfig.DSN, opts...)
	case "sqlite", "":
		return sqlitestore.New(profile.StatePath)
	default:
		return nil, fmt.Errorf("unknown state driver: %q", profile.StateDriver)
	}
}

// buildWorkspace selects a workspace implementation based on the profile.
func buildWorkspace(profile Profile) workspace.Workspace {
	switch profile.WorkspaceType {
	case "directory":
		return workspace.NewDirectoryWorkspace(profile.WorkspaceBase)
	default:
		return workspace.NewGitWorkspace(profile.ProjectRoot)
	}
}

// buildQueue constructs the MissionQueue based on config.
// Returns nil if queue is not configured (opt-in).
func buildQueue(cfg *config.Config, store state.Store) (queue.MissionQueue, error) {
	backend := cfg.Runtime.Queue.Backend
	if backend == "" {
		return nil, nil // queue not configured — direct spawn only
	}

	switch backend {
	case "memory":
		return queue.NewMemoryQueue(store, 100), nil
	case "postgres":
		pgStore, ok := store.(*pgstore.PostgresStore)
		if !ok {
			return nil, fmt.Errorf("postgres queue requires postgres state driver")
		}
		var opts []queue.QueueOption
		if cfg.Runtime.Queue.LeaseTimeout.Duration > 0 {
			opts = append(opts, queue.WithLeaseTimeout(cfg.Runtime.Queue.LeaseTimeout.Duration))
		}
		return queue.NewPostgresQueue(pgStore.Pool(), opts...), nil
	default:
		return nil, fmt.Errorf("unknown queue backend: %q", backend)
	}
}

// buildRegistryStore constructs the registry store and optionally runs bootstrap.
// Only the gateway process should pass bootstrap=true — it is the authoritative
// owner of MCP server config. Non-gateway processes (controlplane daemon, worker,
// registry CLI) must pass false to avoid nuking gateway-managed registry rows.
func buildRegistryStore(ctx context.Context, profile Profile, bootstrap bool) (registry.Store, error) {
	var store registry.Store
	var err error

	switch profile.RegistryDriver {
	case "sqlite", "":
		store, err = sqliteregistry.New(ctx, profile.StatePath)
	case "postgres":
		if profile.RegistryPostgresConfig.DSN == "" {
			return nil, fmt.Errorf("registry driver is postgres but no DSN configured (set registry.postgres.dsn or runtime.state.postgres.dsn)")
		}
		store, err = pgregistry.New(ctx, profile.RegistryPostgresConfig.DSN)
	default:
		return nil, fmt.Errorf("unknown registry driver: %q", profile.RegistryDriver)
	}
	if err != nil {
		return nil, err
	}

	if bootstrap {
		if err := store.SyncConfigServers(ctx, profile.MCPServers); err != nil {
			store.Close()
			return nil, fmt.Errorf("registry sync config: %w", err)
		}
	}

	return store, nil
}

// buildAdmissionStores constructs the ObjectiveStore, ProposalStore, and
// MissionReader from the state store. Returns (nil, nil, nil) if the store
// type is not supported (e.g., during testing with a mock store).
func buildAdmissionStores(store state.Store, profile Profile) (objective.ObjectiveStore, proposal.ProposalStore, admission.MissionReader) {
	switch s := store.(type) {
	case *pgstore.PostgresStore:
		pool := s.Pool()
		return objective.NewPostgresStore(pool),
			proposal.NewPostgresStore(pool),
			admission.NewPostgresMissionReader(pool)
	case *sqlitestore.SQLiteStore:
		db := s.DB()
		return objective.NewSQLiteStore(db),
			proposal.NewSQLiteStore(db),
			admission.NewSQLiteMissionReader(db)
	default:
		return nil, nil, nil
	}
}
