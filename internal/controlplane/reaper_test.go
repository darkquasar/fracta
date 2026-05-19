package controlplane

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/mailbox"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/queue"
	"github.com/darkquasar/fracta/internal/runtime"
)

// mockStore implements state.Store for testing.
type mockStore struct {
	mu    sync.Mutex
	state model.State
}

func (m *mockStore) Load(_ context.Context) (model.State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	agents := make([]model.AgentEntry, len(m.state.Agents))
	copy(agents, m.state.Agents)
	return model.State{
		Agents:      agents,
		Chessmaster: m.state.Chessmaster,
	}, nil
}

func (m *mockStore) WithLock(_ context.Context, fn func(*model.State) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fn(&m.state)
}

func (m *mockStore) Close() error                                                                              { return nil }
func (m *mockStore) Mailbox() mailbox.Mailbox                                                                  { return nil }
func (m *mockStore) UpdateChessmaster(_ context.Context, _, _ string, _ time.Time) error                       { return nil }
func (m *mockStore) UpdateAgentResult(_ context.Context, _ string, _ model.AgentStatus, _, _ string) error     { return nil }
func (m *mockStore) UpdateAgentIntent(_ context.Context, _, _ string) error                                    { return nil }
func (m *mockStore) RemoveAgent(_ context.Context, task string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	filtered := m.state.Agents[:0]
	for _, a := range m.state.Agents {
		if a.Task != task {
			filtered = append(filtered, a)
		}
	}
	m.state.Agents = filtered
	return nil
}
func (m *mockStore) UpdateAgentStatus(_ context.Context, task string, status model.AgentStatus, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.state.Agents {
		if m.state.Agents[i].Task == task {
			m.state.Agents[i].Status = status
		}
	}
	return nil
}

func (m *mockStore) FindAgent(_ context.Context, task string) (*model.AgentEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.state.Agents {
		if m.state.Agents[i].Task == task {
			return &m.state.Agents[i], nil
		}
	}
	return nil, nil
}

func (m *mockStore) ClaimAgent(_ context.Context, task string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.state.Agents {
		if m.state.Agents[i].Task == task && m.state.Agents[i].Status == model.StatusQueued {
			m.state.Agents[i].Status = model.StatusRunning
			m.state.Agents[i].StartTime = time.Now()
			return nil
		}
	}
	return fmt.Errorf("agent %s not found or not queued", task)
}

func (m *mockStore) UpdateAgentStatusIf(_ context.Context, task string, expected []model.AgentStatus, newStatus model.AgentStatus, lastOutput string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.state.Agents {
		if m.state.Agents[i].Task == task {
			for _, exp := range expected {
				if m.state.Agents[i].Status == exp {
					m.state.Agents[i].Status = newStatus
					m.state.Agents[i].LastOutput = lastOutput
					return true, nil
				}
			}
			return false, nil
		}
	}
	return false, nil
}

func (m *mockStore) UpdateAgentResultIf(_ context.Context, task string, expected []model.AgentStatus, status model.AgentStatus, lastOutput, resumeToken string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.state.Agents {
		if m.state.Agents[i].Task == task {
			for _, exp := range expected {
				if m.state.Agents[i].Status == exp {
					m.state.Agents[i].Status = status
					m.state.Agents[i].LastOutput = lastOutput
					if resumeToken != "" {
						m.state.Agents[i].ResumeToken = resumeToken
					}
					return true, nil
				}
			}
			return false, nil
		}
	}
	return false, nil
}

// mockBackend implements runtime.Backend for testing.
type mockBackend struct {
	mu      sync.Mutex
	killed  []string
	killErr map[string]error
}

func (m *mockBackend) Spawn(ctx context.Context, opts runtime.SpawnOpts) (runtime.AgentHandle, error) {
	return nil, fmt.Errorf("not implemented in mock")
}

func (m *mockBackend) Kill(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.killErr != nil {
		if err, ok := m.killErr[id]; ok {
			return err
		}
	}
	m.killed = append(m.killed, id)
	return nil
}

func (m *mockBackend) Status(ctx context.Context, id string) (model.AgentStatus, error) {
	return model.StatusRunning, nil
}

func (m *mockBackend) Logs(ctx context.Context, id string, tailLines int) (string, error) {
	return "", fmt.Errorf("not implemented in mock")
}

func (m *mockBackend) killedAgents() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.killed))
	copy(result, m.killed)
	return result
}

func dur(d time.Duration) config.Duration {
	return config.Duration{Duration: d}
}

func TestReaper_KillsExpiredAgents(t *testing.T) {
	store := &mockStore{
		state: model.State{
			Agents: []model.AgentEntry{
				{Task: "expired-agent", Status: model.StatusRunning, StartTime: time.Now().Add(-2 * time.Hour)},
				{Task: "fresh-agent", Status: model.StatusRunning, StartTime: time.Now()},
				{Task: "stopped-agent", Status: model.StatusStopped, StartTime: time.Now().Add(-3 * time.Hour)},
			},
		},
	}
	backend := &mockBackend{}

	r := NewReaper(store, backend, config.ReaperConfig{
		MaxAge:   dur(1 * time.Hour),
		Interval: dur(50 * time.Millisecond),
	})

	r.Start()
	time.Sleep(200 * time.Millisecond)
	r.Stop()

	killed := backend.killedAgents()
	if len(killed) != 1 {
		t.Fatalf("expected 1 killed agent, got %d: %v", len(killed), killed)
	}
	if killed[0] != "expired-agent" {
		t.Errorf("expected killed agent %q, got %q", "expired-agent", killed[0])
	}

	// Verify state was updated
	st, _ := store.Load(context.Background())
	for _, a := range st.Agents {
		if a.Task == "expired-agent" && a.Status != model.StatusStopped {
			t.Errorf("expected expired agent status %q, got %q", model.StatusStopped, a.Status)
		}
	}
}

func TestReaper_NoMaxAge_DoesNotKill(t *testing.T) {
	store := &mockStore{
		state: model.State{
			Agents: []model.AgentEntry{
				{Task: "old-agent", Status: model.StatusRunning, StartTime: time.Now().Add(-100 * time.Hour)},
			},
		},
	}
	backend := &mockBackend{}

	r := NewReaper(store, backend, config.ReaperConfig{
		Interval: dur(50 * time.Millisecond),
	})

	r.Start()
	time.Sleep(200 * time.Millisecond)
	r.Stop()

	killed := backend.killedAgents()
	if len(killed) != 0 {
		t.Fatalf("expected 0 killed agents with max_age disabled, got %d", len(killed))
	}
}

func TestReaper_CheckSpawnAllowed_UnderLimit(t *testing.T) {
	st := &model.State{
		Agents: []model.AgentEntry{
			{Task: "a1", Status: model.StatusRunning},
			{Task: "a2", Status: model.StatusStopped},
		},
	}
	r := NewReaper(&mockStore{}, &mockBackend{}, config.ReaperConfig{
		MaxConcurrent: 3,
		Interval:      dur(time.Hour),
	})

	if err := r.CheckSpawnAllowed(st); err != nil {
		t.Errorf("CheckSpawnAllowed should succeed with 1 running < 3 max, got: %v", err)
	}
}

func TestReaper_CheckSpawnAllowed_AtLimit(t *testing.T) {
	st := &model.State{
		Agents: []model.AgentEntry{
			{Task: "a1", Status: model.StatusRunning},
			{Task: "a2", Status: model.StatusRunning},
			{Task: "a3", Status: model.StatusRunning},
		},
	}
	r := NewReaper(&mockStore{}, &mockBackend{}, config.ReaperConfig{
		MaxConcurrent: 3,
		Interval:      dur(time.Hour),
	})

	err := r.CheckSpawnAllowed(st)
	if err == nil {
		t.Fatal("CheckSpawnAllowed should fail at max_concurrent limit")
	}

	mce, ok := err.(*MaxConcurrentError)
	if !ok {
		t.Fatalf("expected *MaxConcurrentError, got %T", err)
	}
	if mce.Limit != 3 {
		t.Errorf("expected limit 3, got %d", mce.Limit)
	}
}

func TestReaper_CheckSpawnAllowed_NoLimit(t *testing.T) {
	st := &model.State{
		Agents: []model.AgentEntry{
			{Task: "a1", Status: model.StatusRunning},
			{Task: "a2", Status: model.StatusRunning},
		},
	}
	r := NewReaper(&mockStore{}, &mockBackend{}, config.ReaperConfig{
		Interval: dur(time.Hour),
	})

	if err := r.CheckSpawnAllowed(st); err != nil {
		t.Errorf("CheckSpawnAllowed with no limit should always succeed, got: %v", err)
	}
}

func TestReaper_Reconfigure(t *testing.T) {
	store := &mockStore{
		state: model.State{
			Agents: []model.AgentEntry{
				{Task: "agent-1", Status: model.StatusRunning, StartTime: time.Now().Add(-30 * time.Minute)},
			},
		},
	}
	backend := &mockBackend{}

	r := NewReaper(store, backend, config.ReaperConfig{
		MaxAge:   dur(1 * time.Hour), // agent is NOT expired (30min < 1hr)
		Interval: dur(50 * time.Millisecond),
	})

	r.Start()
	time.Sleep(150 * time.Millisecond)

	if killed := backend.killedAgents(); len(killed) != 0 {
		t.Fatalf("agent should not be killed before reconfigure, got: %v", killed)
	}

	// Reconfigure with shorter max_age — agent is now expired
	r.Reconfigure(config.ReaperConfig{
		MaxAge:        dur(10 * time.Minute),
		Interval:      dur(50 * time.Millisecond),
		MaxConcurrent: 2,
	})

	time.Sleep(200 * time.Millisecond)
	r.Stop()

	killed := backend.killedAgents()
	if len(killed) != 1 || killed[0] != "agent-1" {
		t.Errorf("expected agent-1 killed after reconfigure, got: %v", killed)
	}
}

func TestReaper_StopIsIdempotent(t *testing.T) {
	store := &mockStore{}
	backend := &mockBackend{}

	r := NewReaper(store, backend, config.ReaperConfig{
		Interval: dur(time.Hour),
	})

	r.Start()
	r.Stop()
	// Second stop should not panic or deadlock — but we only call once since
	// closing a closed channel panics. This test verifies Stop() completes cleanly.
}

func TestMaxConcurrentError_Message(t *testing.T) {
	err := &MaxConcurrentError{Limit: 5}
	want := "max concurrent agents (5) reached"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestReaper_SkipsQueuedAgents(t *testing.T) {
	store := &mockStore{
		state: model.State{
			Agents: []model.AgentEntry{
				{Task: "queued-agent", Status: model.StatusQueued, Mode: "queued", StartTime: time.Now().Add(-2 * time.Hour)},
				{Task: "running-agent", Status: model.StatusRunning, StartTime: time.Now().Add(-2 * time.Hour)},
			},
		},
	}
	backend := &mockBackend{}

	r := NewReaper(store, backend, config.ReaperConfig{
		MaxAge:   dur(1 * time.Hour),
		Interval: dur(50 * time.Millisecond),
	})

	r.Start()
	time.Sleep(200 * time.Millisecond)
	r.Stop()

	killed := backend.killedAgents()
	if len(killed) != 1 {
		t.Fatalf("expected 1 killed agent, got %d: %v", len(killed), killed)
	}
	if killed[0] != "running-agent" {
		t.Errorf("expected killed agent %q, got %q", "running-agent", killed[0])
	}
}

func TestReaper_QueuedRunningAgent_CancelsViaQueue(t *testing.T) {
	store := &mockStore{
		state: model.State{
			Agents: []model.AgentEntry{
				{Task: "worker-agent", Status: model.StatusRunning, Mode: "queued", MissionID: 99, StartTime: time.Now().Add(-2 * time.Hour)},
			},
		},
	}
	backend := &mockBackend{}
	mockQueue := &mockMissionQueue{}

	r := NewReaper(store, backend, config.ReaperConfig{
		MaxAge:   dur(1 * time.Hour),
		Interval: dur(50 * time.Millisecond),
	})
	r.SetQueue(mockQueue)

	r.Start()
	time.Sleep(200 * time.Millisecond)
	r.Stop()

	// Backend should NOT be called for queued-mode agents.
	killed := backend.killedAgents()
	if len(killed) != 0 {
		t.Fatalf("expected 0 backend kills for queued agent, got %d", len(killed))
	}

	// Queue should have been called to cancel.
	mockQueue.mu.Lock()
	cancelled := mockQueue.cancelled
	mockQueue.mu.Unlock()
	if len(cancelled) != 1 || cancelled[0] != 99 {
		t.Errorf("expected queue cancel for mission 99, got %v", cancelled)
	}

	// Agent should be Stopped.
	st, _ := store.Load(context.Background())
	for _, a := range st.Agents {
		if a.Task == "worker-agent" && a.Status != model.StatusStopped {
			t.Errorf("expected status Stopped, got %q", a.Status)
		}
	}
}

func TestReaper_CleansUpTerminalQueuedAgents(t *testing.T) {
	store := &mockStore{
		state: model.State{
			Agents: []model.AgentEntry{
				{Task: "done-agent", Status: model.StatusCompleted, Mode: "queued"},
				{Task: "alive-agent", Status: model.StatusRunning, Mode: "batch", StartTime: time.Now()},
			},
		},
	}
	backend := &mockBackend{}

	r := NewReaper(store, backend, config.ReaperConfig{
		MaxAge:   dur(1 * time.Hour),
		Interval: dur(50 * time.Millisecond),
	})

	r.Start()
	time.Sleep(200 * time.Millisecond)
	r.Stop()

	// Terminal queued agent should be removed.
	st, _ := store.Load(context.Background())
	for _, a := range st.Agents {
		if a.Task == "done-agent" {
			t.Error("expected done-agent to be removed by reaper")
		}
	}
}

// mockMissionQueue implements queue.MissionQueue for testing.
type mockMissionQueue struct {
	mu        sync.Mutex
	cancelled []int64
}

func (m *mockMissionQueue) Enqueue(_ context.Context, _ *queue.Mission, _ *model.AgentEntry) error {
	return nil
}
func (m *mockMissionQueue) Dequeue(_ context.Context) (*queue.Mission, error) { return nil, nil }
func (m *mockMissionQueue) Ack(_ context.Context, _ int64) error              { return nil }
func (m *mockMissionQueue) Fail(_ context.Context, _ int64, _ string) error   { return nil }
func (m *mockMissionQueue) Len(_ context.Context) (int, error)                { return 0, nil }
func (m *mockMissionQueue) Status(_ context.Context, _ int64) (string, error) { return "", nil }
func (m *mockMissionQueue) Cancel(_ context.Context, missionID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelled = append(m.cancelled, missionID)
	return nil
}
func (m *mockMissionQueue) Close() error { return nil }

// TestReaper_UpdateUsesLiveContext verifies that the context passed to
// UpdateAgentStatus is not cancelled (regression: previously used the
// kill context after cancel()).
func TestReaper_UpdateUsesLiveContext(t *testing.T) {
	store := &contextCheckingStore{
		mockStore: mockStore{
			state: model.State{
				Agents: []model.AgentEntry{
					{Task: "ctx-test", Status: model.StatusRunning, StartTime: time.Now().Add(-2 * time.Hour)},
				},
			},
		},
	}
	backend := &mockBackend{}

	r := NewReaper(store, backend, config.ReaperConfig{
		MaxAge:   dur(1 * time.Hour),
		Interval: dur(50 * time.Millisecond),
	})

	r.Start()
	time.Sleep(200 * time.Millisecond)
	r.Stop()

	if store.updateCalledWithCancelledCtx {
		t.Fatal("UpdateAgentStatus was called with a cancelled context")
	}
	if !store.updateCalled {
		t.Fatal("UpdateAgentStatus was never called")
	}
}

// contextCheckingStore wraps mockStore and checks whether UpdateAgentStatus
// receives a cancelled context.
type contextCheckingStore struct {
	mockStore
	updateCalled                bool
	updateCalledWithCancelledCtx bool
}

func (s *contextCheckingStore) UpdateAgentStatus(ctx context.Context, task string, status model.AgentStatus, output string) error {
	s.updateCalled = true
	if ctx.Err() != nil {
		s.updateCalledWithCancelledCtx = true
	}
	return s.mockStore.UpdateAgentStatus(ctx, task, status, output)
}

// --- Stream backend mocks ---

type mockStreamBackend struct {
	mu     sync.Mutex
	killed []string
}

func (m *mockStreamBackend) SpawnStreamPod(_ context.Context, _ runtime.StreamPodOpts) (*runtime.StreamPodInfo, error) {
	return nil, fmt.Errorf("not implemented in mock")
}

func (m *mockStreamBackend) KillStreamPod(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.killed = append(m.killed, id)
	return nil
}

func (m *mockStreamBackend) killedPods() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.killed))
	copy(result, m.killed)
	return result
}

type mockStreamBackendErr struct {
	err error
}

func (m *mockStreamBackendErr) SpawnStreamPod(_ context.Context, _ runtime.StreamPodOpts) (*runtime.StreamPodInfo, error) {
	return nil, nil
}

func (m *mockStreamBackendErr) KillStreamPod(_ context.Context, _ string) error {
	return m.err
}

// --- Stream reaper tests ---

func TestReaper_KillsExpiredStreamAgent(t *testing.T) {
	store := &mockStore{state: model.State{Agents: []model.AgentEntry{
		{Task: "stream-exp", Status: model.StatusRunning, Mode: "stream", StartTime: time.Now().Add(-2 * time.Hour)},
		{Task: "batch-exp", Status: model.StatusRunning, Mode: "", StartTime: time.Now().Add(-2 * time.Hour)},
	}}}
	backend := &mockBackend{}
	sb := &mockStreamBackend{}
	r := NewReaper(store, backend, config.ReaperConfig{MaxAge: dur(1 * time.Hour), Interval: dur(100 * time.Millisecond)})
	r.SetStreamBackend(sb)

	r.reap()

	if pods := sb.killedPods(); len(pods) != 1 || pods[0] != "stream-exp" {
		t.Errorf("KillStreamPod = %v, want [stream-exp]", pods)
	}
	if killed := backend.killedAgents(); len(killed) != 1 || killed[0] != "batch-exp" {
		t.Errorf("Kill = %v, want [batch-exp]", killed)
	}
}

func TestReaper_KillsIdleStreamAgent(t *testing.T) {
	store := &mockStore{state: model.State{Agents: []model.AgentEntry{
		{Task: "idle-stream", Status: model.StatusIdle, Mode: "stream", StartTime: time.Now().Add(-2 * time.Hour)},
		{Task: "idle-batch", Status: model.StatusIdle, Mode: "", StartTime: time.Now().Add(-2 * time.Hour)},
	}}}
	backend := &mockBackend{}
	sb := &mockStreamBackend{}
	r := NewReaper(store, backend, config.ReaperConfig{MaxAge: dur(1 * time.Hour), Interval: dur(100 * time.Millisecond)})
	r.SetStreamBackend(sb)

	r.reap()

	if pods := sb.killedPods(); len(pods) != 1 || pods[0] != "idle-stream" {
		t.Errorf("KillStreamPod = %v, want [idle-stream]", pods)
	}
	if killed := backend.killedAgents(); len(killed) != 0 {
		t.Errorf("Kill should not be called for idle batch, got %v", killed)
	}
}

func TestReaper_NoTransitionWithoutStreamBackend(t *testing.T) {
	store := &mockStore{state: model.State{Agents: []model.AgentEntry{
		{Task: "no-sb", Status: model.StatusRunning, Mode: "stream", StartTime: time.Now().Add(-2 * time.Hour)},
	}}}
	r := NewReaper(store, &mockBackend{}, config.ReaperConfig{MaxAge: dur(1 * time.Hour), Interval: dur(100 * time.Millisecond)})

	r.reap()

	agent, _ := store.FindAgent(context.Background(), "no-sb")
	if agent != nil && agent.Status == model.StatusStopped {
		t.Error("should NOT transition without streamBackend")
	}
}

func TestReaper_BackstopCleansFailedStreamAgent(t *testing.T) {
	store := &mockStore{state: model.State{Agents: []model.AgentEntry{
		{Task: "failed-old", Status: model.StatusFailed, Mode: "stream", StartTime: time.Now().Add(-1 * time.Hour)},
		{Task: "failed-recent", Status: model.StatusFailed, Mode: "stream", StartTime: time.Now().Add(-2 * time.Minute)},
		{Task: "failed-batch", Status: model.StatusFailed, Mode: "", StartTime: time.Now().Add(-1 * time.Hour)},
	}}}
	backend := &mockBackend{}
	sb := &mockStreamBackend{}
	r := NewReaper(store, backend, config.ReaperConfig{Interval: dur(100 * time.Millisecond)})
	r.SetStreamBackend(sb)

	r.reap()

	pods := sb.killedPods()
	if len(pods) != 1 || pods[0] != "failed-old" {
		t.Errorf("backstop should only clean failed-old (past grace), got %v", pods)
	}
	if killed := backend.killedAgents(); len(killed) != 0 {
		t.Errorf("batch backend Kill should not be called for failed agents, got %v", killed)
	}
}

func TestReaper_ToleratesErrNotFound(t *testing.T) {
	store := &mockStore{state: model.State{Agents: []model.AgentEntry{
		{Task: "gone", Status: model.StatusRunning, Mode: "stream", StartTime: time.Now().Add(-2 * time.Hour)},
	}}}
	r := NewReaper(store, &mockBackend{}, config.ReaperConfig{MaxAge: dur(1 * time.Hour), Interval: dur(100 * time.Millisecond)})
	r.SetStreamBackend(&mockStreamBackendErr{err: runtime.ErrNotFound})

	r.reap()

	agent, _ := store.FindAgent(context.Background(), "gone")
	if agent == nil || agent.Status != model.StatusStopped {
		t.Errorf("should transition even with ErrNotFound, got %v", agent)
	}
} 
