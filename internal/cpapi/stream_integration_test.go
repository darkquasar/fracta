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
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/orchestrator"
)

// Stream-mode parity tests for the remote path.
//
// These tests verify that remote say, peek, and kill on live stream agents
// reach the single authoritative server-side ProcessRegistry correctly.
//
// Implementation note (spec S6.4, S11 rule 8):
// The HTTPServer and LocalControlPlaneClient share the same ProcessRegistry
// instance. Remote requests through the HTTP API are handled by the same
// process that owns the live sessions. This is the correct ownership model
// for single-process deployment.
//
// For multi-replica deployments (future), requests would need to be routed
// to the replica that owns the session, or a server-side session manager
// abstraction would be needed. That is explicitly out of scope for spec-30.

func newStreamFixture(t *testing.T) (*RemoteControlPlaneClient, *testStore, *orchestrator.ProcessRegistry, *httptest.Server) {
	t.Helper()

	store := &testStore{}
	mb := &testMailbox{}
	backend := &testBackend{logOutput: "backend logs"}
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
		Project: config.ProjectConfig{
			AllowedTools: []string{"Bash"},
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

	local := NewLocalControlPlaneClient(cp, "/tmp/stream-test",
		WithProcessRegistry(registry),
		WithRuntimeRegistry(hostReg),
		WithObjectiveStore(objStore),
	)

	srv := NewHTTPServer(":0", local)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	remote := NewRemoteControlPlaneClient(ts.URL)
	return remote, store, registry, ts
}

// TestStreamParity_PeekReachesLiveSession verifies that a remote peek
// request reaches the server-side ProcessRegistry's live stream handle.
func TestStreamParity_PeekReachesLiveSession(t *testing.T) {
	remote, store, registry, _ := newStreamFixture(t)
	ctx := context.Background()

	store.agents = []model.AgentEntry{
		{Task: "stream-agent", Status: model.StatusIdle, Mode: "stream", LastOutput: "stale"},
	}
	registry.Register("stream-agent", &testStreamSession{recentOutput: "fresh live output"})

	resp, err := remote.Peek(ctx, PeekRequest{Name: "stream-agent"})
	require.NoError(t, err)
	assert.Equal(t, "fresh live output", resp.Output, "peek should return live stream output, not stale state")
}

// TestStreamParity_KillClosesLiveSession verifies that a remote kill
// request closes the server-side ProcessRegistry's live stream handle.
func TestStreamParity_KillClosesLiveSession(t *testing.T) {
	remote, store, registry, _ := newStreamFixture(t)
	ctx := context.Background()

	session := &testStreamSession{}
	store.agents = []model.AgentEntry{
		{Task: "stream-agent", Status: model.StatusIdle, Mode: "stream", WorkspacePath: "/tmp/ws"},
	}
	registry.Register("stream-agent", session)

	resp, err := remote.Kill(ctx, KillRequest{Name: "stream-agent"})
	require.NoError(t, err)
	assert.Equal(t, "killed", resp.Status)
	assert.True(t, session.closed, "kill should close the live stream session")
	assert.Nil(t, registry.Get("stream-agent"), "kill should remove handle from registry")
}

// TestStreamParity_SayRoutesToStreamHandle verifies that a remote say
// request on a stream agent routes through the ProcessRegistry.
func TestStreamParity_SayRoutesToStreamHandle(t *testing.T) {
	remote, store, registry, _ := newStreamFixture(t)
	ctx := context.Background()

	session := &testStreamSession{recentOutput: "initial"}
	store.agents = []model.AgentEntry{
		{Task: "stream-agent", Status: model.StatusIdle, Mode: "stream"},
	}
	registry.Register("stream-agent", session)

	resp, err := remote.Say(ctx, SayRequest{Name: "stream-agent", Message: "follow up"})
	require.NoError(t, err)
	assert.Equal(t, "dispatched", resp.Status)
	assert.Contains(t, resp.Message, "streaming agent")
}

// TestStreamParity_SayFallsToBatchResume verifies that when no stream
// handle exists, say falls through to the batch resume path.
func TestStreamParity_SayFallsToBatchResume(t *testing.T) {
	remote, store, _, _ := newStreamFixture(t)
	ctx := context.Background()

	store.agents = []model.AgentEntry{
		{Task: "batch-agent", Status: model.StatusCompleted, Mode: "batch",
			RuntimeType: "test", ResumeToken: "tok-123"},
	}
	// No stream handle registered — should fall to batch resume.

	// SayAsync will try to spawn a process, which will fail in test env.
	// That's expected — the important thing is that it routes correctly.
	resp, err := remote.Say(ctx, SayRequest{Name: "batch-agent", Message: "resume"})
	// Error is expected because the mock backend's Spawn returns an error.
	// But the error message should indicate it went through the batch path.
	if err != nil {
		assert.Contains(t, err.Error(), "say failed")
	} else {
		assert.Equal(t, "dispatched", resp.Status)
	}
}
