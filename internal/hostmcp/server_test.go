package hostmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockClient implements cpapi.ControlPlaneClient for testing.
type mockClient struct {
	spawnFn          func(ctx context.Context, req cpapi.SpawnRequest) (*cpapi.SpawnResponse, error)
	listAgentsFn     func(ctx context.Context, req cpapi.ListAgentsRequest) (*cpapi.ListAgentsResponse, error)
	getAgentFn       func(ctx context.Context, req cpapi.GetAgentRequest) (*cpapi.GetAgentResponse, error)
	getMissionFn     func(ctx context.Context, req cpapi.GetMissionRequest) (*cpapi.GetMissionResponse, error)
	peekFn           func(ctx context.Context, req cpapi.PeekRequest) (*cpapi.PeekResponse, error)
	getLogsFn        func(ctx context.Context, req cpapi.GetLogsRequest) (*cpapi.GetLogsResponse, error)
	sayFn            func(ctx context.Context, req cpapi.SayRequest) (*cpapi.SayResponse, error)
	killFn           func(ctx context.Context, req cpapi.KillRequest) (*cpapi.KillResponse, error)
	createObjFn      func(ctx context.Context, req cpapi.CreateObjectiveRequest) (*cpapi.CreateObjectiveResponse, error)
	listObjsFn       func(ctx context.Context, req cpapi.ListObjectivesRequest) (*cpapi.ListObjectivesResponse, error)
	getObjFn         func(ctx context.Context, req cpapi.GetObjectiveRequest) (*cpapi.GetObjectiveResponse, error)
}

func (m *mockClient) Spawn(ctx context.Context, req cpapi.SpawnRequest) (*cpapi.SpawnResponse, error) {
	if m.spawnFn != nil {
		return m.spawnFn(ctx, req)
	}
	return &cpapi.SpawnResponse{Agent: req.Task, Status: "running", Mode: "batch"}, nil
}

func (m *mockClient) ListAgents(ctx context.Context, req cpapi.ListAgentsRequest) (*cpapi.ListAgentsResponse, error) {
	if m.listAgentsFn != nil {
		return m.listAgentsFn(ctx, req)
	}
	return &cpapi.ListAgentsResponse{}, nil
}

func (m *mockClient) GetAgent(ctx context.Context, req cpapi.GetAgentRequest) (*cpapi.GetAgentResponse, error) {
	if m.getAgentFn != nil {
		return m.getAgentFn(ctx, req)
	}
	return nil, fmt.Errorf("agent %q not found", req.Name)
}

func (m *mockClient) GetMission(ctx context.Context, req cpapi.GetMissionRequest) (*cpapi.GetMissionResponse, error) {
	if m.getMissionFn != nil {
		return m.getMissionFn(ctx, req)
	}
	return nil, fmt.Errorf("not implemented")
}

func (m *mockClient) Peek(ctx context.Context, req cpapi.PeekRequest) (*cpapi.PeekResponse, error) {
	if m.peekFn != nil {
		return m.peekFn(ctx, req)
	}
	return &cpapi.PeekResponse{Output: "test output"}, nil
}

func (m *mockClient) GetLogs(ctx context.Context, req cpapi.GetLogsRequest) (*cpapi.GetLogsResponse, error) {
	if m.getLogsFn != nil {
		return m.getLogsFn(ctx, req)
	}
	return &cpapi.GetLogsResponse{Output: "test logs"}, nil
}

func (m *mockClient) Say(ctx context.Context, req cpapi.SayRequest) (*cpapi.SayResponse, error) {
	if m.sayFn != nil {
		return m.sayFn(ctx, req)
	}
	return &cpapi.SayResponse{Status: "completed", Message: "response"}, nil
}

func (m *mockClient) Kill(ctx context.Context, req cpapi.KillRequest) (*cpapi.KillResponse, error) {
	if m.killFn != nil {
		return m.killFn(ctx, req)
	}
	return &cpapi.KillResponse{Status: "killed", Message: fmt.Sprintf("Agent %q killed.", req.Name)}, nil
}

func (m *mockClient) CreateObjective(ctx context.Context, req cpapi.CreateObjectiveRequest) (*cpapi.CreateObjectiveResponse, error) {
	if m.createObjFn != nil {
		return m.createObjFn(ctx, req)
	}
	return nil, fmt.Errorf("not implemented")
}

func (m *mockClient) ListObjectives(ctx context.Context, req cpapi.ListObjectivesRequest) (*cpapi.ListObjectivesResponse, error) {
	if m.listObjsFn != nil {
		return m.listObjsFn(ctx, req)
	}
	return nil, fmt.Errorf("not implemented")
}

func (m *mockClient) GetObjective(ctx context.Context, req cpapi.GetObjectiveRequest) (*cpapi.GetObjectiveResponse, error) {
	if m.getObjFn != nil {
		return m.getObjFn(ctx, req)
	}
	return nil, fmt.Errorf("not implemented")
}

func (m *mockClient) UnfreezeObjective(_ context.Context, _ cpapi.UnfreezeObjectiveRequest) (*cpapi.UnfreezeObjectiveResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockClient) Merge(_ context.Context, _ cpapi.MergeRequest) (*cpapi.MergeResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockClient) DryRunSpawn(_ context.Context, _ cpapi.DryRunRequest) (*cpapi.DryRunResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockClient) IngestEvents(_ context.Context, _ cpapi.IngestEventsRequest, _ string) (*cpapi.IngestEventsResponse, error) {
	return &cpapi.IngestEventsResponse{Accepted: 0}, nil
}

func (m *mockClient) QueryEvents(_ context.Context, _ cpapi.EventsQueryRequest) (*cpapi.EventsQueryResponse, error) {
	return &cpapi.EventsQueryResponse{}, nil
}

func (m *mockClient) GraphQuery(_ context.Context, _ cpapi.GraphQueryRequest) (*cpapi.GraphQueryResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockClient) GraphUpdate(_ context.Context, _ cpapi.GraphUpdateRequest) (*cpapi.GraphUpdateResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockClient) GraphSchema(_ context.Context, _ cpapi.GraphSchemaRequest) (*cpapi.GraphSchemaResponse, error) {
	return nil, fmt.Errorf("graph not configured")
}

func (m *mockClient) GraphPath(_ context.Context, _ cpapi.GraphPathRequest) (*cpapi.GraphPathResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockClient) GraphNeighbors(_ context.Context, _ cpapi.GraphNeighborsRequest) (*cpapi.GraphNeighborsResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

var _ cpapi.ControlPlaneClient = (*mockClient)(nil)

// TestToolRegistration verifies that all expected tools are registered.
func TestToolRegistration(t *testing.T) {
	srv := New(&mockClient{})

	// The MCPServer should have the tools we registered.
	// We verify by listing tools.
	ctx := context.Background()
	tools, err := toolNames(ctx, srv)
	require.NoError(t, err)

	expected := []string{
		"fracta_spawn",
		"fracta_list",
		"fracta_peek",
		"fracta_say",
		"fracta_kill",
		"fracta_logs",
		"fracta_get_agent",
		"fracta_get_mission",
		"fracta_create_objective",
		"fracta_list_objectives",
		"fracta_get_objective",
	}
	for _, name := range expected {
		assert.Contains(t, tools, name, "tool %q should be registered", name)
	}

	// Verify no gateway/agent tools leak into the host surface.
	gatewayOnly := []string{
		"fracta_send",    // agent-facing
		"fracta_inbox",   // agent-facing
		"fracta_init",    // admin init
		"fracta_merge",   // local-only
	}
	for _, name := range gatewayOnly {
		assert.NotContains(t, tools, name, "tool %q should NOT be on host surface", name)
	}
}

// TestSpawnDispatch verifies spawn routes through the client with correct parameters.
func TestSpawnDispatch(t *testing.T) {
	var captured cpapi.SpawnRequest
	mc := &mockClient{
		spawnFn: func(_ context.Context, req cpapi.SpawnRequest) (*cpapi.SpawnResponse, error) {
			captured = req
			return &cpapi.SpawnResponse{
				Agent:  req.Task,
				Branch: "feature/" + req.Task,
				Status: "running",
				Mode:   "batch",
			}, nil
		},
	}

	srv := New(mc)
	result, err := srv.handleSpawn(context.Background(), makeToolRequest(map[string]interface{}{
		"task":      "test-agent",
		"contract":  "do the thing",
		"model":     "opus",
		"tier":      "heavy",
		"runtime": "claude",
		"mode":      "batch",
		"dispatch":  "queued",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	assert.Equal(t, "test-agent", captured.Task)
	assert.Equal(t, "do the thing", captured.Contract)
	assert.Equal(t, "opus", captured.Model)
	assert.Equal(t, "heavy", captured.Tier)
	assert.Equal(t, "claude", captured.RuntimeType)
	assert.Equal(t, "batch", captured.Mode)
	assert.Equal(t, "queued", captured.Dispatch)

	// Verify response is valid JSON
	var resp cpapi.SpawnResponse
	require.NoError(t, json.Unmarshal([]byte(textContent(result)), &resp))
	assert.Equal(t, "test-agent", resp.Agent)
}

// TestSpawnMissingTask verifies spawn returns error without task.
func TestSpawnMissingTask(t *testing.T) {
	srv := New(&mockClient{})
	result, err := srv.handleSpawn(context.Background(), makeToolRequest(map[string]interface{}{}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

// TestListDispatch verifies list routes through the client.
func TestListDispatch(t *testing.T) {
	mc := &mockClient{
		listAgentsFn: func(_ context.Context, _ cpapi.ListAgentsRequest) (*cpapi.ListAgentsResponse, error) {
			return &cpapi.ListAgentsResponse{
				Agents: []cpapi.AgentInfo{
					{Name: "agent-1", Status: model.StatusRunning, Mode: "batch"},
					{Name: "agent-2", Status: model.StatusIdle, Mode: "stream"},
				},
			}, nil
		},
	}

	srv := New(mc)
	result, err := srv.handleList(context.Background(), mcp.CallToolRequest{})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var agents []cpapi.AgentInfo
	require.NoError(t, json.Unmarshal([]byte(textContent(result)), &agents))
	assert.Len(t, agents, 2)
	assert.Equal(t, "agent-1", agents[0].Name)
	assert.Equal(t, "agent-2", agents[1].Name)
}

// TestPeekDispatch verifies peek routes through the client.
func TestPeekDispatch(t *testing.T) {
	mc := &mockClient{
		peekFn: func(_ context.Context, req cpapi.PeekRequest) (*cpapi.PeekResponse, error) {
			return &cpapi.PeekResponse{Output: "hello from " + req.Name}, nil
		},
	}

	srv := New(mc)
	result, err := srv.handlePeek(context.Background(), makeToolRequest(map[string]interface{}{
		"name": "test-agent",
	}))
	require.NoError(t, err)
	assert.Equal(t, "hello from test-agent", textContent(result))
}

// TestPeekRawMode verifies peek passes mode through.
func TestPeekRawMode(t *testing.T) {
	var capturedMode string
	mc := &mockClient{
		peekFn: func(_ context.Context, req cpapi.PeekRequest) (*cpapi.PeekResponse, error) {
			capturedMode = req.Mode
			return &cpapi.PeekResponse{Output: "raw events"}, nil
		},
	}

	srv := New(mc)
	_, err := srv.handlePeek(context.Background(), makeToolRequest(map[string]interface{}{
		"name": "test-agent",
		"mode": "raw",
	}))
	require.NoError(t, err)
	assert.Equal(t, "raw", capturedMode)
}

// TestSayDispatch verifies say routes through the client.
func TestSayDispatch(t *testing.T) {
	var capturedReq cpapi.SayRequest
	mc := &mockClient{
		sayFn: func(_ context.Context, req cpapi.SayRequest) (*cpapi.SayResponse, error) {
			capturedReq = req
			return &cpapi.SayResponse{Status: "completed", Message: "response text"}, nil
		},
	}

	srv := New(mc)
	result, err := srv.handleSay(context.Background(), makeToolRequest(map[string]interface{}{
		"name":    "test-agent",
		"message": "do more",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "test-agent", capturedReq.Name)
	assert.Equal(t, "do more", capturedReq.Message)
	assert.Equal(t, "response text", textContent(result))
}

// TestSayMissingParams verifies say returns errors for missing params.
func TestSayMissingParams(t *testing.T) {
	srv := New(&mockClient{})

	// Missing name
	result, err := srv.handleSay(context.Background(), makeToolRequest(map[string]interface{}{
		"message": "hello",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)

	// Missing message
	result, err = srv.handleSay(context.Background(), makeToolRequest(map[string]interface{}{
		"name": "test",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

// TestKillDispatch verifies kill routes through the client.
func TestKillDispatch(t *testing.T) {
	var capturedReq cpapi.KillRequest
	mc := &mockClient{
		killFn: func(_ context.Context, req cpapi.KillRequest) (*cpapi.KillResponse, error) {
			capturedReq = req
			return &cpapi.KillResponse{Status: "killed", Message: "done"}, nil
		},
	}

	srv := New(mc)
	result, err := srv.handleKill(context.Background(), makeToolRequest(map[string]interface{}{
		"name":       "test-agent",
		"keep_files": true,
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "test-agent", capturedReq.Name)
	assert.True(t, capturedReq.KeepFiles)
}

// TestLogsDispatch verifies logs routes through the client.
func TestLogsDispatch(t *testing.T) {
	var capturedReq cpapi.GetLogsRequest
	mc := &mockClient{
		getLogsFn: func(_ context.Context, req cpapi.GetLogsRequest) (*cpapi.GetLogsResponse, error) {
			capturedReq = req
			return &cpapi.GetLogsResponse{Output: "log line 1\nlog line 2"}, nil
		},
	}

	srv := New(mc)
	result, err := srv.handleLogs(context.Background(), makeToolRequest(map[string]interface{}{
		"task":  "test-agent",
		"lines": float64(50), // JSON numbers are float64
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "test-agent", capturedReq.Task)
	assert.Equal(t, 50, capturedReq.Lines)
	assert.Equal(t, "log line 1\nlog line 2", textContent(result))
}

// TestLogsEmpty verifies empty logs return "No logs available."
func TestLogsEmpty(t *testing.T) {
	mc := &mockClient{
		getLogsFn: func(_ context.Context, _ cpapi.GetLogsRequest) (*cpapi.GetLogsResponse, error) {
			return &cpapi.GetLogsResponse{Output: ""}, nil
		},
	}

	srv := New(mc)
	result, err := srv.handleLogs(context.Background(), makeToolRequest(map[string]interface{}{
		"task": "test-agent",
	}))
	require.NoError(t, err)
	assert.Equal(t, "No logs available.", textContent(result))
}

// TestSpawnError verifies client errors are surfaced as tool errors.
func TestSpawnError(t *testing.T) {
	mc := &mockClient{
		spawnFn: func(_ context.Context, _ cpapi.SpawnRequest) (*cpapi.SpawnResponse, error) {
			return nil, fmt.Errorf("agent already exists")
		},
	}

	srv := New(mc)
	result, err := srv.handleSpawn(context.Background(), makeToolRequest(map[string]interface{}{
		"task": "dup-agent",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, textContent(result), "agent already exists")
}

// TestServerName verifies the MCP server has the correct name.
func TestServerName(t *testing.T) {
	srv := New(&mockClient{})
	assert.NotNil(t, srv.MCPServer())
}

// TestGetAgentDispatch verifies get-agent routes through the client.
func TestGetAgentDispatch(t *testing.T) {
	mc := &mockClient{
		getAgentFn: func(_ context.Context, req cpapi.GetAgentRequest) (*cpapi.GetAgentResponse, error) {
			return &cpapi.GetAgentResponse{
				Agent: cpapi.AgentInfo{
					Name:   req.Name,
					Status: model.StatusRunning,
					Mode:   "batch",
				},
			}, nil
		},
	}

	srv := New(mc)
	result, err := srv.handleGetAgent(context.Background(), makeToolRequest(map[string]interface{}{
		"name": "my-agent",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp cpapi.GetAgentResponse
	require.NoError(t, json.Unmarshal([]byte(textContent(result)), &resp))
	assert.Equal(t, "my-agent", resp.Agent.Name)
	assert.Equal(t, model.StatusRunning, resp.Agent.Status)
}

// TestGetAgentMissingName verifies get-agent returns error without name.
func TestGetAgentMissingName(t *testing.T) {
	srv := New(&mockClient{})
	result, err := srv.handleGetAgent(context.Background(), makeToolRequest(map[string]interface{}{}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

// TestGetMissionDispatch verifies get-mission routes through the client.
func TestGetMissionDispatch(t *testing.T) {
	mc := &mockClient{
		getMissionFn: func(_ context.Context, req cpapi.GetMissionRequest) (*cpapi.GetMissionResponse, error) {
			return &cpapi.GetMissionResponse{
				Mission: cpapi.MissionInfo{
					AgentTask: req.Name,
					MissionID: 42,
					Status:    "running",
				},
			}, nil
		},
	}

	srv := New(mc)
	result, err := srv.handleGetMission(context.Background(), makeToolRequest(map[string]interface{}{
		"name": "queued-agent",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp cpapi.GetMissionResponse
	require.NoError(t, json.Unmarshal([]byte(textContent(result)), &resp))
	assert.Equal(t, "queued-agent", resp.Mission.AgentTask)
	assert.Equal(t, int64(42), resp.Mission.MissionID)
}

// TestCreateObjectiveDispatch verifies create-objective routes through the client.
func TestCreateObjectiveDispatch(t *testing.T) {
	var captured cpapi.CreateObjectiveRequest
	mc := &mockClient{
		createObjFn: func(_ context.Context, req cpapi.CreateObjectiveRequest) (*cpapi.CreateObjectiveResponse, error) {
			captured = req
			return &cpapi.CreateObjectiveResponse{
				Objective: cpapi.ObjectiveInfo{ID: "obj-123", Status: "open"},
			}, nil
		},
	}

	srv := New(mc)
	result, err := srv.handleCreateObjective(context.Background(), makeToolRequest(map[string]interface{}{
		"description": "investigate lateral movement",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "investigate lateral movement", captured.Description)

	var resp cpapi.CreateObjectiveResponse
	require.NoError(t, json.Unmarshal([]byte(textContent(result)), &resp))
	assert.Equal(t, "obj-123", resp.Objective.ID)
}

// TestListObjectivesDispatch verifies list-objectives routes through the client.
func TestListObjectivesDispatch(t *testing.T) {
	mc := &mockClient{
		listObjsFn: func(_ context.Context, req cpapi.ListObjectivesRequest) (*cpapi.ListObjectivesResponse, error) {
			return &cpapi.ListObjectivesResponse{
				Objectives: []cpapi.ObjectiveInfo{
					{ID: "obj-1", Description: "first", Status: "open"},
					{ID: "obj-2", Description: "second", Status: "answered"},
				},
			}, nil
		},
	}

	srv := New(mc)
	result, err := srv.handleListObjectives(context.Background(), makeToolRequest(map[string]interface{}{
		"status": "open",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp cpapi.ListObjectivesResponse
	require.NoError(t, json.Unmarshal([]byte(textContent(result)), &resp))
	assert.Len(t, resp.Objectives, 2)
}

// TestGetObjectiveDispatch verifies get-objective routes through the client.
func TestGetObjectiveDispatch(t *testing.T) {
	mc := &mockClient{
		getObjFn: func(_ context.Context, req cpapi.GetObjectiveRequest) (*cpapi.GetObjectiveResponse, error) {
			return &cpapi.GetObjectiveResponse{
				Objective: cpapi.ObjectiveInfo{
					ID:          req.ID,
					Description: "investigate C2 traffic",
					Status:      "open",
				},
			}, nil
		},
	}

	srv := New(mc)
	result, err := srv.handleGetObjective(context.Background(), makeToolRequest(map[string]interface{}{
		"id": "obj-456",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var resp cpapi.GetObjectiveResponse
	require.NoError(t, json.Unmarshal([]byte(textContent(result)), &resp))
	assert.Equal(t, "obj-456", resp.Objective.ID)
	assert.Equal(t, "investigate C2 traffic", resp.Objective.Description)
}

// TestGetObjectiveMissingID verifies get-objective returns error without id.
func TestGetObjectiveMissingID(t *testing.T) {
	srv := New(&mockClient{})
	result, err := srv.handleGetObjective(context.Background(), makeToolRequest(map[string]interface{}{}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

// --- helpers ---

// toolNames lists all registered tool names on the server.
func toolNames(_ context.Context, srv *Server) ([]string, error) {
	tools := srv.mcp.ListTools()
	var names []string
	for name := range tools {
		names = append(names, name)
	}
	return names, nil
}

// makeToolRequest builds a CallToolRequest with the given parameters.
func makeToolRequest(params map[string]interface{}) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: params,
		},
	}
}

// textContent extracts the text content from a CallToolResult.
func textContent(r *mcp.CallToolResult) string {
	for _, c := range r.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}
