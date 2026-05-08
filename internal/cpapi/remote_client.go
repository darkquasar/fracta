package cpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Compile-time interface satisfaction check.
var _ ControlPlaneClient = (*RemoteControlPlaneClient)(nil)

// RemoteControlPlaneClient implements ControlPlaneClient by calling the
// in-cluster control-plane HTTP API. It normalizes responses into the same
// types as LocalControlPlaneClient, making both interchangeable.
type RemoteControlPlaneClient struct {
	baseURL    string
	httpClient *http.Client
}

// RemoteClientOption configures a RemoteControlPlaneClient.
type RemoteClientOption func(*RemoteControlPlaneClient)

// WithHTTPClient sets a custom HTTP client (e.g. for timeouts or transport).
func WithHTTPClient(c *http.Client) RemoteClientOption {
	return func(r *RemoteControlPlaneClient) { r.httpClient = c }
}

// NewRemoteControlPlaneClient creates a RemoteControlPlaneClient targeting the given base URL.
// The baseURL should include scheme and host (e.g. "http://localhost:8090").
func NewRemoteControlPlaneClient(baseURL string, opts ...RemoteClientOption) *RemoteControlPlaneClient {
	c := &RemoteControlPlaneClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Validate checks that the remote control-plane API is reachable by hitting /healthz.
// Call this at startup to fail fast rather than discovering unreachable APIs on first command.
func (c *RemoteControlPlaneClient) Validate(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/healthz", nil)
	if err != nil {
		return fmt.Errorf("remote control-plane validation: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("remote control-plane unreachable at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("remote control-plane unhealthy at %s: status %d", c.baseURL, resp.StatusCode)
	}
	return nil
}

func (c *RemoteControlPlaneClient) Spawn(ctx context.Context, req SpawnRequest) (*SpawnResponse, error) {
	var resp SpawnResponse
	if err := c.postJSON(ctx, "/api/v1/agents", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) ListAgents(ctx context.Context, _ ListAgentsRequest) (*ListAgentsResponse, error) {
	var resp ListAgentsResponse
	if err := c.getJSON(ctx, "/api/v1/agents", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) GetAgent(ctx context.Context, req GetAgentRequest) (*GetAgentResponse, error) {
	var resp GetAgentResponse
	if err := c.getJSON(ctx, "/api/v1/agents/"+url.PathEscape(req.Name), nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) GetMission(ctx context.Context, req GetMissionRequest) (*GetMissionResponse, error) {
	var resp GetMissionResponse
	if err := c.getJSON(ctx, "/api/v1/agents/"+url.PathEscape(req.Name)+"/mission", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) Peek(ctx context.Context, req PeekRequest) (*PeekResponse, error) {
	params := url.Values{}
	if req.Mode != "" {
		params.Set("mode", req.Mode)
	}
	var resp PeekResponse
	if err := c.getJSON(ctx, "/api/v1/agents/"+url.PathEscape(req.Name)+"/peek", params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) GetLogs(ctx context.Context, req GetLogsRequest) (*GetLogsResponse, error) {
	params := url.Values{}
	if req.Lines > 0 {
		params.Set("lines", fmt.Sprintf("%d", req.Lines))
	}
	var resp GetLogsResponse
	if err := c.getJSON(ctx, "/api/v1/agents/"+url.PathEscape(req.Task)+"/logs", params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) Say(ctx context.Context, req SayRequest) (*SayResponse, error) {
	body := map[string]string{"message": req.Message}
	var resp SayResponse
	if err := c.postJSON(ctx, "/api/v1/agents/"+url.PathEscape(req.Name)+"/say", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) Kill(ctx context.Context, req KillRequest) (*KillResponse, error) {
	body := map[string]bool{"keep_files": req.KeepFiles}
	var resp KillResponse
	if err := c.deleteJSON(ctx, "/api/v1/agents/"+url.PathEscape(req.Name), body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) Merge(ctx context.Context, req MergeRequest) (*MergeResponse, error) {
	var resp MergeResponse
	if err := c.postJSON(ctx, "/api/v1/agents/"+url.PathEscape(req.Name)+"/merge", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) DryRunSpawn(ctx context.Context, req DryRunRequest) (*DryRunResponse, error) {
	var resp DryRunResponse
	if err := c.postJSON(ctx, "/api/v1/agents/dry-run", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) CreateObjective(ctx context.Context, req CreateObjectiveRequest) (*CreateObjectiveResponse, error) {
	var resp CreateObjectiveResponse
	if err := c.postJSON(ctx, "/api/v1/objectives", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) ListObjectives(ctx context.Context, req ListObjectivesRequest) (*ListObjectivesResponse, error) {
	params := url.Values{}
	if req.Status != "" {
		params.Set("status", req.Status)
	}
	var resp ListObjectivesResponse
	if err := c.getJSON(ctx, "/api/v1/objectives", params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) GetObjective(ctx context.Context, req GetObjectiveRequest) (*GetObjectiveResponse, error) {
	var resp GetObjectiveResponse
	if err := c.getJSON(ctx, "/api/v1/objectives/"+url.PathEscape(req.ID), nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) UnfreezeObjective(ctx context.Context, req UnfreezeObjectiveRequest) (*UnfreezeObjectiveResponse, error) {
	var resp UnfreezeObjectiveResponse
	if err := c.postJSON(ctx, "/api/v1/objectives/"+url.PathEscape(req.ID)+"/unfreeze", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) IngestEvents(ctx context.Context, req IngestEventsRequest, task string) (*IngestEventsResponse, error) {
	var resp IngestEventsResponse
	if err := c.postJSON(ctx, "/api/v1/agents/"+url.PathEscape(task)+"/events", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) QueryEvents(ctx context.Context, req EventsQueryRequest) (*EventsQueryResponse, error) {
	params := url.Values{}
	if req.Last > 0 {
		params.Set("last", fmt.Sprintf("%d", req.Last))
	}
	if req.Since != "" {
		params.Set("since", req.Since)
	}
	var resp EventsQueryResponse
	if err := c.getJSON(ctx, "/api/v1/agents/"+url.PathEscape(req.Task)+"/events", params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) GraphQuery(ctx context.Context, req GraphQueryRequest) (*GraphQueryResponse, error) {
	var resp GraphQueryResponse
	if err := c.postJSON(ctx, "/api/v1/graph/query", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) GraphUpdate(ctx context.Context, req GraphUpdateRequest) (*GraphUpdateResponse, error) {
	var resp GraphUpdateResponse
	if err := c.postJSON(ctx, "/api/v1/graph/update", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) GraphSchema(ctx context.Context, req GraphSchemaRequest) (*GraphSchemaResponse, error) {
	var resp GraphSchemaResponse
	if err := c.postJSON(ctx, "/api/v1/graph/schema", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) GraphPath(ctx context.Context, req GraphPathRequest) (*GraphPathResponse, error) {
	var resp GraphPathResponse
	if err := c.postJSON(ctx, "/api/v1/graph/path", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RemoteControlPlaneClient) GraphNeighbors(ctx context.Context, req GraphNeighborsRequest) (*GraphNeighborsResponse, error) {
	var resp GraphNeighborsResponse
	if err := c.postJSON(ctx, "/api/v1/graph/neighbors", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// --- HTTP helpers ---

func (c *RemoteControlPlaneClient) getJSON(ctx context.Context, path string, params url.Values, out interface{}) error {
	u := c.baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	return c.doJSON(req, out)
}

func (c *RemoteControlPlaneClient) postJSON(ctx context.Context, path string, body interface{}, out interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshalling request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doJSON(req, out)
}

func (c *RemoteControlPlaneClient) deleteJSON(ctx context.Context, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshalling request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, "DELETE", c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.doJSON(req, out)
}

func (c *RemoteControlPlaneClient) doJSON(req *http.Request, out interface{}) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return fmt.Errorf("server error (%d): %s", resp.StatusCode, errResp.Error)
		}
		return fmt.Errorf("server error (%d): %s", resp.StatusCode, string(body))
	}

	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
	}
	return nil
}
