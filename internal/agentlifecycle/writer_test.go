package agentlifecycle

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/mailbox"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/state"
)

// capturingBus records all emitted events for assertion.
type capturingBus struct {
	mu     sync.Mutex
	events []events.Event
}

func (b *capturingBus) Emit(_ context.Context, e events.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
}

func (b *capturingBus) lastEvent() events.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.events) == 0 {
		return events.Event{}
	}
	return b.events[len(b.events)-1]
}

func (b *capturingBus) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.events)
}

// memStore is a minimal in-memory state.Store for testing the writer.
type memStore struct {
	mu     sync.Mutex
	agents map[string]*model.AgentEntry
}

func newMemStore() *memStore {
	return &memStore{agents: make(map[string]*model.AgentEntry)}
}

func (s *memStore) Load(_ context.Context) (model.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var agents []model.AgentEntry
	for _, a := range s.agents {
		agents = append(agents, *a)
	}
	return model.State{Agents: agents}, nil
}

func (s *memStore) WithLock(_ context.Context, fn func(*model.State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var agents []model.AgentEntry
	for _, a := range s.agents {
		agents = append(agents, *a)
	}
	st := model.State{Agents: agents}
	if err := fn(&st); err != nil {
		return err
	}
	s.agents = make(map[string]*model.AgentEntry)
	for i := range st.Agents {
		s.agents[st.Agents[i].Task] = &st.Agents[i]
	}
	return nil
}

func (s *memStore) FindAgent(_ context.Context, task string) (*model.AgentEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[task]
	if !ok {
		return nil, nil
	}
	copy := *a
	return &copy, nil
}

func (s *memStore) RemoveAgent(_ context.Context, task string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.agents, task)
	return nil
}

func (s *memStore) UpdateAgentStatus(_ context.Context, task string, status model.AgentStatus, lastOutput string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.agents[task]; ok {
		a.Status = status
		a.LastOutput = lastOutput
	}
	return nil
}

func (s *memStore) UpdateAgentResult(_ context.Context, task string, status model.AgentStatus, lastOutput, resumeToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.agents[task]; ok {
		a.Status = status
		a.LastOutput = lastOutput
		if resumeToken != "" {
			a.ResumeToken = resumeToken
		}
	}
	return nil
}

func (s *memStore) UpdateAgentStatusIf(_ context.Context, task string, expected []model.AgentStatus, newStatus model.AgentStatus, lastOutput string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[task]
	if !ok {
		return false, nil
	}
	for _, exp := range expected {
		if a.Status == exp {
			a.Status = newStatus
			a.LastOutput = lastOutput
			return true, nil
		}
	}
	return false, nil
}

func (s *memStore) UpdateAgentResultIf(_ context.Context, task string, expected []model.AgentStatus, status model.AgentStatus, lastOutput, resumeToken string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[task]
	if !ok {
		return false, nil
	}
	for _, exp := range expected {
		if a.Status == exp {
			a.Status = status
			a.LastOutput = lastOutput
			if resumeToken != "" {
				a.ResumeToken = resumeToken
			}
			return true, nil
		}
	}
	return false, nil
}

func (s *memStore) UpdateAgentIntent(_ context.Context, task, intent string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.agents[task]; ok {
		a.CurrentIntent = intent
	}
	return nil
}

func (s *memStore) ClaimAgent(_ context.Context, task string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[task]
	if !ok {
		return state.ErrAgentNotClaimable
	}
	if a.Status != model.StatusQueued {
		return state.ErrAgentNotClaimable
	}
	a.Status = model.StatusRunning
	a.StartTime = time.Now()
	return nil
}

func (s *memStore) UpdateChessmaster(_ context.Context, _, _ string, _ time.Time) error {
	return nil
}
func (s *memStore) Mailbox() mailbox.Mailbox { return nil }
func (s *memStore) Close() error             { return nil }

func (s *memStore) get(task string) *model.AgentEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agents[task]
}

func (s *memStore) put(entry model.AgentEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[entry.Task] = &entry
}

// --- Tests ---

func TestRecordAgentStarted(t *testing.T) {
	store := newMemStore()
	bus := &capturingBus{}
	progressCalled := false

	w := New(store, bus,
		WithProgressHook(func() { progressCalled = true }),
		WithClock(func() time.Time { return time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC) }),
	)

	err := w.RecordAgentStarted(context.Background(), "agent-1", CreationMeta{
		LifecycleMeta: LifecycleMeta{RuntimeType: "claude", Backend: "local"},
		WorkspacePath: "/tmp/ws",
		BranchName:    "feature/test",
		Mode:          "batch",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	a := store.get("agent-1")
	if a == nil {
		t.Fatal("agent not inserted")
	}
	if a.Status != model.StatusRunning {
		t.Errorf("expected Running, got %s", a.Status)
	}
	if a.WorkspacePath != "/tmp/ws" {
		t.Errorf("workspace not set")
	}

	if bus.count() != 1 {
		t.Fatalf("expected 1 event, got %d", bus.count())
	}
	evt := bus.lastEvent()
	if evt.Action != "lifecycle.started" {
		t.Errorf("expected lifecycle.started, got %s", evt.Action)
	}
	if evt.Component != "agentlifecycle" {
		t.Errorf("expected component agentlifecycle, got %s", evt.Component)
	}
	if evt.Attrs["runtime"] != "claude" {
		t.Errorf("expected runtime=claude, got %s", evt.Attrs["runtime"])
	}
	if !progressCalled {
		t.Error("progress hook not called")
	}
}

func TestRecordAgentStarted_AdmissionFails(t *testing.T) {
	store := newMemStore()
	bus := &capturingBus{}

	w := New(store, bus,
		WithAdmissionCheck(func(*model.State) error {
			return &maxConcurrentErr{}
		}),
	)

	err := w.RecordAgentStarted(context.Background(), "agent-1", CreationMeta{
		LifecycleMeta: LifecycleMeta{RuntimeType: "claude"},
		Mode:          "batch",
	})
	if err == nil {
		t.Fatal("expected admission error")
	}
	if store.get("agent-1") != nil {
		t.Error("agent should not be inserted on admission failure")
	}
	if bus.count() != 0 {
		t.Error("no events should be emitted on admission failure")
	}
}

type maxConcurrentErr struct{}

func (e *maxConcurrentErr) Error() string { return "max concurrent reached" }

func TestClaimQueuedAgent(t *testing.T) {
	store := newMemStore()
	store.put(model.AgentEntry{Task: "q-agent", Status: model.StatusQueued})
	bus := &capturingBus{}

	w := New(store, bus)
	err := w.ClaimQueuedAgent(context.Background(), "q-agent", LifecycleMeta{MissionID: 42})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	a := store.get("q-agent")
	if a.Status != model.StatusRunning {
		t.Errorf("expected Running, got %s", a.Status)
	}
	if bus.count() != 1 {
		t.Fatalf("expected 1 event, got %d", bus.count())
	}
	if bus.lastEvent().Action != "lifecycle.started" {
		t.Errorf("expected lifecycle.started, got %s", bus.lastEvent().Action)
	}
}

func TestClaimQueuedAgent_NotQueued(t *testing.T) {
	store := newMemStore()
	store.put(model.AgentEntry{Task: "running-agent", Status: model.StatusRunning})
	bus := &capturingBus{}

	w := New(store, bus)
	err := w.ClaimQueuedAgent(context.Background(), "running-agent", LifecycleMeta{})
	if err != ErrTransitionSkipped {
		t.Errorf("expected ErrTransitionSkipped, got %v", err)
	}
	if bus.count() != 0 {
		t.Error("no event should be emitted when transition is skipped")
	}
}

func TestMarkCompleted(t *testing.T) {
	store := newMemStore()
	store.put(model.AgentEntry{Task: "a", Status: model.StatusRunning})
	bus := &capturingBus{}

	w := New(store, bus)
	err := w.MarkCompleted(context.Background(), "a", ResultMeta{
		LifecycleMeta: LifecycleMeta{RuntimeType: "codex"},
		LastOutput:    "done",
		ResumeToken:   "tok-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	a := store.get("a")
	if a.Status != model.StatusCompleted {
		t.Errorf("expected Completed, got %s", a.Status)
	}
	if a.LastOutput != "done" {
		t.Errorf("expected output 'done', got %s", a.LastOutput)
	}
	if a.ResumeToken != "tok-1" {
		t.Errorf("expected resume token 'tok-1', got %s", a.ResumeToken)
	}
	if bus.lastEvent().Action != "lifecycle.completed" {
		t.Errorf("expected lifecycle.completed, got %s", bus.lastEvent().Action)
	}
}

func TestMarkCompleted_AlreadyTerminal(t *testing.T) {
	store := newMemStore()
	store.put(model.AgentEntry{Task: "a", Status: model.StatusStopped})
	bus := &capturingBus{}

	w := New(store, bus)
	err := w.MarkCompleted(context.Background(), "a", ResultMeta{LastOutput: "late"})
	if err != ErrTransitionSkipped {
		t.Errorf("expected ErrTransitionSkipped, got %v", err)
	}
	if bus.count() != 0 {
		t.Error("no event when transition skipped")
	}
	if store.get("a").Status != model.StatusStopped {
		t.Error("status should not have changed")
	}
}

func TestMarkFailed(t *testing.T) {
	store := newMemStore()
	store.put(model.AgentEntry{Task: "f", Status: model.StatusRunning})
	bus := &capturingBus{}

	w := New(store, bus)
	err := w.MarkFailed(context.Background(), "f", ResultMeta{
		LifecycleMeta: LifecycleMeta{Reason: "crash"},
		LastOutput:    "error details",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.get("f").Status != model.StatusFailed {
		t.Errorf("expected Failed, got %s", store.get("f").Status)
	}
	evt := bus.lastEvent()
	if evt.Action != "lifecycle.failed" {
		t.Errorf("expected lifecycle.failed, got %s", evt.Action)
	}
	if evt.Severity != "error" {
		t.Errorf("expected severity error, got %s", evt.Severity)
	}
}

func TestMarkStopped(t *testing.T) {
	store := newMemStore()
	store.put(model.AgentEntry{Task: "s", Status: model.StatusRunning})
	bus := &capturingBus{}

	w := New(store, bus)
	err := w.MarkStopped(context.Background(), "s", LifecycleMeta{Reason: "killed by user"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.get("s").Status != model.StatusStopped {
		t.Errorf("expected Stopped, got %s", store.get("s").Status)
	}
	evt := bus.lastEvent()
	if evt.Action != "lifecycle.stopped" {
		t.Errorf("expected lifecycle.stopped, got %s", evt.Action)
	}
	if evt.Severity != "warn" {
		t.Errorf("expected severity warn, got %s", evt.Severity)
	}
}

func TestMarkRunning_NoEvent(t *testing.T) {
	store := newMemStore()
	store.put(model.AgentEntry{Task: "idle-a", Status: model.StatusIdle})
	bus := &capturingBus{}

	w := New(store, bus)
	err := w.MarkRunning(context.Background(), "idle-a", LifecycleMeta{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.get("idle-a").Status != model.StatusRunning {
		t.Errorf("expected Running, got %s", store.get("idle-a").Status)
	}
	if bus.count() != 0 {
		t.Error("MarkRunning should not emit lifecycle events")
	}
}

func TestMarkIdle_NoEvent(t *testing.T) {
	store := newMemStore()
	store.put(model.AgentEntry{Task: "run-a", Status: model.StatusRunning})
	bus := &capturingBus{}

	w := New(store, bus)
	err := w.MarkIdle(context.Background(), "run-a", "partial output", "tok", LifecycleMeta{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := store.get("run-a")
	if a.Status != model.StatusIdle {
		t.Errorf("expected Idle, got %s", a.Status)
	}
	if a.LastOutput != "partial output" {
		t.Errorf("expected output set")
	}
	if bus.count() != 0 {
		t.Error("MarkIdle should not emit lifecycle events")
	}
}
