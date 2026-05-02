package orchestrator

import (
	"context"
	"time"

	"github.com/darkquasar/fracta/internal/project"
)

const snapshotCooldown = 500 * time.Millisecond

func (o *Orchestrator) updateChessmasterStatus(status, action string) {
	_ = o.Store.UpdateChessmaster(context.Background(), status, action, time.Now())
	o.SnapshotProgress()
}

// SnapshotProgress rewrites .fracta/progress.md with the current state.
// Uses trailing-edge debounce: writes immediately on first call, then
// coalesces rapid subsequent calls and fires one trailing write after
// the cooldown period. This ensures the final update is never lost.
func (o *Orchestrator) SnapshotProgress() {
	o.snapshotMu.Lock()
	defer o.snapshotMu.Unlock()

	if o.snapshotTimer != nil {
		// Timer already pending — mark dirty so the trailing write fires.
		o.snapshotDirty = true
		return
	}

	// No timer pending — write immediately.
	o.doSnapshot()

	// Start cooldown. When it expires, fire a trailing write if dirty.
	o.snapshotTimer = time.AfterFunc(snapshotCooldown, func() {
		o.snapshotMu.Lock()
		defer o.snapshotMu.Unlock()
		o.snapshotTimer = nil
		if o.snapshotDirty {
			o.snapshotDirty = false
			o.doSnapshot()
		}
	})
}

// doSnapshot performs the actual Load + write. Must be called with snapshotMu held.
func (o *Orchestrator) doSnapshot() {
	st, err := o.Store.Load(context.Background())
	if err != nil {
		if o.Logger != nil {
			o.Logger.Warn("snapshot: failed to load state", "error", err)
		}
		return
	}
	if err := project.WriteProgressSnapshot(o.Root, st); err != nil {
		if o.Logger != nil {
			o.Logger.Warn("snapshot: failed to write progress", "error", err)
		}
	}
}
