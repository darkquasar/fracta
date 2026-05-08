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
)

// graphMockClient extends mockClient with configurable graph responses.
type graphMockClient struct {
	mockClient

	graphQueryResp     *GraphQueryResponse
	graphQueryErr      error
	graphUpdateResp    *GraphUpdateResponse
	graphUpdateErr     error
	graphSchemaResp    *GraphSchemaResponse
	graphSchemaErr     error
	graphPathResp      *GraphPathResponse
	graphPathErr       error
	graphNeighborsResp *GraphNeighborsResponse
	graphNeighborsErr  error

	lastGraphQueryReq     GraphQueryRequest
	lastGraphUpdateReq    GraphUpdateRequest
	lastGraphPathReq      GraphPathRequest
	lastGraphNeighborsReq GraphNeighborsRequest
}

func (m *graphMockClient) GraphQuery(_ context.Context, req GraphQueryRequest) (*GraphQueryResponse, error) {
	m.lastGraphQueryReq = req
	return m.graphQueryResp, m.graphQueryErr
}
func (m *graphMockClient) GraphUpdate(_ context.Context, req GraphUpdateRequest) (*GraphUpdateResponse, error) {
	m.lastGraphUpdateReq = req
	return m.graphUpdateResp, m.graphUpdateErr
}
func (m *graphMockClient) GraphSchema(_ context.Context, _ GraphSchemaRequest) (*GraphSchemaResponse, error) {
	return m.graphSchemaResp, m.graphSchemaErr
}
func (m *graphMockClient) GraphPath(_ context.Context, req GraphPathRequest) (*GraphPathResponse, error) {
	m.lastGraphPathReq = req
	return m.graphPathResp, m.graphPathErr
}
func (m *graphMockClient) GraphNeighbors(_ context.Context, req GraphNeighborsRequest) (*GraphNeighborsResponse, error) {
	m.lastGraphNeighborsReq = req
	return m.graphNeighborsResp, m.graphNeighborsErr
}

func newGraphTestServer(mc *graphMockClient) *httptest.Server {
	srv := NewHTTPServer(":0", mc)
	return httptest.NewServer(srv.Handler())
}

func TestHandler_GraphQuery(t *testing.T) {
	mc := &graphMockClient{
		graphQueryResp: &GraphQueryResponse{
			Records: []map[string]any{{"name": "Node1"}},
		},
	}
	ts := newGraphTestServer(mc)
	defer ts.Close()

	body, _ := json.Marshal(GraphQueryRequest{Cypher: "MATCH (n) RETURN n LIMIT 1"})
	resp, err := http.Post(ts.URL+"/api/v1/graph/query", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "MATCH (n) RETURN n LIMIT 1", mc.lastGraphQueryReq.Cypher)

	var result GraphQueryResponse
	json.NewDecoder(resp.Body).Decode(&result)
	assert.Len(t, result.Records, 1)
}

func TestHandler_GraphQuery_MissingCypher(t *testing.T) {
	ts := newTestServer(&mockClient{})
	defer ts.Close()

	body, _ := json.Marshal(GraphQueryRequest{})
	resp, err := http.Post(ts.URL+"/api/v1/graph/query", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHandler_GraphQuery_Error(t *testing.T) {
	mc := &graphMockClient{graphQueryErr: fmt.Errorf("graph not configured")}
	ts := newGraphTestServer(mc)
	defer ts.Close()

	body, _ := json.Marshal(GraphQueryRequest{Cypher: "MATCH (n) RETURN n"})
	resp, err := http.Post(ts.URL+"/api/v1/graph/query", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestHandler_GraphUpdate(t *testing.T) {
	mc := &graphMockClient{
		graphUpdateResp: &GraphUpdateResponse{Status: "ok"},
	}
	ts := newGraphTestServer(mc)
	defer ts.Close()

	body, _ := json.Marshal(GraphUpdateRequest{
		Cypher: "MERGE (n:Test {name: $name})",
		Params: map[string]any{"name": "test"},
		Source: "agent:test",
	})
	resp, err := http.Post(ts.URL+"/api/v1/graph/update", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "agent:test", mc.lastGraphUpdateReq.Source)
}

func TestHandler_GraphUpdate_MissingCypher(t *testing.T) {
	ts := newTestServer(&mockClient{})
	defer ts.Close()

	body, _ := json.Marshal(GraphUpdateRequest{})
	resp, err := http.Post(ts.URL+"/api/v1/graph/update", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHandler_GraphSchema(t *testing.T) {
	mc := &graphMockClient{
		graphSchemaResp: &GraphSchemaResponse{
			Labels:            []string{"Node", "Edge"},
			RelationshipTypes: []string{"CONNECTS"},
			PropertyKeys:      []string{"name", "type"},
		},
	}
	ts := newGraphTestServer(mc)
	defer ts.Close()

	// Test with empty body (should work due to EOF tolerance).
	resp, err := http.Post(ts.URL+"/api/v1/graph/schema", "application/json", bytes.NewReader([]byte{}))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result GraphSchemaResponse
	json.NewDecoder(resp.Body).Decode(&result)
	assert.Equal(t, []string{"Node", "Edge"}, result.Labels)
	assert.Equal(t, []string{"CONNECTS"}, result.RelationshipTypes)
}

func TestHandler_GraphSchema_WithBody(t *testing.T) {
	mc := &graphMockClient{
		graphSchemaResp: &GraphSchemaResponse{Labels: []string{"Test"}},
	}
	ts := newGraphTestServer(mc)
	defer ts.Close()

	body, _ := json.Marshal(GraphSchemaRequest{})
	resp, err := http.Post(ts.URL+"/api/v1/graph/schema", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHandler_GraphPath(t *testing.T) {
	mc := &graphMockClient{
		graphPathResp: &GraphPathResponse{
			Records: []map[string]any{{"path": "a->b"}},
		},
	}
	ts := newGraphTestServer(mc)
	defer ts.Close()

	body, _ := json.Marshal(GraphPathRequest{
		FromLabel: "System",
		FromKey:   "name",
		FromValue: "server-a",
		ToLabel:   "System",
		ToKey:     "name",
		ToValue:   "server-b",
	})
	resp, err := http.Post(ts.URL+"/api/v1/graph/path", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "System", mc.lastGraphPathReq.FromLabel)
}

func TestHandler_GraphNeighbors(t *testing.T) {
	mc := &graphMockClient{
		graphNeighborsResp: &GraphNeighborsResponse{
			Records: []map[string]any{{"labels": []string{"System"}, "props": map[string]any{"name": "peer"}}},
		},
	}
	ts := newGraphTestServer(mc)
	defer ts.Close()

	body, _ := json.Marshal(GraphNeighborsRequest{
		Label:     "System",
		Key:       "name",
		Value:     "server-a",
		Depth:     2,
		EdgeTypes: []string{"CONNECTS"},
	})
	resp, err := http.Post(ts.URL+"/api/v1/graph/neighbors", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 2, mc.lastGraphNeighborsReq.Depth)
	assert.Equal(t, []string{"CONNECTS"}, mc.lastGraphNeighborsReq.EdgeTypes)
}

func TestHandler_GraphNeighbors_Error(t *testing.T) {
	mc := &graphMockClient{graphNeighborsErr: fmt.Errorf("graph not configured")}
	ts := newGraphTestServer(mc)
	defer ts.Close()

	body, _ := json.Marshal(GraphNeighborsRequest{Label: "System", Key: "name", Value: "x"})
	resp, err := http.Post(ts.URL+"/api/v1/graph/neighbors", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}
