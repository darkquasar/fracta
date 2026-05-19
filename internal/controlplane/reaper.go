// Package controlplane provides the ControlPlane and Reaper types that manage
// agent lifecycle, TTL enforcement, and concurrency limits.
package controlplane

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/darkquasar/fracta/internal/agentlifecycle"
	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/mailbox"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/objective"
	"github.com/darkquasar/fracta/internal/queue"
	"github.com/darkquasar/fracta/internal/runtime"
	"github.com/darkquasar/fracta/internal/state"
)

// Reaper periodically scans state for agents that have exceeded their max age
// and kills them via the runtime backend. It also enforces max_concurrent limits.
type Reaper struct {
	store          state.Store
	backend        runtime.Backend
	streamBackend  runtime.StreamBackend        // optional — for stream pod cleanup
	queue          queue.MissionQueue          // optional — nil when queue not configured
	mailbox        mailbox.Mailbox             // optional — for terminal queued agent cleanup
	objectiveStore objective.ObjectiveStore     // optional — for objective timeout enforcement
	bus            events.Bus                  // optional — emits reap lifecycle events
	lifecycle      *agentlifecycle.Writer      // lifecycle transition coordinator
	cfg            config.ReaperConfig
	mu             sync.RWMutex
	stopCh         chan struct{}
	done           chan struct{}
	logger         *slog.Logger
}

// NewReaper creates a Reaper with the given store, backend, and configuration.
// Call Start() to begin the background reap loop.
func NewReaper(store state.Store, backend runtime.Backend, cfg config.ReaperConfig) *Reaper {
	return &Reaper{
		store:   store,
		backend: backend,
		cfg:     cfg,
		stopCh:  make(chan struct{}),
		done:    make(chan struct{}),
		logger:  fractalog.Component("reaper"),
	}
}

// SetQueue configures the queue for the reaper. Must be called before Start().
func (r *Reaper) SetQueue(q queue.MissionQueue) {
	r.queue = q
}

// SetStreamBackend configures the stream backend for stream pod cleanup. Must be called before Start().
func (r *Reaper) SetStreamBackend(sb runtime.StreamBackend) {
	r.streamBackend = sb
}

// SetMailbox configures the mailbox for the reaper. Must be called before Start().
func (r *Reaper) SetMailbox(mb mailbox.Mailbox) {
	r.mailbox = mb
}

// SetObjectiveStore configures objective timeout enforcement. Must be called before Start().
func (r *Reaper) SetObjectiveStore(os objective.ObjectiveStore) {
	r.objectiveStore = os
}

// SetEventBus configures the event bus for reaper lifecycle events.
func (r *Reaper) SetEventBus(bus events.Bus) {
	r.bus = bus
}

// SetLifecycle configures the lifecycle writer for coordinated terminal transitions.
func (r *Reaper) SetLifecycle(lc *agentlifecycle.Writer) {
	r.lifecycle = lc
}

// Start launches the background goroutine that periodically reaps expired agents.
func (r *Reaper) Start() {
	go r.loop()
}

// Stop signals the reaper goroutine to stop and waits for it to exit.
func (r *Reaper) Stop() {
	close(r.stopCh)
	<-r.done
}

// MaxConcurrent returns the current max concurrent agent limit.
// Returns 0 if no limit is set. Use this inside a WithLock block
// to atomically check the count against the state being modified.
func (r *Reaper) MaxConcurrent() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg.MaxConcurrent
}

// CheckSpawnAllowed checks whether a new agent can be spawned given
// the current state. Call this INSIDE a WithLock block for atomicity.
// Pass the state that's already loaded under the lock.
func (r *Reaper) CheckSpawnAllowed(st *model.State) error {
	maxConcurrent := r.MaxConcurrent()
	if maxConcurrent <= 0 {
		return nil // no limit
	}

	running := 0
	for _, a := range st.Agents {
		if a.Status == model.StatusRunning {
			running++
		}
	}

	if running >= maxConcurrent {
		return &MaxConcurrentError{Limit: maxConcurrent}
	}
	return nil
}

// Reconfigure swaps the reaper config under a write lock.
// The new config takes effect on the next reap cycle.
func (r *Reaper) Reconfigure(cfg config.ReaperConfig) {
	r.mu.Lock()
	r.cfg = cfg
	r.mu.Unlock()
}

// loop is the background goroutine that runs reap cycles at the configured interval.
func (r *Reaper) loop() {
	defer close(r.done)

	for {
		r.mu.RLock()
		interval := r.cfg.Interval.Duration
		r.mu.RUnlock()

		if interval <= 0 {
			interval = 30 * time.Second
		}

		select {
		case <-r.stopCh:
			return
		case <-time.After(interval):
			r.reap()
		}
	}
}

// isTerminal returns true if the agent status is a terminal state.
func isTerminal(s model.AgentStatus) bool {
	return s == model.StatusStopped || s == model.StatusFailed || s == model.StatusCompleted
}

// reap scans state for agents past max_age and kills them.
func (r *Reaper) reap() {
	r.mu.RLock()
	maxAge := r.cfg.MaxAge.Duration
	r.mu.RUnlock()

	ctx := context.Background()
	st, err := r.store.Load(ctx)
	if err != nil {
		r.logger.Error("failed to load state", "error", err)
		return
	}

	now := time.Now()
	for _, agent := range st.Agents {
		// Skip Queued agents — not aging yet. Queue lease reaper handles stuck claimed missions.
		if agent.Status == model.StatusQueued {
			continue
		}

		// Reconciliation sweep: clean up terminal queued agents.
		if agent.Mode == "queued" && isTerminal(agent.Status) {
			r.cleanupTerminalQueuedAgent(ctx, agent)
			continue
		}

		// Eligibility: Running agents (all modes) + Idle stream agents.
		eligible := agent.Status == model.StatusRunning ||
			(agent.Mode == "stream" && agent.Status == model.StatusIdle)
		if !eligible {
			continue
		}

		// Skip max_age check if disabled.
		if maxAge <= 0 {
			continue
		}

		if now.Sub(agent.StartTime) <= maxAge {
			continue
		}

		reason := "max_age"
		r.logger.Info("killing expired agent",
			"agent", agent.Task,
			"mode", agent.Mode,
			"status", agent.Status,
			"age", now.Sub(agent.StartTime).Round(time.Second),
			"max_age", maxAge,
		)

		if agent.Mode == "queued" {
			// Worker-executed agent — cancel via queue, mark Stopped.
			if r.queue != nil && agent.MissionID != 0 {
				r.queue.Cancel(ctx, agent.MissionID)
			}
		} else if agent.Mode == "stream" {
			if r.streamBackend == nil {
				r.logger.Warn("stream agent expired but no stream backend configured",
					"agent", agent.Task,
				)
				continue
			}
			killCtx, killCancel := context.WithTimeout(context.Background(), 10*time.Second)
			killErr := r.streamBackend.KillStreamPod(killCtx, agent.Task)
			killCancel()
			if killErr != nil && !errors.Is(killErr, runtime.ErrNotFound) {
				r.logger.Error("failed to kill stream pod",
					"agent", agent.Task,
					"error", killErr,
				)
				continue
			}
		} else {
			// Direct-spawn batch agent — kill via backend (existing path).
			killCtx, killCancel := context.WithTimeout(context.Background(), 10*time.Second)
			killErr := r.backend.Kill(killCtx, agent.Task)
			killCancel()
			if killErr != nil {
				r.logger.Error("failed to kill agent",
					"agent", agent.Task,
					"error", killErr,
				)
				continue
			}
		}

		r.transitionReapedAgent(ctx, agent.Task, "reaped: "+reason)
		r.emitReapEvent(ctx, agent.Task, reason, "info")
	}

	// Check objective timeouts in the same cycle.
	r.reapObjectiveTimeouts()

	// Backstop: clean up stream resources for Failed stream agents.
	// If session.Done() didn't fire (e.g., connection stayed open after logical failure),
	// resources leak. This sweep catches them after a grace period.
	// NOTE: Uses StartTime as proxy since AgentEntry has no failure timestamp.
	// This means agents that ran longer than failedStreamGrace before failing
	// get swept immediately — acceptable since KillStreamPod is idempotent.
	const failedStreamGrace = 5 * time.Minute
	if r.streamBackend != nil {
		for _, agent := range st.Agents {
			if agent.Mode != "stream" || agent.Status != model.StatusFailed {
				continue
			}
			if now.Sub(agent.StartTime) <= failedStreamGrace {
				continue
			}
			killCtx, killCancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := r.streamBackend.KillStreamPod(killCtx, agent.Task); err != nil && !errors.Is(err, runtime.ErrNotFound) {
				r.logger.Warn("failed stream backstop cleanup failed", "agent", agent.Task, "error", err)
			}
			killCancel()
		}
	}
}

// reapObjectiveTimeouts checks open objectives for wall-clock TTL expiration.
func (r *Reaper) reapObjectiveTimeouts() {
	if r.objectiveStore == nil {
		return
	}

	ctx := context.Background()
	objectives, err := r.objectiveStore.ListByStatus(ctx, objective.StatusOpen)
	if err != nil {
		r.logger.Error("failed to list open objectives for timeout check", "error", err)
		return
	}

	now := time.Now()
	for _, obj := range objectives {
		if obj.MaxRuntime <= 0 {
			continue
		}
		if now.Sub(obj.CreatedAt) <= obj.MaxRuntime {
			continue
		}

		r.logger.Info("timing out objective",
			"objective_id", obj.ID,
			"age", now.Sub(obj.CreatedAt).Round(time.Second),
			"max_runtime", obj.MaxRuntime,
		)

		obj.Status = objective.StatusTimedOut
		if err := r.objectiveStore.Update(ctx, obj); err != nil {
			r.logger.Error("failed to time out objective",
				"objective_id", obj.ID,
				"error", err,
			)
			continue
		}

		if r.mailbox != nil {
			r.mailbox.Send(ctx, "reaper", "chessmaster", fmt.Sprintf(
				"Objective %q timed out after %s (max_runtime=%s)",
				obj.ID, now.Sub(obj.CreatedAt).Round(time.Second), obj.MaxRuntime,
			))
		}
	}
}

// cleanupTerminalQueuedAgent removes state for a queued agent that has reached
// a terminal state (Stopped, Failed, Completed).
func (r *Reaper) cleanupTerminalQueuedAgent(ctx context.Context, agent model.AgentEntry) {
	r.logger.Info("cleaning up terminal queued agent",
		"agent", agent.Task,
		"status", agent.Status,
	)

	storeCtx, storeCancel := context.WithTimeout(ctx, 5*time.Second)
	defer storeCancel()

	if err := r.store.RemoveAgent(storeCtx, agent.Task); err != nil {
		r.logger.Error("failed to remove terminal queued agent", "agent", agent.Task, "error", err)
	}
	if r.mailbox != nil {
		if err := r.mailbox.Remove(storeCtx, agent.Task); err != nil {
			r.logger.Warn("mailbox cleanup failed for terminal queued agent", "agent", agent.Task, "error", err)
		}
	}

	r.emitReapEvent(ctx, agent.Task, "terminal_queued", "debug")
}

// emitReapEvent emits a structured event when the reaper cleans up or kills an agent.
func (r *Reaper) emitReapEvent(ctx context.Context, task, reason, severity string) {
	if r.bus == nil {
		return
	}
	e := events.Info("reconciler", "reap")
	e.Category = "agent"
	e.Resource = "task:" + task
	e.Outcome = "success"
	e.Severity = severity
	e.Task = task
	e.Detail = "agent reaped: " + reason
	e.Attrs = map[string]string{"reason": reason}
	r.bus.Emit(ctx, e)
}

// transitionReapedAgent is the single choke point for reaper-owned terminal
// transitions. Delegates to the lifecycle writer for coordinated state + event.
func (r *Reaper) transitionReapedAgent(ctx context.Context, task, reason string) {
	if r.lifecycle == nil {
		// Fallback: direct store update if writer not wired.
		storeCtx, storeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := r.store.UpdateAgentStatus(storeCtx, task, model.StatusStopped, reason); err != nil {
			r.logger.Error("failed to update state for reaped agent", "agent", task, "error", err)
		}
		storeCancel()
		return
	}

	err := r.lifecycle.MarkStopped(ctx, task, agentlifecycle.LifecycleMeta{
		Reason: reason,
	})
	if err != nil && err != agentlifecycle.ErrTransitionSkipped {
		r.logger.Error("lifecycle MarkStopped failed for reaped agent", "agent", task, "error", err)
	}
}

// MaxConcurrentError is returned by CheckSpawnAllowed when the limit is reached.
type MaxConcurrentError struct {
	Limit int
}

func (e *MaxConcurrentError) Error() string {
	return fmt.Sprintf("max concurrent agents (%d) reached", e.Limit)
} 
