package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/agentlifecycle"
	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/queue"
	"github.com/darkquasar/fracta/internal/runtime"
)

// captureBus records all emitted events for test assertions.
type captureBus struct {
	mu     sync.Mutex
	events []events.Event
}

func (b *captureBus) Emit(_ context.Context, e events.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
}

func (b *captureBus) all() []events.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]events.Event, len(b.events))
	copy(out, b.events)
	return out
}

func (b *captureBus) byAction(action string) []events.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []events.Event
	for _, e := range b.events {
		if e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

func TestWorker_EmitsLifecycleEvents(t *testing.T) {
	store := testStore(t)
	q := queue.NewMemoryQueue(store, 10)
	defer q.Close()

	bus := &captureBus{}

	fh := &fakeHost{
		commandSpec: host.CommandSpec{Command: "echo", Args: []string{"done"}},
		parseResult: host.Result{Output: "completed successfully"},
	}

	reg := host.NewMapRegistry("testhost")
	reg.Register("testhost", fh)

	wsBase := t.TempDir()

	// Enqueue a mission.
	ctx := context.Background()
	payload := queue.MissionPayload{Task: "agent-events", RuntimeType: "testhost", Model: "test-model"}
	payloadBytes, _ := json.Marshal(payload)
	m := &queue.Mission{AgentTask: "agent-events", Payload: payloadBytes}
	agent := &model.AgentEntry{
		Task:        "agent-events",
		RuntimeType: "testhost",
		Status:      model.StatusQueued,
		Mode:        "queued",
	}
	if err := q.Enqueue(ctx, m, agent); err != nil {
		t.Fatal(err)
	}

	mission, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatal(err)
	}

	lc := agentlifecycle.New(store, bus)
	w := New("test-worker", q, store, reg, wsBase, WithBackend(runtime.NewLocalBackend()), WithEvents(bus), WithLifecycle(lc))
	if err := w.executeMission(ctx, mission); err != nil {
		t.Fatalf("executeMission: %v", err)
	}

	got := bus.all()

	// Expect: claim, execute, complete (in that order).
	if len(got) < 3 {
		t.Fatalf("expected at least 3 events, got %d: %v", len(got), eventActions(got))
	}

	// Verify claim event (includes host_type from payload).
	claims := bus.byAction("claim")
	if len(claims) != 1 {
		t.Fatalf("expected 1 claim event, got %d", len(claims))
	}
	assertEvent(t, claims[0], "worker", "claim", "agent", "info")
	if claims[0].Attrs["runtime"] != "testhost" {
		t.Errorf("claim event runtime attr = %q, want %q", claims[0].Attrs["runtime"], "testhost")
	}

	// Verify execute event.
	executes := bus.byAction("execute")
	if len(executes) != 1 {
		t.Fatalf("expected 1 execute event, got %d", len(executes))
	}
	assertEvent(t, executes[0], "worker", "execute", "agent", "info")
	if executes[0].Attrs["model"] != "test-model" {
		t.Errorf("execute event model attr = %q, want %q", executes[0].Attrs["model"], "test-model")
	}
	if executes[0].Attrs["runtime"] != "testhost" {
		t.Errorf("execute event runtime attr = %q, want %q", executes[0].Attrs["runtime"], "testhost")
	}

	// Verify complete event.
	completes := bus.byAction("complete")
	if len(completes) != 1 {
		t.Fatalf("expected 1 complete event, got %d", len(completes))
	}
	assertEvent(t, completes[0], "worker", "complete", "agent", "info")
	if completes[0].Outcome != "success" {
		t.Errorf("complete event outcome = %q, want success", completes[0].Outcome)
	}

	// Worker-emitted events should have mission_id in attrs.
	// Host adapter events (lifecycle.started, etc.) use the MissionID struct field instead.
	for _, e := range got {
		if e.Component == "worker" && e.Attrs["mission_id"] == "" {
			t.Errorf("event %q missing mission_id attr", e.Action)
		}
	}

	// All events should have the task set.
	for _, e := range got {
		if e.Task != "agent-events" {
			t.Errorf("event %q task = %q, want agent-events", e.Action, e.Task)
		}
	}
}

func TestWorker_EmitsFailEvent(t *testing.T) {
	store := testStore(t)
	q := queue.NewMemoryQueue(store, 10)
	defer q.Close()

	bus := &captureBus{}

	// Host that returns an error result.
	fh := &fakeHost{
		commandSpec: host.CommandSpec{Command: "echo", Args: []string{"fail"}},
		parseResult: host.Result{Output: "something went wrong", IsError: true},
	}

	reg := host.NewMapRegistry("testhost")
	reg.Register("testhost", fh)

	wsBase := t.TempDir()

	ctx := context.Background()
	payload := queue.MissionPayload{Task: "agent-fail", RuntimeType: "testhost"}
	payloadBytes, _ := json.Marshal(payload)
	m := &queue.Mission{AgentTask: "agent-fail", Payload: payloadBytes}
	agent := &model.AgentEntry{
		Task:        "agent-fail",
		RuntimeType: "testhost",
		Status:      model.StatusQueued,
		Mode:        "queued",
	}
	if err := q.Enqueue(ctx, m, agent); err != nil {
		t.Fatal(err)
	}

	mission, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatal(err)
	}

	lc := agentlifecycle.New(store, bus)
	w := New("test-worker", q, store, reg, wsBase, WithBackend(runtime.NewLocalBackend()), WithEvents(bus), WithLifecycle(lc))
	// executeMission should succeed (it handles the error internally).
	if err := w.executeMission(ctx, mission); err != nil {
		t.Fatalf("executeMission: %v", err)
	}

	// Should have: claim, execute, fail. The IsError flag triggers StatusFailed,
	// and the else branch of Step 13 now emits a terminal fail event before Queue.Fail.

	got := bus.all()
	actions := eventActions(got)
	t.Logf("events: %v", actions)

	// Expect: claim, execute, fail.
	claims := bus.byAction("claim")
	if len(claims) != 1 {
		t.Fatalf("expected 1 claim event, got %d", len(claims))
	}
	executes := bus.byAction("execute")
	if len(executes) != 1 {
		t.Fatalf("expected 1 execute event, got %d", len(executes))
	}

	// Terminal fail event emitted for execution failures.
	fails := bus.byAction("fail")
	if len(fails) != 1 {
		t.Fatalf("expected 1 fail event, got %d", len(fails))
	}
	assertEvent(t, fails[0], "worker", "fail", "agent", "warn")
	if fails[0].Outcome != "failure" {
		t.Errorf("fail event outcome = %q, want failure", fails[0].Outcome)
	}

	// No complete event (because status is Failed).
	completes := bus.byAction("complete")
	if len(completes) != 0 {
		t.Errorf("expected 0 complete events for failed mission, got %d", len(completes))
	}
}

func TestWorker_EmitsFailEventOnEarlyError(t *testing.T) {
	store := testStore(t)
	q := queue.NewMemoryQueue(store, 10)
	defer q.Close()

	bus := &captureBus{}

	// Register only "claude" — mission with "unknown-host" will fail.
	reg := host.NewMapRegistry("claude")
	reg.Register("claude", &fakeHost{})

	wsBase := t.TempDir()

	ctx := context.Background()
	payload := queue.MissionPayload{Task: "agent-early-fail", RuntimeType: "unknown-host"}
	payloadBytes, _ := json.Marshal(payload)
	m := &queue.Mission{AgentTask: "agent-early-fail", Payload: payloadBytes}
	agent := &model.AgentEntry{
		Task:        "agent-early-fail",
		RuntimeType: "unknown-host",
		Status:      model.StatusQueued,
		Mode:        "queued",
	}
	if err := q.Enqueue(ctx, m, agent); err != nil {
		t.Fatal(err)
	}

	mission, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatal(err)
	}

	lc := agentlifecycle.New(store, bus)
	w := New("test-worker", q, store, reg, wsBase, WithBackend(runtime.NewLocalBackend()), WithEvents(bus), WithLifecycle(lc))
	err = w.executeMission(ctx, mission)
	if err == nil {
		t.Fatal("expected error from unknown host")
	}

	// Should emit: claim, then fail (from failMission).
	fails := bus.byAction("fail")
	if len(fails) != 1 {
		t.Fatalf("expected 1 fail event, got %d (all events: %v)", len(fails), eventActions(bus.all()))
	}

	f := fails[0]
	assertEvent(t, f, "worker", "fail", "agent", "warn")
	if f.Outcome != "failure" {
		t.Errorf("fail event outcome = %q, want failure", f.Outcome)
	}
	if f.Attrs["reason"] == "" {
		t.Error("fail event should have reason attr")
	}
	if f.Task != "agent-early-fail" {
		t.Errorf("fail event task = %q, want agent-early-fail", f.Task)
	}
}

func TestWorker_NilBusSafe(t *testing.T) {
	w := &Worker{Events: nil}
	// Should not panic.
	w.emit(context.Background(), events.Info("worker", "test"))
}

func TestWorker_EmitMissionID(t *testing.T) {
	bus := &captureBus{}
	w := &Worker{Events: bus}

	w.emit(context.Background(), events.Info("worker", "test"), func(e *events.Event) {
		e.MissionID = 42
		e.Attrs = map[string]string{"mission_id": fmt.Sprintf("%d", 42)}
	})

	got := bus.all()
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].MissionID != 42 {
		t.Errorf("MissionID = %d, want 42", got[0].MissionID)
	}
	if got[0].Attrs["mission_id"] != "42" {
		t.Errorf("attrs[mission_id] = %q, want 42", got[0].Attrs["mission_id"])
	}
}

func TestWorker_EmitHeartbeats(t *testing.T) {
	bus := &captureBus{}
	w := &Worker{Events: bus, logger: fractalog.Component("worker")}

	// Pass interval directly — no Config race.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.emitHeartbeats(ctx, "test-agent", 99, "claude", 50*time.Millisecond)

	// Wait for at least 2 heartbeats at 50ms interval.
	time.Sleep(130 * time.Millisecond)
	cancel()

	heartbeats := bus.byAction("heartbeat")
	if len(heartbeats) < 2 {
		t.Fatalf("expected at least 2 heartbeat events, got %d", len(heartbeats))
	}

	hb := heartbeats[0]
	if hb.Component != "worker" {
		t.Errorf("heartbeat component = %q, want worker", hb.Component)
	}
	if hb.Category != "agent_activity" {
		t.Errorf("heartbeat category = %q, want agent_activity", hb.Category)
	}
	if hb.Severity != "debug" {
		t.Errorf("heartbeat severity = %q, want debug", hb.Severity)
	}
	if hb.Task != "test-agent" {
		t.Errorf("heartbeat task = %q, want test-agent", hb.Task)
	}
	if hb.MissionID != 99 {
		t.Errorf("heartbeat mission_id = %d, want 99", hb.MissionID)
	}
	if hb.Attrs["runtime"] != "claude" {
		t.Errorf("heartbeat runtime = %q, want claude", hb.Attrs["runtime"])
	}
	if hb.Attrs["phase"] != "executing" {
		t.Errorf("heartbeat phase = %q, want executing", hb.Attrs["phase"])
	}
	if hb.Attrs["uptime_s"] == "" {
		t.Error("heartbeat uptime_s should not be empty")
	}
}

func TestWorker_EmitHeartbeats_StopsOnCancel(t *testing.T) {
	bus := &captureBus{}
	w := &Worker{
		Events: bus,
		logger: fractalog.Component("worker"),
	}

	ctx, cancel := context.WithCancel(context.Background())
	go w.emitHeartbeats(ctx, "test-cancel", 1, "claude", 20*time.Millisecond)

	time.Sleep(50 * time.Millisecond)
	cancel()
	time.Sleep(50 * time.Millisecond)

	count := len(bus.byAction("heartbeat"))
	// After cancel, no more should arrive.
	time.Sleep(50 * time.Millisecond)
	countAfter := len(bus.byAction("heartbeat"))

	if countAfter != count {
		t.Errorf("heartbeats continued after cancel: before=%d after=%d", count, countAfter)
	}
}

// --- helpers ---

func assertEvent(t *testing.T, e events.Event, component, action, category, severity string) {
	t.Helper()
	if e.Component != component {
		t.Errorf("event component = %q, want %q", e.Component, component)
	}
	if e.Action != action {
		t.Errorf("event action = %q, want %q", e.Action, action)
	}
	if e.Category != category {
		t.Errorf("event category = %q, want %q", e.Category, category)
	}
	if e.Severity != severity {
		t.Errorf("event severity = %q, want %q", e.Severity, severity)
	}
	if e.ID == "" {
		t.Error("event ID should not be empty")
	}
}

func eventActions(evts []events.Event) []string {
	out := make([]string, len(evts))
	for i, e := range evts {
		out[i] = e.Action
	}
	return out
}
