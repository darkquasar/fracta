package cpapi

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/controlplane"
	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/mailbox"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/objective"
	"github.com/darkquasar/fracta/internal/orchestrator"
	"github.com/darkquasar/fracta/internal/queue"
	"github.com/darkquasar/fracta/internal/runtime"
	"github.com/darkquasar/fracta/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Test doubles ---

type testStore struct {
	agents []model.AgentEntry
}

func (s *testStore) Load(_ context.Context) (model.State, error) {
	return model.State{Agents: s.agents}, nil
}
func (s *testStore) WithLock(_ context.Context, fn func(*model.State) error) error {
	st := &model.State{Agents: s.agents}
	if err := fn(st); err != nil {
		return err
	}
	s.agents = st.Agents
	return nil
}
func (s *testStore) FindAgent(_ context.Context, task string) (*model.AgentEntry, error) {
	for i := range s.agents {
		if s.agents[i].Task == task {
			return &s.agents[i], nil
		}
	}
	return nil, nil
}
func (s *testStore) RemoveAgent(_ context.Context, task string) error {
	filtered := s.agents[:0]
	for _, a := range s.agents {
		if a.Task != task {
			filtered = append(filtered, a)
		}
	}
	s.agents = filtered
	return nil
}
func (s *testStore) UpdateAgentStatus(_ context.Context, task string, status model.AgentStatus, lastOutput string) error {
	for i := range s.agents {
		if s.agents[i].Task == task {
			s.agents[i].Status = status
			if lastOutput != "" {
				s.agents[i].LastOutput = lastOutput
			}
			return nil
		}
	}
	return nil
}
func (s *testStore) UpdateAgentResult(_ context.Context, task string, status model.AgentStatus, lastOutput, resumeToken string) error {
	for i := range s.agents {
		if s.agents[i].Task == task {
			s.agents[i].Status = status
			s.agents[i].LastOutput = lastOutput
			s.agents[i].ResumeToken = resumeToken
			return nil
		}
	}
	return fmt.Errorf("agent %s not found", task)
}
func (s *testStore) UpdateAgentIntent(_ context.Context, task, intent string) error {
	for i := range s.agents {
		if s.agents[i].Task == task {
			s.agents[i].CurrentIntent = intent
			return nil
		}
	}
	return nil
}
func (s *testStore) ClaimAgent(_ context.Context, task string) error { return nil }
func (s *testStore) UpdateAgentStatusIf(_ context.Context, _ string, _ []model.AgentStatus, _ model.AgentStatus, _ string) (bool, error) {
	return true, nil
}
func (s *testStore) UpdateAgentResultIf(_ context.Context, _ string, _ []model.AgentStatus, _ model.AgentStatus, _, _ string) (bool, error) {
	return true, nil
}
func (s *testStore) UpdateChessmaster(_ context.Context, _, _ string, _ time.Time) error {
	return nil
}
func (s *testStore) Mailbox() mailbox.Mailbox { return &testMailbox{} }
func (s *testStore) Close() error             { return nil }

type testMailbox struct {
	messages []mailbox.Message
}

func (m *testMailbox) Send(_ context.Context, from, to, content string) error { return nil }
func (m *testMailbox) Read(_ context.Context, task string) ([]mailbox.Message, error) {
	return m.messages, nil
}
func (m *testMailbox) UnreadCount(_ context.Context, task string) (int, error) {
	count := 0
	for _, msg := range m.messages {
		if msg.To == task && !msg.Read {
			count++
		}
	}
	return count, nil
}
func (m *testMailbox) Remove(_ context.Context, task string) error { return nil }

type testBackend struct {
	logOutput string
	killCalls []string
}

func (b *testBackend) Spawn(_ context.Context, _ runtime.SpawnOpts) (runtime.AgentHandle, error) {
	return nil, fmt.Errorf("not implemented in test")
}
func (b *testBackend) Kill(_ context.Context, id string) error {
	b.killCalls = append(b.killCalls, id)
	return nil
}
func (b *testBackend) Status(_ context.Context, _ string) (model.AgentStatus, error) {
	return model.StatusCompleted, nil
}
func (b *testBackend) Logs(_ context.Context, _ string, _ int) (string, error) {
	return b.logOutput, nil
}

type testWorkspace struct{}

func (w *testWorkspace) Create(agentID, baseBranch string) (*workspace.Info, error) {
	return &workspace.Info{
		Path:       "/tmp/worktrees/" + agentID,
		BranchName: "feature/" + agentID,
		BaseBranch: baseBranch,
	}, nil
}
func (w *testWorkspace) Remove(_ *workspace.Info, _ bool) error { return nil }

type testQueue struct {
	missions    []*queue.Mission
	missionIDCt int64
}

func (q *testQueue) Enqueue(_ context.Context, m *queue.Mission, agent *model.AgentEntry) error {
	q.missionIDCt++
	m.ID = q.missionIDCt
	q.missions = append(q.missions, m)
	return nil
}
func (q *testQueue) Dequeue(_ context.Context) (*queue.Mission, error) {
	return nil, fmt.Errorf("not implemented")
}
func (q *testQueue) Ack(_ context.Context, _ int64) error            { return nil }
func (q *testQueue) Fail(_ context.Context, _ int64, _ string) error { return nil }
func (q *testQueue) Len(_ context.Context) (int, error)              { return len(q.missions), nil }
func (q *testQueue) Status(_ context.Context, missionID int64) (string, error) {
	for _, m := range q.missions {
		if m.ID == missionID {
			return m.Status, nil
		}
	}
	return "", queue.ErrNotFound
}
func (q *testQueue) Cancel(_ context.Context, _ int64) error { return nil }
func (q *testQueue) Close() error                            { return nil }

type testObjStore struct {
	objectives map[string]*objective.Objective
}

func newTestObjStore() *testObjStore {
	return &testObjStore{objectives: make(map[string]*objective.Objective)}
}

func (s *testObjStore) Create(_ context.Context, o *objective.Objective) error {
	o.CreatedAt = time.Now()
	s.objectives[o.ID] = o
	return nil
}
func (s *testObjStore) Get(_ context.Context, id string) (*objective.Objective, error) {
	o, ok := s.objectives[id]
	if !ok {
		return nil, objective.ErrNotFound
	}
	return o, nil
}
func (s *testObjStore) Update(_ context.Context, o *objective.Objective) error {
	s.objectives[o.ID] = o
	return nil
}
func (s *testObjStore) IncrementMissionCount(_ context.Context, id string) error { return nil }
func (s *testObjStore) IncrementFindingCount(_ context.Context, id string) error { return nil }
func (s *testObjStore) ListByStatus(_ context.Context, status objective.ObjectiveStatus) ([]*objective.Objective, error) {
	var result []*objective.Objective
	for _, o := range s.objectives {
		if o.Status == status {
			result = append(result, o)
		}
	}
	return result, nil
}

type testHost struct{}

func (h *testHost) WriteWorkspace(_ string, _ []string, _ host.WorkspaceConfig) error {
	return nil
}
func (h *testHost) Bootstrap(task, baseBranch, contract string) host.BootstrapResult {
	return host.BootstrapResult{FileName: "CLAUDE.md", FileBody: contract, InitialPrompt: "execute"}
}
func (h *testHost) BuildBatchCommand(prompt, model, resumeToken string) host.CommandSpec {
	return host.CommandSpec{Command: "echo", Args: []string{"test"}}
}
func (h *testHost) ParseBatchOutput(stdout []byte, waitErr error) (host.Result, error) {
	return host.Result{Output: string(stdout)}, nil
}
func (h *testHost) StartStream(_, _, _ string) (host.StreamSession, error) {
	return nil, host.ErrStreamNotSupported
}
func (h *testHost) Capabilities() host.Capabilities {
	return host.Capabilities{ResumeToken: true, AgentMCP: true, StructuredEvents: true}
}

// --- Test fixtures ---

func newTestClient(t *testing.T, opts ...func(*testFixture)) *testFixture {
	t.Helper()

	f := &testFixture{
		store:    &testStore{},
		mb:       &testMailbox{},
		backend:  &testBackend{logOutput: "test log output"},
		ws:       &testWorkspace{},
		objStore: newTestObjStore(),
		queue:    &testQueue{},
	}
	for _, opt := range opts {
		opt(f)
	}

	hostReg := host.NewMapRegistry("test")
	hostReg.Register("test", &testHost{})

	cfg := &config.Config{
		Project: config.ProjectConfig{
			AllowedTools: []string{"Bash", "Read", "Write"},
		},
		Agents: config.AgentsConfig{
			DefaultRuntime: "test",
			AgentRuntimes: map[string]config.RuntimeEntry{
				"test": {Model: "test-model", ModelTiers: map[string]string{"heavy": "test-heavy"}},
			},
		},
	}

	cp := &controlplane.ControlPlane{
		Backend:   f.backend,
		Store:     f.store,
		Mailbox:   f.mb,
		Workspace: f.ws,
		Queue:     f.queue,
		Config:    cfg,
		Events:    events.NoopBus{},
		Profile:   controlplane.Profile{BackendType: "local"},
	}
	cp.ObjectiveStore = f.objStore

	f.registry = orchestrator.NewProcessRegistry()
	f.client = NewLocalControlPlaneClient(cp, "/tmp/test-root",
		WithProcessRegistry(f.registry),
		WithRuntimeRegistry(hostReg),
		WithObjectiveStore(f.objStore),
	)
	f.cp = cp
	return f
}

type testFixture struct {
	client   *LocalControlPlaneClient
	store    *testStore
	mb       *testMailbox
	backend  *testBackend
	ws       *testWorkspace
	objStore *testObjStore
	queue    *testQueue
	registry *orchestrator.ProcessRegistry
	cp       *controlplane.ControlPlane
}

// --- Tests ---

func TestListAgents_Empty(t *testing.T) {
	f := newTestClient(t)
	resp, err := f.client.ListAgents(context.Background(), ListAgentsRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.Agents)
}

func TestListAgents_WithAgents(t *testing.T) {
	f := newTestClient(t)
	f.store.agents = []model.AgentEntry{
		{Task: "agent-1", Status: model.StatusRunning, Mode: "batch", BranchName: "feature/agent-1"},
		{Task: "agent-2", Status: model.StatusCompleted, Mode: "stream", BranchName: "feature/agent-2"},
	}

	resp, err := f.client.ListAgents(context.Background(), ListAgentsRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.Agents, 2)
	assert.Equal(t, "agent-1", resp.Agents[0].Name)
	assert.Equal(t, model.StatusRunning, resp.Agents[0].Status)
	assert.Equal(t, "batch", resp.Agents[0].Mode)
	assert.Equal(t, "agent-2", resp.Agents[1].Name)
	assert.Equal(t, model.StatusCompleted, resp.Agents[1].Status)
}

func TestListAgents_DefaultModeBatch(t *testing.T) {
	f := newTestClient(t)
	f.store.agents = []model.AgentEntry{
		{Task: "agent-1", Status: model.StatusRunning}, // Mode empty
	}

	resp, err := f.client.ListAgents(context.Background(), ListAgentsRequest{})
	require.NoError(t, err)
	assert.Equal(t, "batch", resp.Agents[0].Mode)
}

func TestGetAgent_Found(t *testing.T) {
	f := newTestClient(t)
	f.store.agents = []model.AgentEntry{
		{Task: "agent-1", Status: model.StatusRunning, Mode: "batch", ObjectiveID: "obj-1"},
	}

	resp, err := f.client.GetAgent(context.Background(), GetAgentRequest{Name: "agent-1"})
	require.NoError(t, err)
	assert.Equal(t, "agent-1", resp.Agent.Name)
	assert.Equal(t, "obj-1", resp.Agent.ObjectiveID)
}

func TestGetAgent_NotFound(t *testing.T) {
	f := newTestClient(t)
	_, err := f.client.GetAgent(context.Background(), GetAgentRequest{Name: "nonexistent"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetMission_Success(t *testing.T) {
	f := newTestClient(t)
	f.store.agents = []model.AgentEntry{
		{Task: "agent-1", MissionID: 42, ObjectiveID: "obj-1"},
	}
	f.queue.missions = []*queue.Mission{
		{ID: 42, Status: "pending"},
	}

	resp, err := f.client.GetMission(context.Background(), GetMissionRequest{Name: "agent-1"})
	require.NoError(t, err)
	assert.Equal(t, int64(42), resp.Mission.MissionID)
	assert.Equal(t, "pending", resp.Mission.Status)
	assert.Equal(t, "obj-1", resp.Mission.ObjectiveID)
}

func TestGetMission_NoMission(t *testing.T) {
	f := newTestClient(t)
	f.store.agents = []model.AgentEntry{
		{Task: "agent-1", MissionID: 0},
	}

	_, err := f.client.GetMission(context.Background(), GetMissionRequest{Name: "agent-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no associated mission")
}

func TestGetLogs_Success(t *testing.T) {
	f := newTestClient(t)
	f.backend.logOutput = "line 1\nline 2\nline 3"

	resp, err := f.client.GetLogs(context.Background(), GetLogsRequest{Task: "agent-1", Lines: 50})
	require.NoError(t, err)
	assert.Equal(t, "line 1\nline 2\nline 3", resp.Output)
}

func TestGetLogs_DefaultLines(t *testing.T) {
	f := newTestClient(t)
	f.backend.logOutput = "output"

	resp, err := f.client.GetLogs(context.Background(), GetLogsRequest{Task: "agent-1"})
	require.NoError(t, err)
	assert.Equal(t, "output", resp.Output)
}

func TestPeek_FallsBackToOrchestrator(t *testing.T) {
	f := newTestClient(t)
	f.store.agents = []model.AgentEntry{
		{Task: "agent-1", LastOutput: "last output from state"},
	}

	// No stream handle registered, so it falls back to orchestrator peek.
	resp, err := f.client.Peek(context.Background(), PeekRequest{Name: "agent-1"})
	require.NoError(t, err)
	assert.Equal(t, "last output from state", resp.Output)
}

func TestPeek_StreamHandle(t *testing.T) {
	f := newTestClient(t)
	f.store.agents = []model.AgentEntry{
		{Task: "agent-1", LastOutput: "old output"},
	}
	f.registry.Register("agent-1", &testStreamSession{recentOutput: "live stream output"})

	resp, err := f.client.Peek(context.Background(), PeekRequest{Name: "agent-1"})
	require.NoError(t, err)
	assert.Equal(t, "live stream output", resp.Output)
}

func TestKill_Success(t *testing.T) {
	f := newTestClient(t)
	f.store.agents = []model.AgentEntry{
		{Task: "agent-1", Status: model.StatusCompleted, WorkspacePath: "/tmp/ws/agent-1"},
	}

	resp, err := f.client.Kill(context.Background(), KillRequest{Name: "agent-1"})
	require.NoError(t, err)
	assert.Equal(t, "killed", resp.Status)

	// Agent should be removed from store.
	a, _ := f.store.FindAgent(context.Background(), "agent-1")
	assert.Nil(t, a)
}

func TestKill_ClosesStreamHandle(t *testing.T) {
	f := newTestClient(t)
	f.store.agents = []model.AgentEntry{
		{Task: "agent-1", Status: model.StatusIdle, WorkspacePath: "/tmp/ws/agent-1"},
	}
	session := &testStreamSession{}
	f.registry.Register("agent-1", session)

	resp, err := f.client.Kill(context.Background(), KillRequest{Name: "agent-1"})
	require.NoError(t, err)
	assert.Equal(t, "killed", resp.Status)
	assert.True(t, session.closed)
	// Handle should be removed from registry.
	assert.Nil(t, f.registry.Get("agent-1"))
}

func TestCreateObjective_Success(t *testing.T) {
	f := newTestClient(t)

	resp, err := f.client.CreateObjective(context.Background(), CreateObjectiveRequest{
		Description: "Test objective",
		MaxMissions: 50,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Objective.ID)
	assert.Equal(t, objective.StatusOpen, resp.Objective.Status)
	assert.Equal(t, 50, resp.Objective.MaxMissions)
	// ApplyDefaults should fill in other fields.
	assert.Equal(t, objective.DefaultMaxDepth, resp.Objective.MaxDepth)
}

func TestCreateObjective_CustomIDAndMaxRuntime(t *testing.T) {
	f := newTestClient(t)

	resp, err := f.client.CreateObjective(context.Background(), CreateObjectiveRequest{
		ID:          "my-hunt",
		Description: "Custom ID objective",
		MaxRuntime:  "2h30m",
	})
	require.NoError(t, err)
	assert.Equal(t, "my-hunt", resp.Objective.ID)

	// Verify the stored objective has the parsed duration.
	stored := f.objStore.objectives["my-hunt"]
	require.NotNil(t, stored)
	assert.Equal(t, 2*3600000000000+30*60000000000, int(stored.MaxRuntime)) // 2h30m in nanoseconds
}

func TestCreateObjective_InvalidMaxRuntime(t *testing.T) {
	f := newTestClient(t)

	_, err := f.client.CreateObjective(context.Background(), CreateObjectiveRequest{
		Description: "Bad runtime",
		MaxRuntime:  "not-a-duration",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid max_runtime")
}

func TestListObjectives_ByStatus(t *testing.T) {
	f := newTestClient(t)
	f.objStore.objectives["obj-1"] = &objective.Objective{
		ID: "obj-1", Status: objective.StatusOpen, Description: "open obj",
	}
	f.objStore.objectives["obj-2"] = &objective.Objective{
		ID: "obj-2", Status: objective.StatusAnswered, Description: "answered obj",
	}

	resp, err := f.client.ListObjectives(context.Background(), ListObjectivesRequest{Status: "open"})
	require.NoError(t, err)
	assert.Len(t, resp.Objectives, 1)
	assert.Equal(t, "obj-1", resp.Objectives[0].ID)
}

func TestListObjectives_All(t *testing.T) {
	f := newTestClient(t)
	f.objStore.objectives["obj-1"] = &objective.Objective{
		ID: "obj-1", Status: objective.StatusOpen,
	}
	f.objStore.objectives["obj-2"] = &objective.Objective{
		ID: "obj-2", Status: objective.StatusAnswered,
	}

	resp, err := f.client.ListObjectives(context.Background(), ListObjectivesRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.Objectives, 2)
}

func TestGetObjective_Found(t *testing.T) {
	f := newTestClient(t)
	f.objStore.objectives["obj-1"] = &objective.Objective{
		ID: "obj-1", Status: objective.StatusOpen, Description: "test",
	}

	resp, err := f.client.GetObjective(context.Background(), GetObjectiveRequest{ID: "obj-1"})
	require.NoError(t, err)
	assert.Equal(t, "test", resp.Objective.Description)
}

func TestGetObjective_NotFound(t *testing.T) {
	f := newTestClient(t)
	_, err := f.client.GetObjective(context.Background(), GetObjectiveRequest{ID: "nonexistent"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestSay_AgentNotFound(t *testing.T) {
	f := newTestClient(t)
	_, err := f.client.Say(context.Background(), SayRequest{Name: "nonexistent", Message: "hello"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestSpawn_ObjectiveRequiresQueue(t *testing.T) {
	f := newTestClient(t)
	// Set queue to nil to simulate no queue.
	f.cp.Queue = nil

	_, err := f.client.Spawn(context.Background(), SpawnRequest{
		Task:        "test-agent",
		Contract:    "do stuff",
		Dispatch:    "queued",
		ObjectiveID: "obj-1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "queue mode not configured")
}

func TestSpawn_ObjectiveDirectDispatchRejected(t *testing.T) {
	f := newTestClient(t)
	_, err := f.client.Spawn(context.Background(), SpawnRequest{
		Task:        "test-agent",
		Contract:    "do stuff",
		Dispatch:    "direct",
		ObjectiveID: "obj-1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "objective_id requires queued dispatch")
}

// --- Stream session test double ---

type testStreamSession struct {
	recentOutput string
	closed       bool
	doneCh       chan struct{}
}

func (s *testStreamSession) Send(message string) (host.Result, error) {
	return host.Result{Output: "response"}, nil
}
func (s *testStreamSession) ResumeToken() string              { return "test-token" }
func (s *testStreamSession) RecentOutput(maxBytes int) string { return s.recentOutput }
func (s *testStreamSession) Done() <-chan struct{} {
	if s.doneCh == nil {
		s.doneCh = make(chan struct{})
	}
	return s.doneCh
}
func (s *testStreamSession) Close() error {
	s.closed = true
	if s.doneCh != nil {
		select {
		case <-s.doneCh:
		default:
			close(s.doneCh)
		}
	}
	return nil
}
