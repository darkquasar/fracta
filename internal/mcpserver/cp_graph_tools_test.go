package mcpserver

import (
	"context"
	"fmt"
	"testing"

	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// graphMockCPClient implements cpapi.ControlPlaneClient for CP proxy tool tests.
type graphMockCPClient struct {
	noopCPClient

	queryResp     *cpapi.GraphQueryResponse
	queryErr      error
	updateResp    *cpapi.GraphUpdateResponse
	updateErr     error
	schemaResp    *cpapi.GraphSchemaResponse
	schemaErr     error
	pathResp      *cpapi.GraphPathResponse
	pathErr       error
	neighborsResp *cpapi.GraphNeighborsResponse
	neighborsErr  error

	lastQueryReq     cpapi.GraphQueryRequest
	lastUpdateReq    cpapi.GraphUpdateRequest
	lastNeighborsReq cpapi.GraphNeighborsRequest
}

func (m *graphMockCPClient) GraphQuery(_ context.Context, req cpapi.GraphQueryRequest) (*cpapi.GraphQueryResponse, error) {
	m.lastQueryReq = req
	return m.queryResp, m.queryErr
}

func (m *graphMockCPClient) GraphUpdate(_ context.Context, req cpapi.GraphUpdateRequest) (*cpapi.GraphUpdateResponse, error) {
	m.lastUpdateReq = req
	return m.updateResp, m.updateErr
}

func (m *graphMockCPClient) GraphSchema(_ context.Context, _ cpapi.GraphSchemaRequest) (*cpapi.GraphSchemaResponse, error) {
	return m.schemaResp, m.schemaErr
}

func (m *graphMockCPClient) GraphPath(_ context.Context, _ cpapi.GraphPathRequest) (*cpapi.GraphPathResponse, error) {
	return m.pathResp, m.pathErr
}

func (m *graphMockCPClient) GraphNeighbors(_ context.Context, req cpapi.GraphNeighborsRequest) (*cpapi.GraphNeighborsResponse, error) {
	m.lastNeighborsReq = req
	return m.neighborsResp, m.neighborsErr
}

// --- Registration tests ---

func TestCPGraphTools_Registration(t *testing.T) {
	m := server.NewMCPServer("test", "0.1.0", server.WithToolCapabilities(true))
	registerCPGraphTools(m, &graphMockCPClient{})

	tools := m.ListTools()

	expectedTools := []string{
		"graph_query",
		"graph_update",
		"graph_schema",
		"graph_path",
		"graph_neighbors",
	}
	for _, name := range expectedTools {
		_, ok := tools[name]
		assert.True(t, ok, "expected tool %q to be registered", name)
	}

	// graph_checkpoint must NOT be registered via CP proxy path (AC 9).
	_, ok := tools["graph_checkpoint"]
	assert.False(t, ok, "graph_checkpoint should NOT be registered on CP proxy path")
}

// --- Output parity tests ---

func TestCPGraphTools_QueryOutputParity(t *testing.T) {
	mock := &graphMockCPClient{
		queryResp: &cpapi.GraphQueryResponse{
			Records: []map[string]any{
				{"name": "CloudTrail", "label": "DomainSource"},
			},
		},
	}

	handler := makeCPGraphQueryHandler(mock)
	result, err := handler(context.Background(), makeGraphToolRequest(map[string]interface{}{
		"cypher": "MATCH (n) RETURN n LIMIT 1",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Output must be a JSON array (same as direct path: json.Marshal(records)).
	text := extractToolText(result)
	assert.Contains(t, text, `"name":"CloudTrail"`)
	assert.True(t, text[0] == '[', "output should be a JSON array, got: %s", text)
}

func TestCPGraphTools_UpdateOutputParity(t *testing.T) {
	mock := &graphMockCPClient{
		updateResp: &cpapi.GraphUpdateResponse{Status: "ok"},
	}

	handler := makeCPGraphUpdateHandler(mock)
	result, err := handler(context.Background(), makeGraphToolRequest(map[string]interface{}{
		"cypher": "CREATE (n:Test {name: $name})",
		"params": `{"name": "test"}`,
		"source": "agent:test",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Output must be exactly "Update executed successfully." (parity with direct path).
	assert.Equal(t, "Update executed successfully.", extractToolText(result))

	// Verify provenance params are forwarded to cpClient (not injected here).
	assert.Equal(t, "agent:test", mock.lastUpdateReq.Source)
}

func TestCPGraphTools_SchemaOutputParity(t *testing.T) {
	mock := &graphMockCPClient{
		schemaResp: &cpapi.GraphSchemaResponse{
			Labels:            []string{"DomainSource", "DataStore"},
			RelationshipTypes: []string{"STORED_IN", "QUERYABLE_VIA"},
			PropertyKeys:      []string{"name", "uri"},
		},
	}

	handler := makeCPGraphSchemaHandler(mock)
	result, err := handler(context.Background(), mcp.CallToolRequest{})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Output must be a structured JSON object with labels, relationship_types, property_keys.
	text := extractToolText(result)
	assert.Contains(t, text, `"labels"`)
	assert.Contains(t, text, `"relationship_types"`)
	assert.Contains(t, text, `"property_keys"`)
	assert.Contains(t, text, `"DomainSource"`)
}

func TestCPGraphTools_PathEmptyOutputParity(t *testing.T) {
	mock := &graphMockCPClient{
		pathResp: &cpapi.GraphPathResponse{
			Records: []map[string]any{},
		},
	}

	handler := makeCPGraphPathHandler(mock)
	result, err := handler(context.Background(), makeGraphToolRequest(map[string]interface{}{
		"from_label": "DomainSource",
		"from_key":   "name",
		"from_value": "CloudTrail",
		"to_label":   "MCPTool",
		"to_key":     "name",
		"to_value":   "elastic.search",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Empty result text must match direct path.
	assert.Equal(t, "No path found between the specified nodes.", extractToolText(result))
}

func TestCPGraphTools_PathNonEmptyOutputParity(t *testing.T) {
	mock := &graphMockCPClient{
		pathResp: &cpapi.GraphPathResponse{
			Records: []map[string]any{
				{"p": "some-path-data"},
			},
		},
	}

	handler := makeCPGraphPathHandler(mock)
	result, err := handler(context.Background(), makeGraphToolRequest(map[string]interface{}{
		"from_label": "DomainSource",
		"from_key":   "name",
		"from_value": "CloudTrail",
		"to_label":   "MCPTool",
		"to_key":     "name",
		"to_value":   "elastic.search",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	text := extractToolText(result)
	assert.True(t, text[0] == '[', "non-empty path output should be a JSON array")
}

func TestCPGraphTools_NeighborsEmptyOutputParity(t *testing.T) {
	mock := &graphMockCPClient{
		neighborsResp: &cpapi.GraphNeighborsResponse{
			Records: []map[string]any{},
		},
	}

	handler := makeCPGraphNeighborsHandler(mock)
	result, err := handler(context.Background(), makeGraphToolRequest(map[string]interface{}{
		"label": "DomainSource",
		"key":   "name",
		"value": "CloudTrail",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Empty result text must match direct path.
	assert.Equal(t, "No neighbors found.", extractToolText(result))
}

func TestCPGraphTools_NeighborsNonEmptyOutputParity(t *testing.T) {
	mock := &graphMockCPClient{
		neighborsResp: &cpapi.GraphNeighborsResponse{
			Records: []map[string]any{
				{"labels": []string{"DataStore"}, "props": map[string]any{"uri": "elasticsearch://main/logs"}},
			},
		},
	}

	handler := makeCPGraphNeighborsHandler(mock)
	result, err := handler(context.Background(), makeGraphToolRequest(map[string]interface{}{
		"label": "DomainSource",
		"key":   "name",
		"value": "CloudTrail",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	text := extractToolText(result)
	assert.True(t, text[0] == '[', "non-empty neighbors output should be a JSON array")
}

// --- Argument parsing parity tests ---

func TestCPGraphTools_NeighborsDepthDefault(t *testing.T) {
	mock := &graphMockCPClient{
		neighborsResp: &cpapi.GraphNeighborsResponse{Records: []map[string]any{}},
	}

	handler := makeCPGraphNeighborsHandler(mock)

	// Missing depth → default 1 (same as direct path).
	_, err := handler(context.Background(), makeGraphToolRequest(map[string]interface{}{
		"label": "DomainSource",
		"key":   "name",
		"value": "CloudTrail",
	}))
	require.NoError(t, err)
	assert.Equal(t, 1, mock.lastNeighborsReq.Depth)

	// Invalid depth → default 1.
	_, err = handler(context.Background(), makeGraphToolRequest(map[string]interface{}{
		"label": "DomainSource",
		"key":   "name",
		"value": "CloudTrail",
		"depth": "abc",
	}))
	require.NoError(t, err)
	assert.Equal(t, 1, mock.lastNeighborsReq.Depth)

	// Nonpositive depth → default 1.
	_, err = handler(context.Background(), makeGraphToolRequest(map[string]interface{}{
		"label": "DomainSource",
		"key":   "name",
		"value": "CloudTrail",
		"depth": "0",
	}))
	require.NoError(t, err)
	assert.Equal(t, 1, mock.lastNeighborsReq.Depth)
}

func TestCPGraphTools_NeighborsEdgeTypesParsing(t *testing.T) {
	mock := &graphMockCPClient{
		neighborsResp: &cpapi.GraphNeighborsResponse{Records: []map[string]any{}},
	}

	handler := makeCPGraphNeighborsHandler(mock)
	_, err := handler(context.Background(), makeGraphToolRequest(map[string]interface{}{
		"label":      "DomainSource",
		"key":        "name",
		"value":      "CloudTrail",
		"edge_types": "HAS_FIELD, JOINS_WITH",
	}))
	require.NoError(t, err)
	assert.Equal(t, []string{"HAS_FIELD", "JOINS_WITH"}, mock.lastNeighborsReq.EdgeTypes)
}

func TestCPGraphTools_UpdateProvenanceForwarding(t *testing.T) {
	mock := &graphMockCPClient{
		updateResp: &cpapi.GraphUpdateResponse{Status: "ok"},
	}

	handler := makeCPGraphUpdateHandler(mock)
	_, err := handler(context.Background(), makeGraphToolRequest(map[string]interface{}{
		"cypher":          "MERGE (n:Test {name: $name})",
		"source":          "agent:hunter",
		"confidence":      "high",
		"correlation_key": "hunt-42",
	}))
	require.NoError(t, err)
	assert.Equal(t, "agent:hunter", mock.lastUpdateReq.Source)
	assert.Equal(t, "high", mock.lastUpdateReq.Confidence)
	assert.Equal(t, "hunt-42", mock.lastUpdateReq.CorrelationKey)
}

func TestCPGraphTools_QueryError(t *testing.T) {
	mock := &graphMockCPClient{
		queryErr: fmt.Errorf("connection refused"),
	}

	handler := makeCPGraphQueryHandler(mock)
	result, err := handler(context.Background(), makeGraphToolRequest(map[string]interface{}{
		"cypher": "MATCH (n) RETURN n",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractToolText(result), "graph query failed")
}

// --- helpers ---

func makeGraphToolRequest(params map[string]interface{}) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: params,
		},
	}
}

func extractToolText(r *mcp.CallToolResult) string {
	for _, c := range r.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}
