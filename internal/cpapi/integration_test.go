package cpapi

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/controlplane"
	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/objective"
	"github.com/darkquasar/fracta/internal/orchestrator"
	"github.com/darkquasar/fracta/internal/queue"
)

// Round-trip integration test: RemoteControlPlaneClient <-> HTTPServer <-> LocalControlPlaneClient.
// Backed by test doubles — not a full integration test with real store/backend.

type integrationFixture struct {
	remote   *RemoteControlPlaneClient
	local    *LocalControlPlaneClient
	store    *testStore
	objStore *testObjStore
	queue    *testQueue
	backend  *testBackend
	registry *orchestrator.ProcessRegistry
	ts       *httptest.Server
}

func newIntegrationFixture(t *testing.T) *integrationFixture {
	t.Helper()

	store := &testStore{}
	mb := &testMailbox{}
	backend := &testBackend{logOutput: "integration test logs"}
	ws := &testWorkspace{}
	q := &testQueue{}
	objStore := newTestObjStore()
	registry := orchestrator.NewProcessRegistry()

	hostReg := host.NewMapRegistry("test")
	hostReg.Register("test", &testHost{})

	cfg := &config.Config{
		Runtimes: map[string]config.RuntimeEntry{
			"test": {Model: "test-model", ModelTiers: map[string]string{"heavy": "test-heavy"}},
		},
		Project: config.ProjectConfig{
			AllowedTools: []string{"Bash", "Read"},
		},
		Agents: config.AgentsConfig{DefaultRuntime: "test"},
	}

	cp := &controlplane.ControlPlane{
		Backend:   backend,
		Store:     store,
		Mailbox:   mb,
		Workspace: ws,
		Queue:     q,
		Config:    cfg,
		Events:    events.NoopBus{},
		Profile:   controlplane.Profile{BackendType: "local"},
	}

	local := NewLocalControlPlaneClient(cp, "/tmp/integration-root",
		WithProcessRegistry(registry),
		WithRuntimeRegistry(hostReg),
		WithObjectiveStore(objStore),
	)

	srv := NewHTTPServer(":0", local)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	remote := NewRemoteControlPlaneClient(ts.URL)

	return &integrationFixture{
		remote:   remote,
		local:    local,
		store:    store,
		objStore: objStore,
		queue:    q,
		backend:  backend,
		registry: registry,
		ts:       ts,
	}
}

func TestIntegration_ListAgents_Empty(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()

	resp, err := f.remote.ListAgents(ctx, ListAgentsRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.Agents)
}

func TestIntegration_ListAgents_WithData(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()

	f.store.agents = []model.AgentEntry{
		{Task: "agent-a", Status: model.StatusRunning, Mode: "batch", BranchName: "feature/agent-a"},
		{Task: "agent-b", Status: model.StatusCompleted, Mode: "stream"},
	}

	resp, err := f.remote.ListAgents(ctx, ListAgentsRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.Agents, 2)
	assert.Equal(t, "agent-a", resp.Agents[0].Name)
	assert.Equal(t, model.StatusRunning, resp.Agents[0].Status)
	assert.Equal(t, "agent-b", resp.Agents[1].Name)
}

func TestIntegration_GetAgent(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()

	f.store.agents = []model.AgentEntry{
		{Task: "agent-a", Status: model.StatusIdle, Mode: "stream", ObjectiveID: "obj-1"},
	}

	resp, err := f.remote.GetAgent(ctx, GetAgentRequest{Name: "agent-a"})
	require.NoError(t, err)
	assert.Equal(t, "agent-a", resp.Agent.Name)
	assert.Equal(t, model.StatusIdle, resp.Agent.Status)
	assert.Equal(t, "obj-1", resp.Agent.ObjectiveID)
}

func TestIntegration_GetAgent_NotFound(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()

	_, err := f.remote.GetAgent(ctx, GetAgentRequest{Name: "nonexistent"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestIntegration_GetLogs(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()

	resp, err := f.remote.GetLogs(ctx, GetLogsRequest{Task: "agent-a", Lines: 50})
	require.NoError(t, err)
	assert.Equal(t, "integration test logs", resp.Output)
}

func TestIntegration_Peek_FallbackToState(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()

	f.store.agents = []model.AgentEntry{
		{Task: "agent-a", LastOutput: "output from state"},
	}

	resp, err := f.remote.Peek(ctx, PeekRequest{Name: "agent-a"})
	require.NoError(t, err)
	assert.Equal(t, "output from state", resp.Output)
}

func TestIntegration_Peek_StreamHandle(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()

	f.store.agents = []model.AgentEntry{
		{Task: "agent-a", LastOutput: "old"},
	}
	f.registry.Register("agent-a", &testStreamSession{recentOutput: "live output"})

	resp, err := f.remote.Peek(ctx, PeekRequest{Name: "agent-a"})
	require.NoError(t, err)
	assert.Equal(t, "live output", resp.Output)
}

func TestIntegration_Kill(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()

	f.store.agents = []model.AgentEntry{
		{Task: "agent-a", Status: model.StatusCompleted, WorkspacePath: "/tmp/ws"},
	}

	resp, err := f.remote.Kill(ctx, KillRequest{Name: "agent-a"})
	require.NoError(t, err)
	assert.Equal(t, "killed", resp.Status)

	a, _ := f.store.FindAgent(ctx, "agent-a")
	assert.Nil(t, a)
}

func TestIntegration_GetMission(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()

	f.store.agents = []model.AgentEntry{
		{Task: "agent-a", MissionID: 42, ObjectiveID: "obj-1"},
	}
	f.queue.missions = []*queue.Mission{{ID: 42, Status: "claimed"}}

	resp, err := f.remote.GetMission(ctx, GetMissionRequest{Name: "agent-a"})
	require.NoError(t, err)
	assert.Equal(t, int64(42), resp.Mission.MissionID)
	assert.Equal(t, "claimed", resp.Mission.Status)
}

func TestIntegration_CreateObjective(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()

	resp, err := f.remote.CreateObjective(ctx, CreateObjectiveRequest{
		Description: "integration test objective",
		MaxMissions: 10,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Objective.ID)
	assert.Equal(t, objective.StatusOpen, resp.Objective.Status)
	assert.Equal(t, 10, resp.Objective.MaxMissions)
}

func TestIntegration_ListObjectives(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()

	f.objStore.objectives["obj-1"] = &objective.Objective{
		ID: "obj-1", Status: objective.StatusOpen, Description: "test",
	}

	resp, err := f.remote.ListObjectives(ctx, ListObjectivesRequest{Status: "open"})
	require.NoError(t, err)
	assert.Len(t, resp.Objectives, 1)
	assert.Equal(t, "obj-1", resp.Objectives[0].ID)
}

func TestIntegration_GetObjective(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()

	f.objStore.objectives["obj-1"] = &objective.Objective{
		ID: "obj-1", Status: objective.StatusAnswered, Description: "answered",
		Outcome: "found it",
	}

	resp, err := f.remote.GetObjective(ctx, GetObjectiveRequest{ID: "obj-1"})
	require.NoError(t, err)
	assert.Equal(t, "answered", resp.Objective.Description)
	assert.Equal(t, "found it", resp.Objective.Outcome)
}

func TestIntegration_GetObjective_NotFound(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()

	_, err := f.remote.GetObjective(ctx, GetObjectiveRequest{ID: "nonexistent"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

// TestIntegration_SayNotFound verifies error propagation through the full stack.
func TestIntegration_SayNotFound(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()

	_, err := f.remote.Say(ctx, SayRequest{Name: "nonexistent", Message: "hello"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// --- Graph round-trip integration tests (spec-37) ---

// newGraphIntegrationFixture creates an integration fixture with a mock GraphClient.
func newGraphIntegrationFixture(t *testing.T, gc graph.GraphClient) *integrationFixture {
	t.Helper()

	store := &testStore{}
	mb := &testMailbox{}
	backend := &testBackend{logOutput: "test logs"}
	ws := &testWorkspace{}
	q := &testQueue{}
	objStore := newTestObjStore()
	registry := orchestrator.NewProcessRegistry()

	hostReg := host.NewMapRegistry("test")
	hostReg.Register("test", &testHost{})

	cfg := &config.Config{
		Runtimes: map[string]config.RuntimeEntry{
			"test": {Model: "test-model"},
		},
		Agents: config.AgentsConfig{DefaultRuntime: "test"},
	}

	cp := &controlplane.ControlPlane{
		Backend:   backend,
		Store:     store,
		Mailbox:   mb,
		Workspace: ws,
		Queue:     q,
		Config:    cfg,
		Events:    events.NoopBus{},
		Profile:   controlplane.Profile{BackendType: "local"},
	}

	local := NewLocalControlPlaneClient(cp, "/tmp/integration-root",
		WithProcessRegistry(registry),
		WithRuntimeRegistry(hostReg),
		WithObjectiveStore(objStore),
		WithGraphClient(gc),
	)

	srv := NewHTTPServer(":0", local)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	remote := NewRemoteControlPlaneClient(ts.URL)

	return &integrationFixture{
		remote:   remote,
		local:    local,
		store:    store,
		objStore: objStore,
		queue:    q,
		backend:  backend,
		registry: registry,
		ts:       ts,
	}
}

func TestIntegration_GraphQuery(t *testing.T) {
	gc := &mockGraphClient{
		queryResult: []graph.Record{{"name": "System-A", "type": "System"}},
	}
	f := newGraphIntegrationFixture(t, gc)
	ctx := context.Background()

	resp, err := f.remote.GraphQuery(ctx, GraphQueryRequest{
		Cypher: "MATCH (n:System) RETURN n.name AS name",
	})
	require.NoError(t, err)
	require.Len(t, resp.Records, 1)
	assert.Equal(t, "System-A", resp.Records[0]["name"])
}

func TestIntegration_GraphUpdate(t *testing.T) {
	gc := &mockGraphClient{}
	f := newGraphIntegrationFixture(t, gc)
	ctx := context.Background()

	resp, err := f.remote.GraphUpdate(ctx, GraphUpdateRequest{
		Cypher:     "MERGE (n:Test {name: $name})",
		Params:     map[string]any{"name": "node-1"},
		Source:     "agent:integration",
		Confidence: "high",
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Status)
	// Verify provenance was injected on the server side.
	assert.Equal(t, "agent:integration", gc.lastUpdateParams["source"])
	assert.Equal(t, "high", gc.lastUpdateParams["confidence"])
	assert.Contains(t, gc.lastUpdateParams, "updated_at")
}

func TestIntegration_GraphSchema(t *testing.T) {
	gc := &mockGraphClient{
		queryResult: []graph.Record{{"label": "System"}},
	}
	f := newGraphIntegrationFixture(t, gc)
	ctx := context.Background()

	resp, err := f.remote.GraphSchema(ctx, GraphSchemaRequest{})
	require.NoError(t, err)
	// The mock returns the same result for all 3 schema queries,
	// so labels, relationship_types, property_keys all get populated.
	assert.NotNil(t, resp)
}

func TestIntegration_GraphPath_NonEmpty(t *testing.T) {
	gc := &mockGraphClient{
		queryResult: []graph.Record{{"p": "path-data"}},
	}
	f := newGraphIntegrationFixture(t, gc)
	ctx := context.Background()

	resp, err := f.remote.GraphPath(ctx, GraphPathRequest{
		FromLabel: "System",
		FromKey:   "name",
		FromValue: "server_a",
		ToLabel:   "System",
		ToKey:     "name",
		ToValue:   "server_b",
	})
	require.NoError(t, err)
	require.Len(t, resp.Records, 1)
}

func TestIntegration_GraphPath_Empty(t *testing.T) {
	gc := &mockGraphClient{
		queryResult: []graph.Record{},
	}
	f := newGraphIntegrationFixture(t, gc)
	ctx := context.Background()

	resp, err := f.remote.GraphPath(ctx, GraphPathRequest{
		FromLabel: "System",
		FromKey:   "name",
		FromValue: "no_such",
		ToLabel:   "System",
		ToKey:     "name",
		ToValue:   "no_such_either",
	})
	require.NoError(t, err)
	assert.Empty(t, resp.Records)
}

func TestIntegration_GraphNeighbors(t *testing.T) {
	gc := &mockGraphClient{
		queryResult: []graph.Record{
			{"labels": []string{"Identity"}, "props": map[string]any{"name": "user-1"}},
		},
	}
	f := newGraphIntegrationFixture(t, gc)
	ctx := context.Background()

	resp, err := f.remote.GraphNeighbors(ctx, GraphNeighborsRequest{
		Label:     "System",
		Key:       "name",
		Value:     "server_a",
		Depth:     2,
		EdgeTypes: []string{"USES"},
	})
	require.NoError(t, err)
	require.Len(t, resp.Records, 1)
}

func TestIntegration_Graph_NotConfigured(t *testing.T) {
	// No GraphClient wired → all graph methods should return errors.
	f := newIntegrationFixture(t)
	ctx := context.Background()

	_, err := f.remote.GraphQuery(ctx, GraphQueryRequest{Cypher: "MATCH (n) RETURN n"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "graph not configured")

	_, err = f.remote.GraphSchema(ctx, GraphSchemaRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "graph not configured")
}
