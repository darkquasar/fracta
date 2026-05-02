package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/darkquasar/fracta/internal/agentlifecycle"
	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/mailbox"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/queue"
	"github.com/darkquasar/fracta/internal/runtime"
	"github.com/darkquasar/fracta/internal/state"
	"github.com/darkquasar/fracta/internal/workspace"
)

// SpawnChecker validates whether a new agent can be spawned.
// Implemented by controlplane.Reaper. Called inside WithLock for atomicity.
type SpawnChecker interface {
	CheckSpawnAllowed(st *model.State) error
}

type Orchestrator struct {
	HostRegistry   host.HostRegistry // resolves host implementations by type
	Config         *config.Config    // unified execution policy from fracta.yaml
	Workspace      workspace.Workspace
	Store          state.Store
	Mailbox        mailbox.Mailbox
	Backend        runtime.Backend
	Queue          queue.MissionQueue // nil when queue not configured
	Root           string
	MCPServers     config.MCPServersConfig
	RuntimeBackend string // "local" or "kubernetes" — controls MCP server delivery mode
	SpawnChecker   SpawnChecker
	Events         events.Bus // lifecycle event bus (nil-safe via NoopBus)
	Lifecycle      *agentlifecycle.Writer
	Logger         *slog.Logger

	// Shared state discovery — threaded into WorkspaceConfig for agent-mode.
	ConfigPath  string
	GraphAddr   string
	StrategyDir string

	// Snapshot debounce state (per-orchestrator, not global).
	snapshotMu    sync.Mutex
	snapshotTimer *time.Timer
	snapshotDirty bool
}

func New(reg host.HostRegistry, ws workspace.Workspace, store state.Store, mb mailbox.Mailbox, root string) *Orchestrator {
	o := &Orchestrator{
		HostRegistry: reg,
		Workspace:    ws,
		Store:        store,
		Mailbox:      mb,
		Root:         root,
		Logger:       fractalog.Component("orchestrator"),
	}
	o.Lifecycle = agentlifecycle.New(store, events.NoopBus{},
		agentlifecycle.WithProgressHook(o.SnapshotProgress),
	)
	return o
}

// emit sends a structured event via the bus. Nil-safe. The mutators allow
// call sites to set category, task, outcome, etc. on the pre-built event.
func (o *Orchestrator) emit(ctx context.Context, e events.Event, mutators ...func(*events.Event)) {
	if o.Events == nil {
		return
	}
	for _, m := range mutators {
		m(&e)
	}
	o.Events.Emit(ctx, e)
}

// emitAgentResult emits a structured complete/fail event based on agent status.
func (o *Orchestrator) emitAgentResult(ctx context.Context, task string, status model.AgentStatus, lastOutput string) {
	if o.Events == nil {
		return
	}
	switch status {
	case model.StatusCompleted:
		o.emit(ctx, events.Info("orchestrator", "complete"),
			func(e *events.Event) {
				e.Category = "agent"
				e.Task = task
				e.Outcome = "success"
				e.Resource = "task:" + task
			})
	case model.StatusFailed:
		detail := lastOutput
		if len(detail) > 512 {
			detail = detail[:512]
		}
		o.emit(ctx, events.Warn("orchestrator", "fail", detail),
			func(e *events.Event) {
				e.Category = "agent"
				e.Task = task
				e.Outcome = "failure"
				e.Resource = "task:" + task
			})
	}
}

// TerminalMeta carries context for terminal agent transitions.
// Explicit fields prevent the "forgotten side effect" pattern (Lesson 6).
type TerminalMeta struct {
	Reason      string // human-readable reason for the transition
	RuntimeType string // runtime type (claude, codex, etc.) — empty if unknown
	Backend     string // backend type (local, kubernetes) — empty if unknown
}

// transitionAgentToTerminal is the single choke point for all orchestrator-owned
// terminal transitions. Delegates to the lifecycle writer for coordinated
// state update + event emission + progress snapshot.
func (o *Orchestrator) transitionAgentToTerminal(ctx context.Context, task string, status model.AgentStatus, meta TerminalMeta) {
	lm := agentlifecycle.LifecycleMeta{
		RuntimeType: meta.RuntimeType,
		Backend:     meta.Backend,
		Reason:      meta.Reason,
	}

	var err error
	switch status {
	case model.StatusCompleted:
		err = o.Lifecycle.MarkCompleted(ctx, task, agentlifecycle.ResultMeta{
			LifecycleMeta: lm,
			LastOutput:    meta.Reason,
		})
	case model.StatusFailed:
		err = o.Lifecycle.MarkFailed(ctx, task, agentlifecycle.ResultMeta{
			LifecycleMeta: lm,
			LastOutput:    meta.Reason,
		})
	case model.StatusStopped:
		err = o.Lifecycle.MarkStopped(ctx, task, lm)
	default:
		o.Logger.Warn("unexpected terminal status", "task", task, "status", status)
		return
	}

	if err != nil && err != agentlifecycle.ErrTransitionSkipped {
		o.Logger.Warn("lifecycle transition failed", "task", task, "status", status, "error", err)
	}
	o.SnapshotProgress()
}

// resolveHost returns the Host for the given runtimeType, falling back to the
// registry default when runtimeType is empty.
func (o *Orchestrator) resolveHost(runtimeType string) (string, host.Host, error) {
	if runtimeType == "" {
		name, h := o.HostRegistry.Default()
		if h == nil {
			return "", nil, fmt.Errorf("no default runtime registered (key %q not found in registry)", name)
		}
		return name, h, nil
	}
	h, err := o.HostRegistry.Get(runtimeType)
	return runtimeType, h, err
}

// resolveRuntimeConfig returns the RuntimeEntry for the given runtime type from the
// unified config. Returns an error if the config is nil or the runtime type is
// not configured — never returns a zero-value RuntimeEntry on miss.
func (o *Orchestrator) resolveRuntimeConfig(runtimeType string) (config.RuntimeEntry, error) {
	if o.Config == nil {
		return config.RuntimeEntry{}, fmt.Errorf("no config loaded")
	}
	runtimes := o.Config.EffectiveRuntimes()
	hc, ok := runtimes[runtimeType]
	if !ok {
		return config.RuntimeEntry{}, fmt.Errorf("runtime %q not configured in fracta.yaml agents.agent_runtimes", runtimeType)
	}
	return hc, nil
}

// withState wraps Store.WithLock and refreshes the progress snapshot after the
// mutation succeeds.
func (o *Orchestrator) withState(fn func(*model.State) error) error {
	if err := o.Store.WithLock(context.Background(), fn); err != nil {
		return err
	}
	o.SnapshotProgress()
	return nil
}
