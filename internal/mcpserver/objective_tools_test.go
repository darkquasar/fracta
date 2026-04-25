package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/darkquasar/fracta/internal/objective"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// objectiveMockClient captures calls to objective methods for assertion.
type objectiveMockClient struct {
	noopCPClient

	createResp   *cpapi.CreateObjectiveResponse
	createErr    error
	listResp     *cpapi.ListObjectivesResponse
	listErr      error
	unfreezeResp *cpapi.UnfreezeObjectiveResponse
	unfreezeErr  error

	lastCreateReq   cpapi.CreateObjectiveRequest
	lastListReq     cpapi.ListObjectivesRequest
	lastUnfreezeReq cpapi.UnfreezeObjectiveRequest
	createCalled    bool
	listCalled      bool
	unfreezeCalled  bool
}

func (m *objectiveMockClient) CreateObjective(_ context.Context, req cpapi.CreateObjectiveRequest) (*cpapi.CreateObjectiveResponse, error) {
	m.createCalled = true
	m.lastCreateReq = req
	return m.createResp, m.createErr
}
func (m *objectiveMockClient) ListObjectives(_ context.Context, req cpapi.ListObjectivesRequest) (*cpapi.ListObjectivesResponse, error) {
	m.listCalled = true
	m.lastListReq = req
	return m.listResp, m.listErr
}
func (m *objectiveMockClient) UnfreezeObjective(_ context.Context, req cpapi.UnfreezeObjectiveRequest) (*cpapi.UnfreezeObjectiveResponse, error) {
	m.unfreezeCalled = true
	m.lastUnfreezeReq = req
	return m.unfreezeResp, m.unfreezeErr
}

// noopCPClient satisfies ControlPlaneClient with no-op stubs for non-objective methods.
type noopCPClient struct{}

func (n noopCPClient) Spawn(_ context.Context, _ cpapi.SpawnRequest) (*cpapi.SpawnResponse, error) {
	return nil, nil
}
func (n noopCPClient) ListAgents(_ context.Context, _ cpapi.ListAgentsRequest) (*cpapi.ListAgentsResponse, error) {
	return nil, nil
}
func (n noopCPClient) GetAgent(_ context.Context, _ cpapi.GetAgentRequest) (*cpapi.GetAgentResponse, error) {
	return nil, nil
}
func (n noopCPClient) GetMission(_ context.Context, _ cpapi.GetMissionRequest) (*cpapi.GetMissionResponse, error) {
	return nil, nil
}
func (n noopCPClient) Peek(_ context.Context, _ cpapi.PeekRequest) (*cpapi.PeekResponse, error) {
	return nil, nil
}
func (n noopCPClient) GetLogs(_ context.Context, _ cpapi.GetLogsRequest) (*cpapi.GetLogsResponse, error) {
	return nil, nil
}
func (n noopCPClient) Say(_ context.Context, _ cpapi.SayRequest) (*cpapi.SayResponse, error) {
	return nil, nil
}
func (n noopCPClient) Kill(_ context.Context, _ cpapi.KillRequest) (*cpapi.KillResponse, error) {
	return nil, nil
}
func (n noopCPClient) Merge(_ context.Context, _ cpapi.MergeRequest) (*cpapi.MergeResponse, error) {
	return nil, nil
}
func (n noopCPClient) DryRunSpawn(_ context.Context, _ cpapi.DryRunRequest) (*cpapi.DryRunResponse, error) {
	return nil, nil
}
func (n noopCPClient) CreateObjective(_ context.Context, _ cpapi.CreateObjectiveRequest) (*cpapi.CreateObjectiveResponse, error) {
	return nil, nil
}
func (n noopCPClient) ListObjectives(_ context.Context, _ cpapi.ListObjectivesRequest) (*cpapi.ListObjectivesResponse, error) {
	return nil, nil
}
func (n noopCPClient) GetObjective(_ context.Context, _ cpapi.GetObjectiveRequest) (*cpapi.GetObjectiveResponse, error) {
	return nil, nil
}
func (n noopCPClient) UnfreezeObjective(_ context.Context, _ cpapi.UnfreezeObjectiveRequest) (*cpapi.UnfreezeObjectiveResponse, error) {
	return nil, nil
}
func (n noopCPClient) IngestEvents(_ context.Context, _ cpapi.IngestEventsRequest, _ string) (*cpapi.IngestEventsResponse, error) {
	return &cpapi.IngestEventsResponse{}, nil
}
func (n noopCPClient) QueryEvents(_ context.Context, _ cpapi.EventsQueryRequest) (*cpapi.EventsQueryResponse, error) {
	return &cpapi.EventsQueryResponse{}, nil
}
func (n noopCPClient) GraphQuery(_ context.Context, _ cpapi.GraphQueryRequest) (*cpapi.GraphQueryResponse, error) {
	return nil, nil
}
func (n noopCPClient) GraphUpdate(_ context.Context, _ cpapi.GraphUpdateRequest) (*cpapi.GraphUpdateResponse, error) {
	return nil, nil
}
func (n noopCPClient) GraphSchema(_ context.Context, _ cpapi.GraphSchemaRequest) (*cpapi.GraphSchemaResponse, error) {
	return nil, fmt.Errorf("graph not configured")
}
func (n noopCPClient) GraphPath(_ context.Context, _ cpapi.GraphPathRequest) (*cpapi.GraphPathResponse, error) {
	return nil, nil
}
func (n noopCPClient) GraphNeighbors(_ context.Context, _ cpapi.GraphNeighborsRequest) (*cpapi.GraphNeighborsResponse, error) {
	return nil, nil
}

// TestObjectiveTools_RegisteredWithCPClient verifies that objective tools appear
// when the server is constructed with only WithControlPlaneClient (no WithObjectiveStore).
// This is the exact bug that prevented thin-client/K8s mode from exposing objectives.
func TestObjectiveTools_RegisteredWithCPClient(t *testing.T) {
	mock := &objectiveMockClient{}
	srv := New("/tmp/test-root", WithControlPlaneClient(mock))

	tools := srv.mcp.ListTools()

	expectedTools := []string{
		"fracta_create_objective",
		"fracta_list_objectives",
		"fracta_unfreeze_objective",
	}
	for _, name := range expectedTools {
		_, ok := tools[name]
		assert.True(t, ok, "expected tool %q to be registered", name)
	}
}

// TestObjectiveTools_NotRegisteredWithoutCPClient verifies that objective tools
// are NOT registered when no cpClient is provided.
func TestObjectiveTools_NotRegisteredWithoutCPClient(t *testing.T) {
	srv := New("/tmp/test-root")

	tools := srv.mcp.ListTools()

	absentTools := []string{
		"fracta_create_objective",
		"fracta_list_objectives",
		"fracta_unfreeze_objective",
	}
	for _, name := range absentTools {
		_, ok := tools[name]
		assert.False(t, ok, "tool %q should NOT be registered without cpClient", name)
	}
}

// TestObjectiveTools_CreateRoutesToCPClient verifies that handleCreateObjective
// forwards the request to cpClient.CreateObjective with correct parameters.
func TestObjectiveTools_CreateRoutesToCPClient(t *testing.T) {
	mock := &objectiveMockClient{
		createResp: &cpapi.CreateObjectiveResponse{
			Objective: cpapi.ObjectiveInfo{
				ID:          "test-123",
				Description: "hunt for APT29",
				Status:      objective.StatusOpen,
			},
		},
	}
	srv := New("/tmp/test-root", WithControlPlaneClient(mock))

	result, err := srv.handleCreateObjective(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{
				"description":  "hunt for APT29",
				"max_missions": float64(50),
				"max_depth":    float64(3),
			},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError, "expected success, got error: %v", result)

	// Verify cpClient was called with the right request.
	assert.True(t, mock.createCalled, "cpClient.CreateObjective was not called")
	assert.Equal(t, "hunt for APT29", mock.lastCreateReq.Description)
	assert.Equal(t, 50, mock.lastCreateReq.MaxMissions)
	assert.Equal(t, 3, mock.lastCreateReq.MaxDepth)

	// Verify response contains the objective info.
	var resp cpapi.CreateObjectiveResponse
	text := extractText(result)
	require.NoError(t, json.Unmarshal([]byte(text), &resp))
	assert.Equal(t, "test-123", resp.Objective.ID)
}

// TestObjectiveTools_CreatePassesIDAndMaxRuntime verifies that the restored id and
// max_runtime fields are forwarded from the MCP tool to cpClient.CreateObjective.
func TestObjectiveTools_CreatePassesIDAndMaxRuntime(t *testing.T) {
	mock := &objectiveMockClient{
		createResp: &cpapi.CreateObjectiveResponse{
			Objective: cpapi.ObjectiveInfo{
				ID:     "custom-id",
				Status: objective.StatusOpen,
			},
		},
	}
	srv := New("/tmp/test-root", WithControlPlaneClient(mock))

	result, err := srv.handleCreateObjective(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{
				"description": "test objective",
				"id":          "custom-id",
				"max_runtime": "2h30m",
			},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	assert.True(t, mock.createCalled)
	assert.Equal(t, "custom-id", mock.lastCreateReq.ID)
	assert.Equal(t, "2h30m", mock.lastCreateReq.MaxRuntime)
}

// TestObjectiveTools_ListRoutesToCPClient verifies handleListObjectives routes through cpClient.
func TestObjectiveTools_ListRoutesToCPClient(t *testing.T) {
	mock := &objectiveMockClient{
		listResp: &cpapi.ListObjectivesResponse{
			Objectives: []cpapi.ObjectiveInfo{
				{ID: "obj-1", Status: objective.StatusOpen},
			},
		},
	}
	srv := New("/tmp/test-root", WithControlPlaneClient(mock))

	result, err := srv.handleListObjectives(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{
				"status": "open",
			},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	assert.True(t, mock.listCalled)
	assert.Equal(t, "open", mock.lastListReq.Status)
}

// TestObjectiveTools_UnfreezeRoutesToCPClient verifies handleUnfreezeObjective routes through cpClient.
func TestObjectiveTools_UnfreezeRoutesToCPClient(t *testing.T) {
	mock := &objectiveMockClient{
		unfreezeResp: &cpapi.UnfreezeObjectiveResponse{
			Objective: cpapi.ObjectiveInfo{
				ID:     "frozen-obj",
				Status: objective.StatusOpen,
			},
		},
	}
	srv := New("/tmp/test-root", WithControlPlaneClient(mock))

	result, err := srv.handleUnfreezeObjective(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{
				"id": "frozen-obj",
			},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	assert.True(t, mock.unfreezeCalled)
	assert.Equal(t, "frozen-obj", mock.lastUnfreezeReq.ID)
}
