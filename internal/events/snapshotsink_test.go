package events

import (
	"context"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/model"
)

func newTestSink() (*SnapshotSink, *SnapshotStore) {
	store := NewSnapshotStore(0)
	sink := NewSnapshotSink(store, 15*time.Second)
	return sink, store
}

func TestSnapshotSink_Heartbeat(t *testing.T) {
	sink, store := newTestSink()
	now := time.Now()

	err := sink.Handle(context.Background(), Event{
		Task:      "agent-1",
		Time:      now,
		Component: "worker",
		Action:    "heartbeat",
		Category:  "agent_activity",
		Attrs:     map[string]string{"phase": "executing", "tool": "Bash", "uptime_s": "30"},
	})
	if err != nil {
		t.Fatal(err)
	}

	snap := store.Get("agent-1")
	if snap == nil {
		t.Fatal("expected snapshot")
	}
	if snap.LastHeartbeatAt != now {
		t.Errorf("LastHeartbeatAt = %v, want %v", snap.LastHeartbeatAt, now)
	}
	if snap.CurrentPhase != "executing" {
		t.Errorf("CurrentPhase = %q, want %q", snap.CurrentPhase, "executing")
	}
	if snap.CurrentTool != "Bash" {
		t.Errorf("CurrentTool = %q, want %q", snap.CurrentTool, "Bash")
	}
}

func TestSnapshotSink_HeartbeatDoesNotClearTool(t *testing.T) {
	sink, store := newTestSink()

	// First heartbeat sets tool.
	sink.Handle(context.Background(), Event{
		Task:   "agent-1",
		Time:   time.Now(),
		Action: "heartbeat",
		Attrs:  map[string]string{"phase": "executing", "tool": "Bash"},
	})
	// Second heartbeat without tool should NOT clear it — adapter events
	// (tool.completed) are authoritative for clearing tool state.
	sink.Handle(context.Background(), Event{
		Task:   "agent-1",
		Time:   time.Now(),
		Action: "heartbeat",
		Attrs:  map[string]string{"phase": "idle"},
	})

	snap := store.Get("agent-1")
	if snap.CurrentTool != "Bash" {
		t.Errorf("CurrentTool = %q, want %q (heartbeat should not clear tool)", snap.CurrentTool, "Bash")
	}

	// tool.completed DOES clear the tool.
	sink.Handle(context.Background(), Event{
		Task:   "agent-1",
		Time:   time.Now(),
		Action: "tool.completed",
		Attrs:  map[string]string{"tool_name": "Bash"},
	})
	snap = store.Get("agent-1")
	if snap.CurrentTool != "" {
		t.Errorf("CurrentTool = %q, want empty after tool.completed", snap.CurrentTool)
	}
}

func TestSnapshotSink_ToolStartedCompleted(t *testing.T) {
	sink, store := newTestSink()

	sink.Handle(context.Background(), Event{
		Task:   "agent-1",
		Time:   time.Now(),
		Action: "tool.started",
		Attrs:  map[string]string{"tool_name": "Edit"},
	})

	snap := store.Get("agent-1")
	if snap.CurrentTool != "Edit" {
		t.Errorf("CurrentTool = %q, want %q", snap.CurrentTool, "Edit")
	}
	if snap.CurrentPhase != "tool_use" {
		t.Errorf("CurrentPhase = %q, want %q", snap.CurrentPhase, "tool_use")
	}

	sink.Handle(context.Background(), Event{
		Task:   "agent-1",
		Time:   time.Now(),
		Action: "tool.completed",
		Attrs:  map[string]string{"tool_name": "Edit"},
	})

	snap = store.Get("agent-1")
	if snap.CurrentTool != "" {
		t.Errorf("CurrentTool = %q, want empty after completion", snap.CurrentTool)
	}
	if snap.CurrentPhase != "executing" {
		t.Errorf("CurrentPhase = %q, want %q", snap.CurrentPhase, "executing")
	}
}

func TestSnapshotSink_LifecycleStarted(t *testing.T) {
	sink, store := newTestSink()
	now := time.Now()

	sink.Handle(context.Background(), Event{
		Task:   "agent-1",
		Time:   now,
		Action: "lifecycle.started",
		Attrs:  map[string]string{"runtime": "claude", "backend": "kubernetes", "pod_name": "fracta-agent-xyz"},
	})

	snap := store.Get("agent-1")
	if snap.Status != model.StatusRunning {
		t.Errorf("Status = %q, want %q", snap.Status, model.StatusRunning)
	}
	if snap.CurrentPhase != "executing" {
		t.Errorf("CurrentPhase = %q, want %q", snap.CurrentPhase, "executing")
	}
	if snap.StartedAt != now {
		t.Errorf("StartedAt = %v, want %v", snap.StartedAt, now)
	}
	if snap.Backend != "kubernetes" {
		t.Errorf("Backend = %q, want %q", snap.Backend, "kubernetes")
	}
	if snap.PodName != "fracta-agent-xyz" {
		t.Errorf("PodName = %q, want %q", snap.PodName, "fracta-agent-xyz")
	}
}

func TestSnapshotSink_LifecycleCompleted(t *testing.T) {
	sink, store := newTestSink()
	now := time.Now()

	// Start first.
	sink.Handle(context.Background(), Event{
		Task: "agent-1", Time: now, Action: "lifecycle.started",
	})
	// Complete.
	sink.Handle(context.Background(), Event{
		Task: "agent-1", Time: now.Add(time.Minute), Action: "lifecycle.completed",
	})

	snap := store.Get("agent-1")
	if snap.Status != model.StatusCompleted {
		t.Errorf("Status = %q, want %q", snap.Status, model.StatusCompleted)
	}
	if snap.CurrentPhase != "done" {
		t.Errorf("CurrentPhase = %q, want %q", snap.CurrentPhase, "done")
	}
	if snap.terminalAt != now.Add(time.Minute) {
		t.Errorf("terminalAt = %v, want %v", snap.terminalAt, now.Add(time.Minute))
	}
}

func TestSnapshotSink_LifecycleFailed(t *testing.T) {
	sink, store := newTestSink()
	now := time.Now()

	sink.Handle(context.Background(), Event{
		Task: "agent-1", Time: now, Action: "lifecycle.started",
	})
	sink.Handle(context.Background(), Event{
		Task: "agent-1", Time: now, Action: "lifecycle.failed",
		Attrs: map[string]string{"error": "out of memory"},
	})

	snap := store.Get("agent-1")
	if snap.Status != model.StatusFailed {
		t.Errorf("Status = %q, want %q", snap.Status, model.StatusFailed)
	}
}

func TestSnapshotSink_MessageExcerpt(t *testing.T) {
	sink, store := newTestSink()

	sink.Handle(context.Background(), Event{
		Task:   "agent-1",
		Time:   time.Now(),
		Action: "message.completed",
		Attrs:  map[string]string{"text_preview": "Hello, I've completed the analysis."},
	})

	snap := store.Get("agent-1")
	if snap.LastMessageExcerpt != "Hello, I've completed the analysis." {
		t.Errorf("LastMessageExcerpt = %q", snap.LastMessageExcerpt)
	}
}

func TestSnapshotSink_MessageExcerptTruncated(t *testing.T) {
	sink, store := newTestSink()

	longText := ""
	for i := 0; i < 300; i++ {
		longText += "x"
	}

	sink.Handle(context.Background(), Event{
		Task:   "agent-1",
		Time:   time.Now(),
		Action: "message.delta",
		Attrs:  map[string]string{"text_preview": longText},
	})

	snap := store.Get("agent-1")
	if len(snap.LastMessageExcerpt) != 256 {
		t.Errorf("LastMessageExcerpt length = %d, want 256", len(snap.LastMessageExcerpt))
	}
}

func TestSnapshotSink_MessageFromDetail(t *testing.T) {
	sink, store := newTestSink()

	sink.Handle(context.Background(), Event{
		Task:   "agent-1",
		Time:   time.Now(),
		Action: "message.completed",
		Detail: "Fallback detail text",
	})

	snap := store.Get("agent-1")
	if snap.LastMessageExcerpt != "Fallback detail text" {
		t.Errorf("LastMessageExcerpt = %q, want %q", snap.LastMessageExcerpt, "Fallback detail text")
	}
}

func TestSnapshotSink_IgnoresEmptyTask(t *testing.T) {
	sink, store := newTestSink()

	err := sink.Handle(context.Background(), Event{
		Time:   time.Now(),
		Action: "heartbeat",
		Attrs:  map[string]string{"phase": "executing"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.Len() != 0 {
		t.Errorf("store should be empty for events without task")
	}
}

func TestSnapshotSink_CheckUnresponsive(t *testing.T) {
	sink, store := newTestSink() // 15s interval → 45s threshold

	now := time.Now()

	// Agent received heartbeat 50s ago.
	store.Update("agent-1", func(s *AgentSnapshot) {
		s.Status = model.StatusRunning
		s.LastHeartbeatAt = now.Add(-50 * time.Second)
		s.CurrentPhase = "executing"
	})

	marked := sink.CheckUnresponsive("agent-1", now)
	if !marked {
		t.Error("expected agent to be marked unresponsive")
	}

	snap := store.Get("agent-1")
	if snap.CurrentPhase != "unresponsive" {
		t.Errorf("CurrentPhase = %q, want %q", snap.CurrentPhase, "unresponsive")
	}
}

func TestSnapshotSink_CheckUnresponsiveNotTriggered(t *testing.T) {
	sink, store := newTestSink()

	now := time.Now()

	// Agent received heartbeat 10s ago (within 45s threshold).
	store.Update("agent-1", func(s *AgentSnapshot) {
		s.Status = model.StatusRunning
		s.LastHeartbeatAt = now.Add(-10 * time.Second)
		s.CurrentPhase = "executing"
	})

	marked := sink.CheckUnresponsive("agent-1", now)
	if marked {
		t.Error("expected agent NOT to be marked unresponsive")
	}

	snap := store.Get("agent-1")
	if snap.CurrentPhase != "executing" {
		t.Errorf("CurrentPhase = %q, want %q", snap.CurrentPhase, "executing")
	}
}

func TestSnapshotSink_CheckUnresponsiveSkipsTerminal(t *testing.T) {
	sink, store := newTestSink()

	now := time.Now()

	store.Update("agent-1", func(s *AgentSnapshot) {
		s.Status = model.StatusCompleted
		s.LastHeartbeatAt = now.Add(-5 * time.Minute)
		s.CurrentPhase = "done"
	})

	marked := sink.CheckUnresponsive("agent-1", now)
	if marked {
		t.Error("terminal agents should not be marked unresponsive")
	}
}

func TestSnapshotSink_CheckAllUnresponsive(t *testing.T) {
	sink, store := newTestSink()

	now := time.Now()

	store.Update("stale", func(s *AgentSnapshot) {
		s.Status = model.StatusRunning
		s.LastHeartbeatAt = now.Add(-50 * time.Second)
		s.CurrentPhase = "executing"
	})
	store.Update("fresh", func(s *AgentSnapshot) {
		s.Status = model.StatusRunning
		s.LastHeartbeatAt = now.Add(-5 * time.Second)
		s.CurrentPhase = "executing"
	})
	store.Update("done", func(s *AgentSnapshot) {
		s.Status = model.StatusCompleted
		s.LastHeartbeatAt = now.Add(-5 * time.Minute)
		s.CurrentPhase = "done"
	})

	count := sink.CheckAllUnresponsive(now)
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}

	if snap := store.Get("stale"); snap.CurrentPhase != "unresponsive" {
		t.Errorf("stale phase = %q", snap.CurrentPhase)
	}
	if snap := store.Get("fresh"); snap.CurrentPhase != "executing" {
		t.Errorf("fresh phase = %q", snap.CurrentPhase)
	}
}

func TestSnapshotSink_TurnEvents(t *testing.T) {
	sink, store := newTestSink()

	sink.Handle(context.Background(), Event{
		Task: "agent-1", Time: time.Now(), Action: "turn.started",
	})
	snap := store.Get("agent-1")
	if snap.CurrentPhase != "thinking" {
		t.Errorf("phase = %q, want %q", snap.CurrentPhase, "thinking")
	}

	sink.Handle(context.Background(), Event{
		Task: "agent-1", Time: time.Now(), Action: "turn.completed",
	})
	snap = store.Get("agent-1")
	if snap.CurrentPhase != "executing" {
		t.Errorf("phase = %q, want %q", snap.CurrentPhase, "executing")
	}
}

func TestSnapshotSink_CorrelationFields(t *testing.T) {
	sink, store := newTestSink()

	sink.Handle(context.Background(), Event{
		Task:        "agent-1",
		Time:        time.Now(),
		Action:      "heartbeat",
		MissionID:   42,
		ObjectiveID: "obj-abc",
		Attrs:       map[string]string{"phase": "executing"},
	})

	snap := store.Get("agent-1")
	if snap.MissionID != 42 {
		t.Errorf("MissionID = %d, want 42", snap.MissionID)
	}
	if snap.ObjectiveID != "obj-abc" {
		t.Errorf("ObjectiveID = %q, want %q", snap.ObjectiveID, "obj-abc")
	}
}
