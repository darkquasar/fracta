package cpapi

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/graph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockGraphClient implements graph.GraphClient for unit tests.
type mockGraphClient struct {
	queryResult []graph.Record
	queryErr    error
	updateErr   error

	lastQueryCypher  string
	lastQueryParams  map[string]any
	lastUpdateCypher string
	lastUpdateParams map[string]any
}

func (m *mockGraphClient) Query(_ context.Context, cypher string, params map[string]any) ([]graph.Record, error) {
	m.lastQueryCypher = cypher
	m.lastQueryParams = params
	return m.queryResult, m.queryErr
}
func (m *mockGraphClient) Update(_ context.Context, cypher string, params map[string]any) error {
	m.lastUpdateCypher = cypher
	m.lastUpdateParams = params
	return m.updateErr
}
func (m *mockGraphClient) Close() error { return nil }

// newGraphTestClient creates a LocalControlPlaneClient with a mock GraphClient.
func newGraphTestClient(t *testing.T, gc graph.GraphClient) *LocalControlPlaneClient {
	t.Helper()
	f := newTestClient(t)
	f.client.graphClient = gc
	return f.client
}

func TestLocalClient_Graph_NotConfigured(t *testing.T) {
	f := newTestClient(t)
	// graphClient is nil by default.
	ctx := context.Background()

	_, err := f.client.GraphQuery(ctx, GraphQueryRequest{Cypher: "MATCH (n) RETURN n"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "graph not configured")

	_, err = f.client.GraphUpdate(ctx, GraphUpdateRequest{Cypher: "CREATE (n:Test)"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "graph not configured")

	_, err = f.client.GraphSchema(ctx, GraphSchemaRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "graph not configured")

	_, err = f.client.GraphPath(ctx, GraphPathRequest{FromLabel: "A", FromKey: "name", FromValue: "x", ToLabel: "B", ToKey: "name", ToValue: "y"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "graph not configured")

	_, err = f.client.GraphNeighbors(ctx, GraphNeighborsRequest{Label: "A", Key: "name", Value: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "graph not configured")
}

func TestLocalClient_GraphQuery(t *testing.T) {
	gc := &mockGraphClient{
		queryResult: []graph.Record{{"name": "TestNode", "type": "System"}},
	}
	client := newGraphTestClient(t, gc)

	resp, err := client.GraphQuery(context.Background(), GraphQueryRequest{
		Cypher: "MATCH (n:System) RETURN n.name AS name, labels(n) AS type",
		Params: map[string]any{"limit": 10},
	})
	require.NoError(t, err)
	require.Len(t, resp.Records, 1)
	assert.Equal(t, "TestNode", resp.Records[0]["name"])
	assert.Equal(t, "MATCH (n:System) RETURN n.name AS name, labels(n) AS type", gc.lastQueryCypher)
}

func TestLocalClient_GraphQuery_Error(t *testing.T) {
	gc := &mockGraphClient{queryErr: fmt.Errorf("syntax error")}
	client := newGraphTestClient(t, gc)

	_, err := client.GraphQuery(context.Background(), GraphQueryRequest{Cypher: "BAD CYPHER"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "syntax error")
}

func TestLocalClient_GraphUpdate_Provenance(t *testing.T) {
	gc := &mockGraphClient{}
	client := newGraphTestClient(t, gc)

	resp, err := client.GraphUpdate(context.Background(), GraphUpdateRequest{
		Cypher:     "MERGE (n:Test {name: $name})",
		Params:     map[string]any{"name": "node-1"},
		Source:     "agent:test",
		Confidence: "high",
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Status)

	// Verify provenance was injected into params.
	assert.Equal(t, "agent:test", gc.lastUpdateParams["source"])
	assert.Equal(t, "high", gc.lastUpdateParams["confidence"])
	// updated_at should be present and RFC3339 formatted.
	updatedAt, ok := gc.lastUpdateParams["updated_at"].(string)
	require.True(t, ok, "updated_at must be a string")
	_, err = time.Parse(time.RFC3339, updatedAt)
	assert.NoError(t, err, "updated_at must be valid RFC3339")
	// Original params preserved.
	assert.Equal(t, "node-1", gc.lastUpdateParams["name"])
}

func TestLocalClient_GraphUpdate_NoProvenance(t *testing.T) {
	gc := &mockGraphClient{}
	client := newGraphTestClient(t, gc)

	resp, err := client.GraphUpdate(context.Background(), GraphUpdateRequest{
		Cypher: "CREATE (n:Test {name: $name})",
		Params: map[string]any{"name": "node-1"},
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Status)
	// No provenance injected.
	assert.NotContains(t, gc.lastUpdateParams, "source")
	assert.NotContains(t, gc.lastUpdateParams, "updated_at")
}

func TestLocalClient_GraphUpdate_ReservedKeyConflict(t *testing.T) {
	gc := &mockGraphClient{}
	client := newGraphTestClient(t, gc)

	_, err := client.GraphUpdate(context.Background(), GraphUpdateRequest{
		Cypher: "MERGE (n:Test {name: $source})",
		Params: map[string]any{"source": "user-provided"},
		Source: "agent:test",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reserved provenance key")
}

func TestLocalClient_GraphSchema(t *testing.T) {
	gc := &mockGraphClient{
		queryResult: []graph.Record{{"label": "System"}, {"label": "Identity"}},
	}
	client := newGraphTestClient(t, gc)

	resp, err := client.GraphSchema(context.Background(), GraphSchemaRequest{})
	require.NoError(t, err)
	// The mock returns same result for all queries, so labels should be populated
	// from the first call to gc.Query.
	assert.NotNil(t, resp.Labels)
}

func TestLocalClient_GraphPath(t *testing.T) {
	gc := &mockGraphClient{
		queryResult: []graph.Record{{"p": "path-data"}},
	}
	client := newGraphTestClient(t, gc)

	resp, err := client.GraphPath(context.Background(), GraphPathRequest{
		FromLabel: "System",
		FromKey:   "name",
		FromValue: "server_a",
		ToLabel:   "System",
		ToKey:     "name",
		ToValue:   "server_b",
	})
	require.NoError(t, err)
	require.Len(t, resp.Records, 1)

	// Verify the constructed Cypher uses the validated labels.
	assert.Contains(t, gc.lastQueryCypher, "System")
	assert.Contains(t, gc.lastQueryCypher, "shortestPath")
}

func TestLocalClient_GraphPath_ValidationError(t *testing.T) {
	gc := &mockGraphClient{}
	client := newGraphTestClient(t, gc)

	_, err := client.GraphPath(context.Background(), GraphPathRequest{
		FromLabel: "invalid label!", // contains space and punctuation
		FromKey:   "name",
		FromValue: "x",
		ToLabel:   "System",
		ToKey:     "name",
		ToValue:   "y",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestLocalClient_GraphNeighbors(t *testing.T) {
	gc := &mockGraphClient{
		queryResult: []graph.Record{
			{"labels": []string{"System"}, "props": map[string]any{"name": "peer-1"}},
		},
	}
	client := newGraphTestClient(t, gc)

	resp, err := client.GraphNeighbors(context.Background(), GraphNeighborsRequest{
		Label:     "System",
		Key:       "name",
		Value:     "server_a",
		Depth:     2,
		EdgeTypes: []string{"CONNECTS", "USES"},
	})
	require.NoError(t, err)
	require.Len(t, resp.Records, 1)

	assert.Contains(t, gc.lastQueryCypher, "System")
	assert.Contains(t, gc.lastQueryCypher, "*1..2")
	assert.Contains(t, gc.lastQueryCypher, "CONNECTS")
}

func TestLocalClient_GraphNeighbors_DefaultDepth(t *testing.T) {
	gc := &mockGraphClient{queryResult: []graph.Record{}}
	client := newGraphTestClient(t, gc)

	_, err := client.GraphNeighbors(context.Background(), GraphNeighborsRequest{
		Label: "System",
		Key:   "name",
		Value: "server_a",
		// Depth omitted (0) — should coerce to 1.
	})
	require.NoError(t, err)
	assert.Contains(t, gc.lastQueryCypher, "*1..1")
}

func TestLocalClient_GraphNeighbors_InvalidEdgeType(t *testing.T) {
	gc := &mockGraphClient{}
	client := newGraphTestClient(t, gc)

	_, err := client.GraphNeighbors(context.Background(), GraphNeighborsRequest{
		Label:     "System",
		Key:       "name",
		Value:     "x",
		EdgeTypes: []string{"invalid edge!"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}
