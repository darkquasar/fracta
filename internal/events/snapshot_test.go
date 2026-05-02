package events

import (
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/model"
)

func TestSnapshotStore_UpdateAndGet(t *testing.T) {
	store := NewSnapshotStore(0)

	// Update creates a new snapshot if none exists.
	store.Update("agent-1", func(s *AgentSnapshot) {
		s.Status = model.StatusRunning
		s.CurrentPhase = "executing"
		s.CurrentTool = "Bash"
	})

	snap := store.Get("agent-1")
	if snap == nil {
		t.Fatal("expected snapshot, got nil")
	}
	if snap.Task != "agent-1" {
		t.Errorf("task = %q, want %q", snap.Task, "agent-1")
	}
	if snap.Status != model.StatusRunning {
		t.Errorf("status = %q, want %q", snap.Status, model.StatusRunning)
	}
	if snap.CurrentPhase != "executing" {
		t.Errorf("phase = %q, want %q", snap.CurrentPhase, "executing")
	}
	if snap.CurrentTool != "Bash" {
		t.Errorf("tool = %q, want %q", snap.CurrentTool, "Bash")
	}
}

func TestSnapshotStore_GetReturnsNilForMissing(t *testing.T) {
	store := NewSnapshotStore(0)
	if got := store.Get("nonexistent"); got != nil {
		t.Errorf("expected nil for missing task, got %+v", got)
	}
}

func TestSnapshotStore_GetReturnsCopy(t *testing.T) {
	store := NewSnapshotStore(0)
	store.Update("agent-1", func(s *AgentSnapshot) {
		s.CurrentTool = "Read"
	})

	snap := store.Get("agent-1")
	snap.CurrentTool = "mutated"

	// Original should be unchanged.
	original := store.Get("agent-1")
	if original.CurrentTool != "Read" {
		t.Errorf("store was mutated via returned copy; got %q, want %q", original.CurrentTool, "Read")
	}
}

func TestSnapshotStore_UpdateExisting(t *testing.T) {
	store := NewSnapshotStore(0)

	store.Update("agent-1", func(s *AgentSnapshot) {
		s.Status = model.StatusRunning
		s.CurrentTool = "Bash"
	})
	store.Update("agent-1", func(s *AgentSnapshot) {
		s.CurrentTool = "Edit"
	})

	snap := store.Get("agent-1")
	if snap.Status != model.StatusRunning {
		t.Errorf("status should be preserved; got %q", snap.Status)
	}
	if snap.CurrentTool != "Edit" {
		t.Errorf("tool = %q, want %q", snap.CurrentTool, "Edit")
	}
}

func TestSnapshotStore_All(t *testing.T) {
	store := NewSnapshotStore(0)

	store.Update("a", func(s *AgentSnapshot) { s.Status = model.StatusRunning })
	store.Update("b", func(s *AgentSnapshot) { s.Status = model.StatusCompleted })

	all := store.All()
	if len(all) != 2 {
		t.Fatalf("len(all) = %d, want 2", len(all))
	}
	if all["a"].Status != model.StatusRunning {
		t.Errorf("a status = %q", all["a"].Status)
	}
	if all["b"].Status != model.StatusCompleted {
		t.Errorf("b status = %q", all["b"].Status)
	}
}

func TestSnapshotStore_Remove(t *testing.T) {
	store := NewSnapshotStore(0)

	store.Update("agent-1", func(s *AgentSnapshot) { s.Status = model.StatusRunning })
	store.Remove("agent-1")

	if got := store.Get("agent-1"); got != nil {
		t.Errorf("expected nil after remove, got %+v", got)
	}
	if store.Len() != 0 {
		t.Errorf("len = %d, want 0", store.Len())
	}
}

func TestSnapshotStore_Cleanup(t *testing.T) {
	store := NewSnapshotStore(30 * time.Minute)

	now := time.Now()

	// Terminal agent expired.
	store.Update("expired", func(s *AgentSnapshot) {
		s.Status = model.StatusCompleted
		s.terminalAt = now.Add(-31 * time.Minute)
	})

	// Terminal agent NOT expired.
	store.Update("recent", func(s *AgentSnapshot) {
		s.Status = model.StatusFailed
		s.terminalAt = now.Add(-5 * time.Minute)
	})

	// Running agent (not terminal).
	store.Update("running", func(s *AgentSnapshot) {
		s.Status = model.StatusRunning
	})

	removed := store.Cleanup(now)
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if store.Get("expired") != nil {
		t.Error("expired snapshot should have been removed")
	}
	if store.Get("recent") == nil {
		t.Error("recent terminal snapshot should still exist")
	}
	if store.Get("running") == nil {
		t.Error("running snapshot should still exist")
	}
}

func TestSnapshotStore_CleanupSkipsZeroTerminalAt(t *testing.T) {
	store := NewSnapshotStore(1 * time.Minute)

	// Terminal but terminalAt not set — should not be cleaned up.
	store.Update("no-terminal-time", func(s *AgentSnapshot) {
		s.Status = model.StatusCompleted
	})

	removed := store.Cleanup(time.Now().Add(1 * time.Hour))
	if removed != 0 {
		t.Errorf("removed = %d, want 0 (terminalAt is zero)", removed)
	}
}

func TestAgentSnapshot_IsTerminal(t *testing.T) {
	tests := []struct {
		status   model.AgentStatus
		terminal bool
	}{
		{model.StatusPending, false},
		{model.StatusQueued, false},
		{model.StatusRunning, false},
		{model.StatusIdle, false},
		{model.StatusCompleted, true},
		{model.StatusFailed, true},
		{model.StatusStopped, true},
	}
	for _, tt := range tests {
		snap := &AgentSnapshot{Status: tt.status}
		if got := snap.IsTerminal(); got != tt.terminal {
			t.Errorf("IsTerminal(%q) = %v, want %v", tt.status, got, tt.terminal)
		}
	}
}
