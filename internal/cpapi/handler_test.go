package cpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/objective"
)

// mockClient implements ControlPlaneClient for handler tests.
type mockClient struct {
	spawnResp      *SpawnResponse
	spawnErr       error
	listAgentsResp *ListAgentsResponse
	listAgentsErr  error
	getAgentResp   *GetAgentResponse
	getAgentErr    error
	getMissionResp *GetMissionResponse
	getMissionErr  error
	peekResp       *PeekResponse
	peekErr        error
	getLogsResp    *GetLogsResponse
	getLogsErr     error
	sayResp        *SayResponse
	sayErr         error
	killResp       *KillResponse
	killErr        error
	createObjResp  *CreateObjectiveResponse
	createObjErr   error
	listObjResp    *ListObjectivesResponse
	listObjErr     error
	getObjResp     *GetObjectiveResponse
	getObjErr      error

	// Capture requests for assertion.
	lastSpawnReq     SpawnRequest
	lastSayReq       SayRequest
	lastKillReq      KillRequest
	lastPeekReq      PeekRequest
	lastGetLogsReq   GetLogsRequest
	lastCreateObjReq CreateObjectiveRequest
	lastListObjReq   ListObjectivesRequest
}

func (m *mockClient) Spawn(_ context.Context, req SpawnRequest) (*SpawnResponse, error) {
	m.lastSpawnReq = req
	return m.spawnResp, m.spawnErr
}
func (m *mockClient) ListAgents(_ context.Context, _ ListAgentsRequest) (*ListAgentsResponse, error) {
	return m.listAgentsResp, m.listAgentsErr
}
func (m *mockClient) GetAgent(_ context.Context, req GetAgentRequest) (*GetAgentResponse, error) {
	return m.getAgentResp, m.getAgentErr
}
func (m *mockClient) GetMission(_ context.Context, req GetMissionRequest) (*GetMissionResponse, error) {
	return m.getMissionResp, m.getMissionErr
}
func (m *mockClient) Peek(_ context.Context, req PeekRequest) (*PeekResponse, error) {
	m.lastPeekReq = req
	return m.peekResp, m.peekErr
}
func (m *mockClient) GetLogs(_ context.Context, req GetLogsRequest) (*GetLogsResponse, error) {
	m.lastGetLogsReq = req
	return m.getLogsResp, m.getLogsErr
}
func (m *mockClient) Say(_ context.Context, req SayRequest) (*SayResponse, error) {
	m.lastSayReq = req
	return m.sayResp, m.sayErr
}
func (m *mockClient) Kill(_ context.Context, req KillRequest) (*KillResponse, error) {
	m.lastKillReq = req
	return m.killResp, m.killErr
}
func (m *mockClient) CreateObjective(_ context.Context, req CreateObjectiveRequest) (*CreateObjectiveResponse, error) {
	m.lastCreateObjReq = req
	return m.createObjResp, m.createObjErr
}
func (m *mockClient) ListObjectives(_ context.Context, req ListObjectivesRequest) (*ListObjectivesResponse, error) {
	m.lastListObjReq = req
	return m.listObjResp, m.listObjErr
}
func (m *mockClient) GetObjective(_ context.Context, req GetObjectiveRequest) (*GetObjectiveResponse, error) {
	return m.getObjResp, m.getObjErr
}
func (m *mockClient) UnfreezeObjective(_ context.Context, req UnfreezeObjectiveRequest) (*UnfreezeObjectiveResponse, error) {
	return &UnfreezeObjectiveResponse{}, nil
}
func (m *mockClient) Merge(_ context.Context, req MergeRequest) (*MergeResponse, error) {
	return &MergeResponse{Status: "merged", Message: "merged"}, nil
}
func (m *mockClient) DryRunSpawn(_ context.Context, _ DryRunRequest) (*DryRunResponse, error) {
	return &DryRunResponse{}, nil
}
func (m *mockClient) IngestEvents(_ context.Context, _ IngestEventsRequest, _ string) (*IngestEventsResponse, error) {
	return &IngestEventsResponse{}, nil
}
func (m *mockClient) QueryEvents(_ context.Context, _ EventsQueryRequest) (*EventsQueryResponse, error) {
	return &EventsQueryResponse{Events: []EventInfo{}}, nil
}
func (m *mockClient) GraphQuery(_ context.Context, _ GraphQueryRequest) (*GraphQueryResponse, error) {
	return &GraphQueryResponse{}, nil
}
func (m *mockClient) GraphUpdate(_ context.Context, _ GraphUpdateRequest) (*GraphUpdateResponse, error) {
	return &GraphUpdateResponse{Status: "ok"}, nil
}
func (m *mockClient) GraphSchema(_ context.Context, _ GraphSchemaRequest) (*GraphSchemaResponse, error) {
	return &GraphSchemaResponse{}, nil
}
func (m *mockClient) GraphPath(_ context.Context, _ GraphPathRequest) (*GraphPathResponse, error) {
	return &GraphPathResponse{}, nil
}
func (m *mockClient) GraphNeighbors(_ context.Context, _ GraphNeighborsRequest) (*GraphNeighborsResponse, error) {
	return &GraphNeighborsResponse{}, nil
}

func newTestServer(mc *mockClient) *httptest.Server {
	srv := NewHTTPServer(":0", mc)
	return httptest.NewServer(srv.Handler())
}

func TestHandler_Healthz(t *testing.T) {
	ts := newTestServer(&mockClient{})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	assert.Equal(t, "ok", body["status"])
}

func TestHandler_Spawn(t *testing.T) {
	mc := &mockClient{
		spawnResp: &SpawnResponse{Agent: "test-agent", Status: "running", Mode: "batch"},
	}
	ts := newTestServer(mc)
	defer ts.Close()

	body, _ := json.Marshal(SpawnRequest{Task: "test-agent", Contract: "do stuff"})
	resp, err := http.Post(ts.URL+"/api/v1/agents", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "test-agent", mc.lastSpawnReq.Task)

	var result SpawnResponse
	json.NewDecoder(resp.Body).Decode(&result)
	assert.Equal(t, "test-agent", result.Agent)
}

func TestHandler_Spawn_MissingTask(t *testing.T) {
	ts := newTestServer(&mockClient{})
	defer ts.Close()

	body, _ := json.Marshal(SpawnRequest{})
	resp, err := http.Post(ts.URL+"/api/v1/agents", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHandler_Spawn_Error(t *testing.T) {
	mc := &mockClient{spawnErr: fmt.Errorf("queue not configured")}
	ts := newTestServer(mc)
	defer ts.Close()

	body, _ := json.Marshal(SpawnRequest{Task: "test"})
	resp, err := http.Post(ts.URL+"/api/v1/agents", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestHandler_ListAgents(t *testing.T) {
	mc := &mockClient{
		listAgentsResp: &ListAgentsResponse{
			Agents: []AgentInfo{
				{Name: "a1", Status: model.StatusRunning, Mode: "batch"},
			},
		},
	}
	ts := newTestServer(mc)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/agents")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result ListAgentsResponse
	json.NewDecoder(resp.Body).Decode(&result)
	assert.Len(t, result.Agents, 1)
	assert.Equal(t, "a1", result.Agents[0].Name)
}

func TestHandler_GetAgent(t *testing.T) {
	mc := &mockClient{
		getAgentResp: &GetAgentResponse{
			Agent: AgentInfo{Name: "a1", Status: model.StatusCompleted},
		},
	}
	ts := newTestServer(mc)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/agents/a1")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHandler_GetAgent_NotFound(t *testing.T) {
	mc := &mockClient{getAgentErr: fmt.Errorf("agent not found")}
	ts := newTestServer(mc)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/agents/nonexistent")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandler_Kill(t *testing.T) {
	mc := &mockClient{
		killResp: &KillResponse{Status: "killed", Message: "Agent killed."},
	}
	ts := newTestServer(mc)
	defer ts.Close()

	req, _ := http.NewRequest("DELETE", ts.URL+"/api/v1/agents/a1", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "a1", mc.lastKillReq.Name)
}

func TestHandler_Say(t *testing.T) {
	mc := &mockClient{
		sayResp: &SayResponse{Status: "dispatched"},
	}
	ts := newTestServer(mc)
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{"message": "hello"})
	resp, err := http.Post(ts.URL+"/api/v1/agents/a1/say", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "a1", mc.lastSayReq.Name)
	assert.Equal(t, "hello", mc.lastSayReq.Message)
}

func TestHandler_Say_MissingMessage(t *testing.T) {
	ts := newTestServer(&mockClient{})
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{})
	resp, err := http.Post(ts.URL+"/api/v1/agents/a1/say", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHandler_Peek(t *testing.T) {
	mc := &mockClient{
		peekResp: &PeekResponse{Output: "agent output"},
	}
	ts := newTestServer(mc)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/agents/a1/peek?mode=raw")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "raw", mc.lastPeekReq.Mode)
}

func TestHandler_GetLogs(t *testing.T) {
	mc := &mockClient{
		getLogsResp: &GetLogsResponse{Output: "log line 1\nlog line 2"},
	}
	ts := newTestServer(mc)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/agents/a1/logs?lines=50")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 50, mc.lastGetLogsReq.Lines)
}

func TestHandler_GetMission(t *testing.T) {
	mc := &mockClient{
		getMissionResp: &GetMissionResponse{
			Mission: MissionInfo{MissionID: 42, AgentTask: "a1", Status: "pending"},
		},
	}
	ts := newTestServer(mc)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/agents/a1/mission")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHandler_CreateObjective(t *testing.T) {
	mc := &mockClient{
		createObjResp: &CreateObjectiveResponse{
			Objective: ObjectiveInfo{
				ID: "obj-1", Status: objective.StatusOpen, Description: "test",
			},
		},
	}
	ts := newTestServer(mc)
	defer ts.Close()

	body, _ := json.Marshal(CreateObjectiveRequest{Description: "test"})
	resp, err := http.Post(ts.URL+"/api/v1/objectives", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "test", mc.lastCreateObjReq.Description)
}

func TestHandler_CreateObjective_MissingDescription(t *testing.T) {
	ts := newTestServer(&mockClient{})
	defer ts.Close()

	body, _ := json.Marshal(CreateObjectiveRequest{})
	resp, err := http.Post(ts.URL+"/api/v1/objectives", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHandler_ListObjectives(t *testing.T) {
	mc := &mockClient{
		listObjResp: &ListObjectivesResponse{
			Objectives: []ObjectiveInfo{{ID: "obj-1", Status: objective.StatusOpen}},
		},
	}
	ts := newTestServer(mc)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/objectives?status=open")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "open", mc.lastListObjReq.Status)
}

func TestHandler_GetObjective(t *testing.T) {
	mc := &mockClient{
		getObjResp: &GetObjectiveResponse{
			Objective: ObjectiveInfo{ID: "obj-1", Description: "test"},
		},
	}
	ts := newTestServer(mc)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/objectives/obj-1")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHandler_GetObjective_NotFound(t *testing.T) {
	mc := &mockClient{getObjErr: fmt.Errorf("not found")}
	ts := newTestServer(mc)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/objectives/nonexistent")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
