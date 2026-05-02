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

// --- Integration test helpers ---

type eventCapture struct {
	mu     sync.Mutex
	events []events.Event
}

func (c *eventCapture) Emit(_ context.Context, e events.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *eventCapture) byAction(action string) []events.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []events.Event
	for _, e := range c.events {
		if e.Action == action {
			result = append(result, e)
		}
	}
	return result
}

func (c *eventCapture) all() []events.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]events.Event, len(c.events))
	copy(cp, c.events)
	return cp
}

// intStore is a thread-safe in-memory store for integration tests.
type intStore struct {
	mu     sync.Mutex
	agents map[string]*model.AgentEntry
}

func newIntStore() *intStore {
	return &intStore{agents: make(map[string]*model.AgentEntry)}
}

func (s *intStore) Load(_ context.Context) (model.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var agents []model.AgentEntry
	for _, a := range s.agents {
		agents = append(agents, *a)
	}
	return model.State{Agents: agents}, nil
}

func (s *intStore) WithLock(_ context.Context, fn func(*model.State) error) error {
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

func (s *intStore) FindAgent(_ context.Context, task string) (*model.AgentEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[task]
	if !ok {
		return nil, nil
	}
	cp := *a
	return &cp, nil
}

func (s *intStore) RemoveAgent(_ context.Context, task string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.agents, task)
	return nil
}

func (s *intStore) UpdateAgentStatus(_ context.Context, task string, status model.AgentStatus, lastOutput string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.agents[task]; ok {
		a.Status = status
		a.LastOutput = lastOutput
	}
	return nil
}

func (s *intStore) UpdateAgentResult(_ context.Context, task string, status model.AgentStatus, lastOutput, resumeToken string) error {
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

func (s *intStore) UpdateAgentStatusIf(_ context.Context, task string, expected []model.AgentStatus, newStatus model.AgentStatus, lastOutput string) (bool, error) {
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

func (s *intStore) UpdateAgentResultIf(_ context.Context, task string, expected []model.AgentStatus, status model.AgentStatus, lastOutput, resumeToken string) (bool, error) {
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

func (s *intStore) UpdateAgentIntent(_ context.Context, task, intent string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.agents[task]; ok {
		a.CurrentIntent = intent
	}
	return nil
}

func (s *intStore) ClaimAgent(_ context.Context, task string) error {
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

func (s *intStore) UpdateChessmaster(_ context.Context, _, _ string, _ time.Time) error { return nil }
func (s *intStore) Mailbox() mailbox.Mailbox                                            { return nil }
func (s *intStore) Close() error                                                        { return nil }

func (s *intStore) put(entry model.AgentEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[entry.Task] = &entry
}

func (s *intStore) get(task string) *model.AgentEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.agents[task]
	if a == nil {
		return nil
	}
	cp := *a
	return &cp
}

// --- Integration Tests ---

func TestIntegration_QueuedClaim_EmitsExactlyOneStarted(t *testing.T) {
	store := newIntStore()
	store.put(model.AgentEntry{Task: "q-agent", Status: model.StatusQueued})
	bus := &eventCapture{}

	w := New(store, bus)

	err := w.ClaimQueuedAgent(context.Background(), "q-agent", LifecycleMeta{
		RuntimeType: "claude",
		MissionID:   1,
	})
	if err != nil {
		t.Fatalf("ClaimQueuedAgent: %v", err)
	}

	started := bus.byAction("lifecycle.started")
	if len(started) != 1 {
		t.Fatalf("expected exactly 1 lifecycle.started, got %d", len(started))
	}
	if started[0].Attrs["runtime"] != "claude" {
		t.Errorf("expected runtime=claude, got %s", started[0].Attrs["runtime"])
	}
	if started[0].Component != "agentlifecycle" {
		t.Errorf("expected component=agentlifecycle, got %s", started[0].Component)
	}

	a := store.get("q-agent")
	if a.Status != model.StatusRunning {
		t.Errorf("expected Running, got %s", a.Status)
	}
}

func TestIntegration_ConcurrentKillAndCompletion_ExactlyOneTerminal(t *testing.T) {
	store := newIntStore()
	store.put(model.AgentEntry{Task: "race-agent", Status: model.StatusRunning})
	bus := &eventCapture{}

	w := New(store, bus)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		w.MarkStopped(context.Background(), "race-agent", LifecycleMeta{Reason: "killed"})
	}()

	go func() {
		defer wg.Done()
		w.MarkCompleted(context.Background(), "race-agent", ResultMeta{
			LastOutput: "done",
		})
	}()

	wg.Wait()

	// Exactly one terminal event should have been emitted.
	allEvents := bus.all()
	terminalCount := 0
	for _, e := range allEvents {
		if e.Action == "lifecycle.completed" || e.Action == "lifecycle.failed" || e.Action == "lifecycle.stopped" {
			terminalCount++
		}
	}
	if terminalCount != 1 {
		t.Fatalf("expected exactly 1 terminal event, got %d (events: %v)", terminalCount, eventActions(allEvents))
	}

	// Agent should be in exactly one terminal state.
	a := store.get("race-agent")
	if a.Status != model.StatusStopped && a.Status != model.StatusCompleted {
		t.Errorf("expected terminal status, got %s", a.Status)
	}
}

func TestIntegration_ReaperTTL_EmitsLifecycleStopped(t *testing.T) {
	store := newIntStore()
	store.put(model.AgentEntry{Task: "ttl-agent", Status: model.StatusRunning})
	bus := &eventCapture{}

	w := New(store, bus)

	err := w.MarkStopped(context.Background(), "ttl-agent", LifecycleMeta{
		Reason: "reaped: TTL exceeded",
	})
	if err != nil {
		t.Fatalf("MarkStopped: %v", err)
	}

	stopped := bus.byAction("lifecycle.stopped")
	if len(stopped) != 1 {
		t.Fatalf("expected exactly 1 lifecycle.stopped, got %d", len(stopped))
	}
	if stopped[0].Severity != "warn" {
		t.Errorf("expected severity=warn, got %s", stopped[0].Severity)
	}
	if stopped[0].Detail != "reaped: TTL exceeded" {
		t.Errorf("expected detail about TTL, got %s", stopped[0].Detail)
	}

	// Must NOT emit lifecycle.completed.
	completed := bus.byAction("lifecycle.completed")
	if len(completed) != 0 {
		t.Errorf("expected 0 lifecycle.completed, got %d", len(completed))
	}

	a := store.get("ttl-agent")
	if a.Status != model.StatusStopped {
		t.Errorf("expected Stopped, got %s", a.Status)
	}
}

func TestIntegration_StreamFatalError_MarksFailed_NoDuplicate(t *testing.T) {
	store := newIntStore()
	bus := &eventCapture{}

	w := New(store, bus,
		WithAdmissionCheck(func(*model.State) error { return nil }),
	)

	// Step 1: Create stream agent via RecordAgentStarted.
	err := w.RecordAgentStarted(context.Background(), "stream-agent", CreationMeta{
		LifecycleMeta: LifecycleMeta{RuntimeType: "claude", Backend: "local"},
		WorkspacePath: "/tmp/ws",
		BranchName:    "feature/x",
		Mode:          "stream",
	})
	if err != nil {
		t.Fatalf("RecordAgentStarted: %v", err)
	}

	started := bus.byAction("lifecycle.started")
	if len(started) != 1 {
		t.Fatalf("expected 1 lifecycle.started after spawn, got %d", len(started))
	}

	// Step 2: Simulate stream fatal error → MarkFailed.
	err = w.MarkFailed(context.Background(), "stream-agent", ResultMeta{
		LifecycleMeta: LifecycleMeta{Reason: "connection lost"},
		LastOutput:    "connection lost",
	})
	if err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	// Step 3: Second MarkFailed (e.g., from observer firing again) should be skipped.
	err = w.MarkFailed(context.Background(), "stream-agent", ResultMeta{
		LifecycleMeta: LifecycleMeta{Reason: "duplicate"},
		LastOutput:    "duplicate",
	})
	if err != ErrTransitionSkipped {
		t.Errorf("expected ErrTransitionSkipped on duplicate MarkFailed, got %v", err)
	}

	// Verify: exactly 1 lifecycle.started + exactly 1 lifecycle.failed.
	allEvents := bus.all()
	startedCount := 0
	failedCount := 0
	for _, e := range allEvents {
		switch e.Action {
		case "lifecycle.started":
			startedCount++
		case "lifecycle.failed":
			failedCount++
		}
	}
	if startedCount != 1 {
		t.Errorf("expected 1 lifecycle.started, got %d", startedCount)
	}
	if failedCount != 1 {
		t.Errorf("expected 1 lifecycle.failed, got %d", failedCount)
	}

	a := store.get("stream-agent")
	if a.Status != model.StatusFailed {
		t.Errorf("expected Failed, got %s", a.Status)
	}
}

func TestIntegration_FullLifecycle_Queued_Running_Completed(t *testing.T) {
	store := newIntStore()
	store.put(model.AgentEntry{Task: "full-lc", Status: model.StatusQueued})
	bus := &eventCapture{}

	w := New(store, bus)

	// Claim → Running.
	if err := w.ClaimQueuedAgent(context.Background(), "full-lc", LifecycleMeta{MissionID: 10}); err != nil {
		t.Fatalf("ClaimQueuedAgent: %v", err)
	}

	// Complete.
	if err := w.MarkCompleted(context.Background(), "full-lc", ResultMeta{
		LastOutput:  "all done",
		ResumeToken: "tok-final",
	}); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}

	// Verify event sequence.
	allEvents := bus.all()
	if len(allEvents) != 2 {
		t.Fatalf("expected 2 events, got %d: %v", len(allEvents), eventActions(allEvents))
	}
	if allEvents[0].Action != "lifecycle.started" {
		t.Errorf("event[0]: expected lifecycle.started, got %s", allEvents[0].Action)
	}
	if allEvents[1].Action != "lifecycle.completed" {
		t.Errorf("event[1]: expected lifecycle.completed, got %s", allEvents[1].Action)
	}

	a := store.get("full-lc")
	if a.Status != model.StatusCompleted {
		t.Errorf("expected Completed, got %s", a.Status)
	}
	if a.LastOutput != "all done" {
		t.Errorf("expected output 'all done', got %s", a.LastOutput)
	}
	if a.ResumeToken != "tok-final" {
		t.Errorf("expected token 'tok-final', got %s", a.ResumeToken)
	}
}

func eventActions(evts []events.Event) []string {
	actions := make([]string, len(evts))
	for i, e := range evts {
		actions[i] = e.Action
	}
	return actions
}
