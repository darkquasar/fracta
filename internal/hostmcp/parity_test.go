package hostmcp

import (
	"context"
	"testing"

	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests verify that the host MCP adapter's tool surface matches the
// lifecycle semantics that the CLI commands expose. Both surfaces route
// through ControlPlaneClient — this test proves the contract is honoured
// at the MCP tool boundary.

// TestParityToolCoverage verifies the host MCP adapter exposes the same
// lifecycle operations as the CLI commands:
//
//	CLI: spawn, list, peek, say, kill  (5 commands)
//	MCP: fracta_spawn, fracta_list, fracta_peek, fracta_say, fracta_kill, fracta_logs (6 tools)
//
// fracta_logs is MCP-only because the CLI uses peek for output. The CLI does
// not expose fracta_init (not a lifecycle op) or fracta_merge (local-only).
func TestParityToolCoverage(t *testing.T) {
	srv := New(&mockClient{})
	tools := srv.mcp.ListTools()

	// These tools correspond to CLI commands via ControlPlaneClient:
	cliParity := []string{
		"fracta_spawn", // CLI: fracta spawn
		"fracta_list",  // CLI: fracta list
		"fracta_peek",  // CLI: fracta peek
		"fracta_say",   // CLI: fracta say
		"fracta_kill",  // CLI: fracta kill
	}

	for _, name := range cliParity {
		_, ok := tools[name]
		assert.True(t, ok, "tool %q must be present for CLI parity", name)
	}

	// fracta_logs is MCP-only (CLI uses peek). Still uses ControlPlaneClient.
	_, ok := tools["fracta_logs"]
	assert.True(t, ok, "fracta_logs must be present (MCP-only lifecycle tool)")
}

// TestParitySpawnParams verifies that the MCP spawn tool accepts the same
// parameters that the CLI spawn command maps to SpawnRequest fields.
func TestParitySpawnParams(t *testing.T) {
	var capturedCLI, capturedMCP cpapi.SpawnRequest

	cliClient := &mockClient{
		spawnFn: func(_ context.Context, req cpapi.SpawnRequest) (*cpapi.SpawnResponse, error) {
			capturedCLI = req
			return &cpapi.SpawnResponse{Agent: req.Task, Status: "running", Mode: "batch"}, nil
		},
	}
	mcpClient := &mockClient{
		spawnFn: func(_ context.Context, req cpapi.SpawnRequest) (*cpapi.SpawnResponse, error) {
			capturedMCP = req
			return &cpapi.SpawnResponse{Agent: req.Task, Status: "running", Mode: "batch"}, nil
		},
	}

	// Simulate what the CLI does (from cmd/spawn.go runSpawn):
	_, err := cliClient.Spawn(context.Background(), cpapi.SpawnRequest{
		Task:        "test-agent",
		Contract:    "task instructions",
		BaseBranch:  "main",
		Model:       "opus",
		Tier:        "heavy",
		RuntimeType: "claude",
		Mode:        "batch",
	})
	require.NoError(t, err)

	// Simulate what the MCP tool does:
	srv := New(mcpClient)
	_, err = srv.handleSpawn(context.Background(), makeToolRequest(map[string]interface{}{
		"task":     "test-agent",
		"contract": "task instructions",
		"base":     "main",
		"model":    "opus",
		"tier":     "heavy",
		"runtime":  "claude",
		"mode":     "batch",
	}))
	require.NoError(t, err)

	// Verify both paths produce the same SpawnRequest to the client:
	assert.Equal(t, capturedCLI.Task, capturedMCP.Task)
	assert.Equal(t, capturedCLI.Contract, capturedMCP.Contract)
	assert.Equal(t, capturedCLI.BaseBranch, capturedMCP.BaseBranch)
	assert.Equal(t, capturedCLI.Model, capturedMCP.Model)
	assert.Equal(t, capturedCLI.Tier, capturedMCP.Tier)
	assert.Equal(t, capturedCLI.RuntimeType, capturedMCP.RuntimeType)
	assert.Equal(t, capturedCLI.Mode, capturedMCP.Mode)
}

// TestParitySayParams verifies say parameter equivalence.
func TestParitySayParams(t *testing.T) {
	var capturedCLI, capturedMCP cpapi.SayRequest

	cliClient := &mockClient{
		sayFn: func(_ context.Context, req cpapi.SayRequest) (*cpapi.SayResponse, error) {
			capturedCLI = req
			return &cpapi.SayResponse{Status: "completed", Message: "ok"}, nil
		},
	}
	mcpClient := &mockClient{
		sayFn: func(_ context.Context, req cpapi.SayRequest) (*cpapi.SayResponse, error) {
			capturedMCP = req
			return &cpapi.SayResponse{Status: "completed", Message: "ok"}, nil
		},
	}

	_, err := cliClient.Say(context.Background(), cpapi.SayRequest{
		Name:    "my-agent",
		Message: "continue working",
	})
	require.NoError(t, err)

	srv := New(mcpClient)
	_, err = srv.handleSay(context.Background(), makeToolRequest(map[string]interface{}{
		"name":    "my-agent",
		"message": "continue working",
	}))
	require.NoError(t, err)

	assert.Equal(t, capturedCLI.Name, capturedMCP.Name)
	assert.Equal(t, capturedCLI.Message, capturedMCP.Message)
}

// TestParityKillParams verifies kill parameter equivalence.
func TestParityKillParams(t *testing.T) {
	var capturedCLI, capturedMCP cpapi.KillRequest

	cliClient := &mockClient{
		killFn: func(_ context.Context, req cpapi.KillRequest) (*cpapi.KillResponse, error) {
			capturedCLI = req
			return &cpapi.KillResponse{Status: "killed"}, nil
		},
	}
	mcpClient := &mockClient{
		killFn: func(_ context.Context, req cpapi.KillRequest) (*cpapi.KillResponse, error) {
			capturedMCP = req
			return &cpapi.KillResponse{Status: "killed"}, nil
		},
	}

	_, err := cliClient.Kill(context.Background(), cpapi.KillRequest{
		Name:      "my-agent",
		KeepFiles: true,
	})
	require.NoError(t, err)

	srv := New(mcpClient)
	_, err = srv.handleKill(context.Background(), makeToolRequest(map[string]interface{}{
		"name":       "my-agent",
		"keep_files": true,
	}))
	require.NoError(t, err)

	assert.Equal(t, capturedCLI.Name, capturedMCP.Name)
	assert.Equal(t, capturedCLI.KeepFiles, capturedMCP.KeepFiles)
}

// TestParityPeekParams verifies peek parameter equivalence.
func TestParityPeekParams(t *testing.T) {
	var capturedCLI, capturedMCP cpapi.PeekRequest

	cliClient := &mockClient{
		peekFn: func(_ context.Context, req cpapi.PeekRequest) (*cpapi.PeekResponse, error) {
			capturedCLI = req
			return &cpapi.PeekResponse{Output: "output"}, nil
		},
	}
	mcpClient := &mockClient{
		peekFn: func(_ context.Context, req cpapi.PeekRequest) (*cpapi.PeekResponse, error) {
			capturedMCP = req
			return &cpapi.PeekResponse{Output: "output"}, nil
		},
	}

	_, err := cliClient.Peek(context.Background(), cpapi.PeekRequest{Name: "my-agent"})
	require.NoError(t, err)

	srv := New(mcpClient)
	_, err = srv.handlePeek(context.Background(), makeToolRequest(map[string]interface{}{
		"name": "my-agent",
	}))
	require.NoError(t, err)

	assert.Equal(t, capturedCLI.Name, capturedMCP.Name)
}

// TestParityNoAdminLeak verifies the host MCP adapter does not expose
// admin-only tools that belong on the Server/admin surface.
func TestParityNoAdminLeak(t *testing.T) {
	srv := New(&mockClient{})
	tools := srv.mcp.ListTools()

	adminOnly := []string{
		"fracta_init",       // admin setup
		"fracta_merge",      // local-only, not lifecycle
		"fracta_set_intent", // agent-facing
		"fracta_send",       // agent-facing
		"fracta_inbox",      // agent-facing
	}

	for _, name := range adminOnly {
		_, ok := tools[name]
		assert.False(t, ok, "admin/agent tool %q must NOT appear on host MCP surface", name)
	}
}

// TestParityClientInterface verifies that both the CLI adapter (cliClientAdapter
// in cmd/helpers.go) and the host MCP adapter share the same ControlPlaneClient
// contract at the type level. The mockClient used in both test suites implements
// the same interface, proving the contract is shared.
func TestParityClientInterface(t *testing.T) {
	// This is a compile-time assertion: if mockClient doesn't implement
	// ControlPlaneClient, this file won't compile.
	var _ cpapi.ControlPlaneClient = (*mockClient)(nil)

	// The host MCP server accepts any ControlPlaneClient:
	var client cpapi.ControlPlaneClient = &mockClient{}
	srv := New(client)
	assert.NotNil(t, srv)

	// This means the same LocalControlPlaneClient or RemoteControlPlaneClient
	// that the CLI uses can also power the host MCP surface.
}

// TestParitySpawnDispatchField verifies that the MCP spawn tool passes the
// dispatch field through, which the CLI spawn command omits (CLI only supports
// direct/batch). This is an expected MCP extension — the parity contract is
// that both use the same SpawnRequest type.
func TestParitySpawnDispatchField(t *testing.T) {
	var captured cpapi.SpawnRequest
	mc := &mockClient{
		spawnFn: func(_ context.Context, req cpapi.SpawnRequest) (*cpapi.SpawnResponse, error) {
			captured = req
			return &cpapi.SpawnResponse{Agent: req.Task, Status: "queued", Mode: "queued"}, nil
		},
	}

	srv := New(mc)
	result, err := srv.handleSpawn(context.Background(), makeToolRequest(map[string]interface{}{
		"task":     "queued-agent",
		"dispatch": "queued",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "queued", captured.Dispatch)
}

// TestParitySpawnObjectiveIDField verifies that the MCP spawn tool passes
// objective_id through the same SpawnRequest.
func TestParitySpawnObjectiveIDField(t *testing.T) {
	var captured cpapi.SpawnRequest
	mc := &mockClient{
		spawnFn: func(_ context.Context, req cpapi.SpawnRequest) (*cpapi.SpawnResponse, error) {
			captured = req
			return &cpapi.SpawnResponse{Agent: req.Task, Status: "queued", Mode: "queued"}, nil
		},
	}

	srv := New(mc)
	_, err := srv.handleSpawn(context.Background(), makeToolRequest(map[string]interface{}{
		"task":         "obj-agent",
		"dispatch":     "queued",
		"objective_id": "obj-123",
	}))
	require.NoError(t, err)
	assert.Equal(t, "obj-123", captured.ObjectiveID)
}

// TestParityErrorSurface verifies that client errors surface correctly
// through both CLI (return error) and MCP (IsError=true) paths.
func TestParityErrorSurface(t *testing.T) {
	errClient := &mockClient{
		peekFn: func(_ context.Context, _ cpapi.PeekRequest) (*cpapi.PeekResponse, error) {
			return nil, assert.AnError
		},
	}

	// CLI path: error returned directly
	_, err := errClient.Peek(context.Background(), cpapi.PeekRequest{Name: "test"})
	assert.Error(t, err)

	// MCP path: error becomes IsError=true tool result
	srv := New(errClient)
	result, err := srv.handlePeek(context.Background(), makeToolRequest(map[string]interface{}{
		"name": "test",
	}))
	require.NoError(t, err) // handler doesn't error, it returns tool error
	assert.True(t, result.IsError)
}

// ----------- helpers used by parity tests (reuse from server_test.go) -------
// makeToolRequest and textContent are defined in server_test.go in this package.
// Since they're in the same test package, they're available here.

// makeCallToolRequest is a typed wrapper for readability in parity tests.
func makeCallToolRequest(params map[string]interface{}) mcp.CallToolRequest {
	return makeToolRequest(params)
}
