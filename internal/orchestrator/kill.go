package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/runtime"
	"github.com/darkquasar/fracta/internal/workspace"
)

func (o *Orchestrator) Kill(task string, keepFiles bool) error {
	ctx := context.Background()
	agent, err := o.Store.FindAgent(ctx, task)
	if err != nil {
		return fmt.Errorf("looking up agent: %w", err)
	}
	if agent == nil {
		return fmt.Errorf("agent %q not found", task)
	}

	meta := TerminalMeta{RuntimeType: agent.RuntimeType}

	switch agent.Status {
	case model.StatusQueued:
		// Agent waiting in queue — cancel the mission, mark agent Stopped.
		if o.Queue != nil && agent.MissionID != 0 {
			o.Queue.Cancel(ctx, agent.MissionID)
		}
		meta.Reason = "killed while queued"
		o.transitionAgentToTerminal(ctx, task, model.StatusStopped, meta)

	case model.StatusRunning:
		if agent.Mode == "queued" {
			// Agent running on a worker — cancel the mission.
			if o.Queue != nil && agent.MissionID != 0 {
				o.Queue.Cancel(ctx, agent.MissionID)
			}
			meta.Reason = "killed while running on worker"
			o.transitionAgentToTerminal(ctx, task, model.StatusStopped, meta)
		} else {
			// Direct-spawn agent — kill batch job and stream pod (exactly one exists).
			if o.Backend != nil {
				killCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				err := o.Backend.Kill(killCtx, task)
				cancel()
				if err != nil {
					if errors.Is(err, runtime.ErrNotFound) {
						o.Logger.Info("process already exited", "task", task)
					} else {
						return fmt.Errorf("kill: stopping agent process (state preserved): %w", err)
					}
				}

				// Also kill the stream pod if the backend supports it.
				// For batch agents the pod name won't exist (ErrNotFound is fine).
				// For stream agents the job name won't exist (already handled above).
				if sb, ok := o.Backend.(runtime.StreamBackend); ok {
					killCtx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
					if podErr := sb.KillStreamPod(killCtx2, task); podErr != nil && !errors.Is(podErr, runtime.ErrNotFound) {
						o.Logger.Warn("kill: stream pod removal failed", "task", task, "error", podErr)
					}
					cancel2()
				}
			}
			// Transition BEFORE cleanup so snapshot is updated, not recreated.
			meta.Reason = "killed"
			o.transitionAgentToTerminal(ctx, task, model.StatusStopped, meta)
			o.cleanupAgent(ctx, agent, keepFiles)
		}

	case model.StatusIdle:
		// Idle stream agent — kill the stream pod, transition, cleanup.
		if agent.Mode == "stream" {
			o.cleanupStreamPod(task)
		}
		meta.Reason = "killed while idle"
		o.transitionAgentToTerminal(ctx, task, model.StatusStopped, meta)
		o.cleanupAgent(ctx, agent, keepFiles)

	default:
		// Already terminal — retry K8s resource cleanup (may have been missed by prior path).
		if agent.Mode == "stream" {
			o.cleanupStreamPod(task)
		}
		meta.Reason = "killed"
		o.transitionAgentToTerminal(ctx, task, agent.Status, meta)
		o.cleanupAgent(ctx, agent, keepFiles)
	}

	o.updateChessmasterStatus("Agent removed", fmt.Sprintf("Killed %s", task))

	return nil
}

// cleanupAgent removes agent state, mailbox, workspace, and log file.
func (o *Orchestrator) cleanupAgent(ctx context.Context, agent *model.AgentEntry, keepFiles bool) {
	wsInfo := &workspace.Info{
		Path:       agent.WorkspacePath,
		BranchName: agent.BranchName,
		BaseBranch: agent.BaseBranch,
	}

	if err := o.Store.RemoveAgent(ctx, agent.Task); err != nil {
		o.Logger.Warn("agent state removal failed", "task", agent.Task, "error", err)
	}

	if err := o.Mailbox.Remove(ctx, agent.Task); err != nil {
		o.Logger.Warn("mailbox cleanup failed", "task", agent.Task, "error", err)
	}

	if err := o.Workspace.Remove(wsInfo, keepFiles); err != nil {
		o.Logger.Warn("workspace cleanup failed", "task", agent.Task, "error", err)
	}

	logFile := filepath.Join(o.Root, model.FractaDir, model.LogsDir, agent.Task+".log")
	os.Remove(logFile)
}
