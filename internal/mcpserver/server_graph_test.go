package mcpserver

import (
	"context"
	"fmt"
	"testing"

	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/darkquasar/fracta/internal/graph"
	"github.com/stretchr/testify/assert"
)

// --- CP proxy probe tests (C7a) ---

func TestServerNew_CPProbeSuccess_RegistersGraphTools(t *testing.T) {
	// GraphSchema probe succeeds → graph tools registered, no checkpoint.
	mock := &probeTestCPClient{
		schemaResp: &cpapi.GraphSchemaResponse{
			Labels:            []string{"Test"},
			RelationshipTypes: []string{},
			PropertyKeys:      []string{},
		},
	}

	srv := New("/tmp/test-root", WithControlPlaneClient(mock))
	tools := srv.mcp.ListTools()

	expectedGraphTools := []string{
		"graph_query",
		"graph_update",
		"graph_schema",
		"graph_path",
		"graph_neighbors",
	}
	for _, name := range expectedGraphTools {
		_, ok := tools[name]
		assert.True(t, ok, "expected tool %q registered via CP proxy after successful probe", name)
	}

	// graph_checkpoint must NOT be on CP proxy path (AC 9).
	_, ok := tools["graph_checkpoint"]
	assert.False(t, ok, "graph_checkpoint should NOT be registered on CP proxy path")
}

func TestServerNew_CPProbeNotConfigured_SkipsGraphTools(t *testing.T) {
	// GraphSchema probe fails with "not configured" → graph tools absent.
	mock := &probeTestCPClient{
		schemaErr: fmt.Errorf("graph not configured"),
	}

	srv := New("/tmp/test-root", WithControlPlaneClient(mock))
	tools := srv.mcp.ListTools()

	absentTools := []string{
		"graph_query",
		"graph_update",
		"graph_schema",
		"graph_path",
		"graph_neighbors",
		"graph_checkpoint",
	}
	for _, name := range absentTools {
		_, ok := tools[name]
		assert.False(t, ok, "tool %q should NOT be registered when graph not configured", name)
	}
}

func TestServerNew_CPProbeTransientError_SkipsGraphTools(t *testing.T) {
	// GraphSchema probe fails with transient error → graph tools absent.
	mock := &probeTestCPClient{
		schemaErr: fmt.Errorf("connection refused"),
	}

	srv := New("/tmp/test-root", WithControlPlaneClient(mock))
	tools := srv.mcp.ListTools()

	absentTools := []string{
		"graph_query",
		"graph_update",
		"graph_schema",
		"graph_path",
		"graph_neighbors",
	}
	for _, name := range absentTools {
		_, ok := tools[name]
		assert.False(t, ok, "tool %q should NOT be registered on transient probe failure", name)
	}
}

// --- Direct-path checkpoint regression (C7b) ---

func TestServerNew_DirectGraph_RegistersGraphToolsAndCheckpoint(t *testing.T) {
	// Server.New with a graph.GraphClient → both 5 graph tools AND graph_checkpoint.
	gc := &stubGraphClient{}

	srv := New("/tmp/test-root", WithGraphClient(gc))
	tools := srv.mcp.ListTools()

	expectedTools := []string{
		"graph_query",
		"graph_update",
		"graph_schema",
		"graph_path",
		"graph_neighbors",
		"graph_checkpoint",
	}
	for _, name := range expectedTools {
		_, ok := tools[name]
		assert.True(t, ok, "expected tool %q registered on direct graph path", name)
	}
}

// --- Agent-server checkpoint regression (C7c) ---

func TestAgentServer_DirectGraph_RegistersCheckpoint(t *testing.T) {
	gc := &stubGraphClient{}

	srv := NewAgentServer("/tmp/test-root", WithAgentGraphClient(gc))
	tools := srv.mcp.ListTools()

	_, ok := tools["graph_checkpoint"]
	assert.True(t, ok, "graph_checkpoint should be registered on agent server with direct graph")
}

// --- Test mocks ---

// probeTestCPClient is a minimal ControlPlaneClient for probe tests.
// GraphSchema returns the configured response/error. All other methods
// delegate to noopCPClient.
type probeTestCPClient struct {
	noopCPClient
	schemaResp *cpapi.GraphSchemaResponse
	schemaErr  error
}

func (m *probeTestCPClient) GraphSchema(_ context.Context, _ cpapi.GraphSchemaRequest) (*cpapi.GraphSchemaResponse, error) {
	return m.schemaResp, m.schemaErr
}

// stubGraphClient is a minimal graph.GraphClient for testing tool registration.
// All methods return empty results.
type stubGraphClient struct{}

func (s *stubGraphClient) Query(_ context.Context, _ string, _ map[string]any) ([]graph.Record, error) {
	return nil, nil
}

func (s *stubGraphClient) Update(_ context.Context, _ string, _ map[string]any) error {
	return nil
}

func (s *stubGraphClient) Close() error { return nil }
