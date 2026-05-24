package agentlifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/state"
)

// ErrTransitionSkipped is returned when a transition was not applied because
// the agent was not in the expected state (e.g., already terminal).
var ErrTransitionSkipped = errors.New("lifecycle transition skipped: agent not in expected state")

// Writer coordinates agent lifecycle transitions: durable state →
// canonical event → progress hook. All lifecycle transitions should
// go through this service rather than directly mutating the store.
type Writer struct {
	store          state.Store
	bus            events.Bus
	admissionCheck func(*model.State) error
	progressHook   func()
	logger         *slog.Logger
	clock          func() time.Time
}

// Option configures a Writer.
type Option func(*Writer)

// WithAdmissionCheck sets the spawn admission gate called inside WithLock
// during RecordAgentStarted.
func WithAdmissionCheck(fn func(*model.State) error) Option {
	return func(w *Writer) { w.admissionCheck = fn }
}

// WithProgressHook sets the callback invoked after successful transitions.
func WithProgressHook(fn func()) Option {
	return func(w *Writer) { w.progressHook = fn }
}

// WithClock overrides the time source (for testing).
func WithClock(fn func() time.Time) Option {
	return func(w *Writer) { w.clock = fn }
}

// CloneWithBus returns a shallow copy of the writer with a different event bus.
// Used by workers in split-deployment mode to emit lifecycle events via a
// per-mission RemoteBus instead of the process-global bus.
func (w *Writer) CloneWithBus(bus events.Bus) *Writer {
	clone := *w
	clone.bus = bus
	return &clone
}

// CloneWithProgressHook returns a shallow copy with a different progress hook.
// Used by orchestrators to wire SnapshotProgress into a CP-level writer.
func (w *Writer) CloneWithProgressHook(hook func()) *Writer {
	clone := *w
	clone.progressHook = hook
	return &clone
}

// New creates a Writer with the given dependencies.
func New(store state.Store, bus events.Bus, opts ...Option) *Writer {
	w := &Writer{
		store:  store,
		bus:    bus,
		logger: fractalog.Component("agentlifecycle"),
		clock:  time.Now,
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

// RecordAgentStarted creates a new agent via WithLock (preserving spawn
// admission atomicity) and emits lifecycle.started.
func (w *Writer) RecordAgentStarted(ctx context.Context, task string, meta CreationMeta) error {
	entry := model.AgentEntry{
		Task:          task,
		RuntimeType:   meta.RuntimeType,
		WorkspacePath: meta.WorkspacePath,
		BranchName:    meta.BranchName,
		BaseBranch:    meta.BaseBranch,
		Status:        model.StatusRunning,
		StartTime:     w.clock(),
		Mode:          meta.Mode,
		MissionID:     meta.MissionID,
		ObjectiveID:   meta.ObjectiveID,
	}

	if err := w.store.WithLock(ctx, func(st *model.State) error {
		if w.admissionCheck != nil {
			if err := w.admissionCheck(st); err != nil {
				return err
			}
		}
		st.Agents = append(st.Agents, entry)
		return nil
	}); err != nil {
		return err
	}

	w.emitLifecycleEvent(ctx, task, "lifecycle.started", "info", meta.LifecycleMeta)
	w.triggerProgress()
	return nil
}

// ClaimQueuedAgent transitions Queued→Running (including start_time reset)
// and emits lifecycle.started. This is the queued agent's execution-begin moment.
// Uses Store.ClaimAgent which atomically sets status + start_time.
func (w *Writer) ClaimQueuedAgent(ctx context.Context, task string, meta LifecycleMeta) error {
	err := w.store.ClaimAgent(ctx, task)
	if err != nil {
		if errors.Is(err, state.ErrAgentNotClaimable) {
			w.logger.Info("ClaimQueuedAgent skipped — agent not claimable", "task", task)
			return ErrTransitionSkipped
		}
		return fmt.Errorf("claim agent %s: %w", task, err)
	}

	w.emitLifecycleEvent(ctx, task, "lifecycle.started", "info", meta)
	w.triggerProgress()
	return nil
}

// MarkRunning transitions Idle→Running. No lifecycle event emitted.
func (w *Writer) MarkRunning(ctx context.Context, task string, meta LifecycleMeta) error {
	ok, err := w.store.UpdateAgentStatusIf(ctx, task,
		[]model.AgentStatus{model.StatusIdle},
		model.StatusRunning, "")
	if err != nil {
		return err
	}
	if !ok {
		w.logger.Info("MarkRunning skipped — agent not in Idle state", "task", task)
		return ErrTransitionSkipped
	}

	w.triggerProgress()
	return nil
}

// MarkIdle transitions Running→Idle. No lifecycle event emitted.
func (w *Writer) MarkIdle(ctx context.Context, task string, output, resumeToken string, meta LifecycleMeta) error {
	ok, err := w.store.UpdateAgentResultIf(ctx, task,
		[]model.AgentStatus{model.StatusRunning},
		model.StatusIdle, output, resumeToken)
	if err != nil {
		return err
	}
	if !ok {
		w.logger.Info("MarkIdle skipped — agent not in Running state", "task", task)
		return ErrTransitionSkipped
	}

	w.triggerProgress()
	return nil
}

// MarkCompleted transitions to Completed and emits lifecycle.completed.
func (w *Writer) MarkCompleted(ctx context.Context, task string, result ResultMeta) error {
	expected := []model.AgentStatus{model.StatusRunning, model.StatusIdle}
	ok, err := w.store.UpdateAgentResultIf(ctx, task,
		expected, model.StatusCompleted, result.LastOutput, result.ResumeToken)
	if err != nil {
		return err
	}
	if !ok {
		w.logger.Info("MarkCompleted skipped — agent already terminal", "task", task)
		return ErrTransitionSkipped
	}

	w.emitLifecycleEvent(ctx, task, "lifecycle.completed", "info", result.LifecycleMeta)
	w.triggerProgress()
	return nil
}

// MarkFailed transitions to Failed and emits lifecycle.failed.
func (w *Writer) MarkFailed(ctx context.Context, task string, result ResultMeta) error {
	expected := []model.AgentStatus{model.StatusRunning, model.StatusIdle, model.StatusQueued}
	ok, err := w.store.UpdateAgentResultIf(ctx, task,
		expected, model.StatusFailed, result.LastOutput, result.ResumeToken)
	if err != nil {
		return err
	}
	if !ok {
		w.logger.Info("MarkFailed skipped — agent already terminal", "task", task)
		return ErrTransitionSkipped
	}

	w.emitLifecycleEvent(ctx, task, "lifecycle.failed", "error", result.LifecycleMeta)
	w.triggerProgress()
	return nil
}

// MarkStopped transitions to Stopped and emits lifecycle.stopped.
func (w *Writer) MarkStopped(ctx context.Context, task string, meta LifecycleMeta) error {
	expected := []model.AgentStatus{model.StatusQueued, model.StatusRunning, model.StatusIdle}
	ok, err := w.store.UpdateAgentStatusIf(ctx, task,
		expected, model.StatusStopped, meta.Reason)
	if err != nil {
		return err
	}
	if !ok {
		w.logger.Info("MarkStopped skipped — agent already terminal", "task", task)
		return ErrTransitionSkipped
	}

	w.emitLifecycleEvent(ctx, task, "lifecycle.stopped", "warn", meta)
	w.triggerProgress()
	return nil
}

func (w *Writer) emitLifecycleEvent(ctx context.Context, task, action, severity string, meta LifecycleMeta) {
	if w.bus == nil {
		return
	}

	attrs := make(map[string]string)
	if meta.RuntimeType != "" {
		attrs["runtime"] = meta.RuntimeType
	}
	if meta.Backend != "" {
		attrs["backend"] = meta.Backend
	}
	if meta.MissionID != 0 {
		attrs["mission_id"] = formatInt64(meta.MissionID)
	}

	w.bus.Emit(ctx, events.Event{
		ID:          uuid.NewString(),
		Time:        w.clock(),
		Component:   "agentlifecycle",
		Category:    "agent_activity",
		Action:      action,
		Severity:    severity,
		Task:        task,
		MissionID:   meta.MissionID,
		ObjectiveID: meta.ObjectiveID,
		Detail:      meta.Reason,
		Attrs:       attrs,
	})
}

func (w *Writer) triggerProgress() {
	if w.progressHook != nil {
		w.progressHook()
	}
}

func formatInt64(n int64) string {
	return fmt.Sprintf("%d", n)
}
