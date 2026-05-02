package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/agentlifecycle"
	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/controlplane"
	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/host/claude"
	"github.com/darkquasar/fracta/internal/mailbox"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/runtime"
	"github.com/darkquasar/fracta/internal/state"
	"github.com/darkquasar/fracta/internal/state/sqlitestore"
	"github.com/darkquasar/fracta/internal/workspace"
)

// --- Mock Workspace ---

type mockWorkspace struct {
	created map[string]bool
	root    string
}

func newMockWorkspace(root string) *mockWorkspace {
	return &mockWorkspace{
		created: make(map[string]bool),
		root:    root,
	}
}

func (m *mockWorkspace) Create(agentID string, baseBranch string) (*workspace.Info, error) {
	path := filepath.Join(m.root, ".worktrees", agentID)
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, err
	}
	m.created[path] = true
	return &workspace.Info{
		Path:       path,
		BranchName: "feature/" + agentID,
		BaseBranch: baseBranch,
	}, nil
}

func (m *mockWorkspace) Remove(info *workspace.Info, keepFiles bool) error {
	if keepFiles {
		return nil
	}
	delete(m.created, info.Path)
	os.RemoveAll(info.Path)
	return nil
}

// --- Mock AgentHandle ---

type mockAgentHandle struct {
	output    []byte
	exitCode  int
	startTime time.Time
	waitErr   error
}

func (h *mockAgentHandle) Wait() error        { return h.waitErr }
func (h *mockAgentHandle) Output() io.Reader   { return bytes.NewReader(h.output) }
func (h *mockAgentHandle) ExitCode() int       { return h.exitCode }
func (h *mockAgentHandle) StartTime() time.Time { return h.startTime }

// --- Mock Backend ---

type mockBackend struct {
	spawnCalls []runtime.SpawnOpts
	killCalls  []string
	handle     *mockAgentHandle
	spawnErr   error
}

func (b *mockBackend) Spawn(_ context.Context, opts runtime.SpawnOpts) (runtime.AgentHandle, error) {
	b.spawnCalls = append(b.spawnCalls, opts)
	if b.spawnErr != nil {
		return nil, b.spawnErr
	}
	return b.handle, nil
}

func (b *mockBackend) Kill(_ context.Context, id string) error {
	b.killCalls = append(b.killCalls, id)
	return nil
}

func (b *mockBackend) Status(_ context.Context, id string) (model.AgentStatus, error) {
	return model.StatusCompleted, nil
}

func (b *mockBackend) Logs(_ context.Context, id string, tailLines int) (string, error) {
	return "", fmt.Errorf("not implemented in mock")
}

// --- Mock Store (simple, for CanSpawn tests) ---

type mockStore struct {
	st model.State
}

func (m *mockStore) Load(_ context.Context) (model.State, error)                   { return m.st, nil }
func (m *mockStore) WithLock(_ context.Context, fn func(*model.State) error) error { return fn(&m.st) }
func (m *mockStore) Close() error                                                  { return nil }
func (m *mockStore) Mailbox() mailbox.Mailbox                                      { return nil }
func (m *mockStore) UpdateChessmaster(_ context.Context, status, lastAction string, updatedAt time.Time) error {
	m.st.Chessmaster.Status = status
	m.st.Chessmaster.LastAction = lastAction
	m.st.Chessmaster.UpdatedAt = updatedAt
	return nil
}
func (m *mockStore) RemoveAgent(_ context.Context, task string) error {
	filtered := m.st.Agents[:0]
	for _, a := range m.st.Agents {
		if a.Task != task {
			filtered = append(filtered, a)
		}
	}
	m.st.Agents = filtered
	return nil
}
func (m *mockStore) UpdateAgentResult(_ context.Context, task string, status model.AgentStatus, lastOutput, resumeToken string) error {
	for i := range m.st.Agents {
		if m.st.Agents[i].Task == task {
			m.st.Agents[i].Status = status
			m.st.Agents[i].LastOutput = lastOutput
			m.st.Agents[i].ResumeToken = resumeToken
			return nil
		}
	}
	return fmt.Errorf("agent %s not found", task)
}
func (m *mockStore) UpdateAgentIntent(_ context.Context, task, intent string) error {
	for i := range m.st.Agents {
		if m.st.Agents[i].Task == task {
			m.st.Agents[i].CurrentIntent = intent
			return nil
		}
	}
	return fmt.Errorf("agent %s not found", task)
}
func (m *mockStore) UpdateAgentStatus(_ context.Context, task string, status model.AgentStatus, lastOutput string) error {
	for i := range m.st.Agents {
		if m.st.Agents[i].Task == task {
			m.st.Agents[i].Status = status
			m.st.Agents[i].LastOutput = lastOutput
			return nil
		}
	}
	return nil
}
func (m *mockStore) FindAgent(_ context.Context, task string) (*model.AgentEntry, error) {
	for i := range m.st.Agents {
		if m.st.Agents[i].Task == task {
			return &m.st.Agents[i], nil
		}
	}
	return nil, nil
}
func (m *mockStore) ClaimAgent(_ context.Context, task string) error {
	for i := range m.st.Agents {
		if m.st.Agents[i].Task == task && m.st.Agents[i].Status == model.StatusQueued {
			m.st.Agents[i].Status = model.StatusRunning
			m.st.Agents[i].StartTime = time.Now()
			return nil
		}
	}
	return fmt.Errorf("agent %s not found or not queued", task)
}
func (m *mockStore) UpdateAgentStatusIf(_ context.Context, task string, expected []model.AgentStatus, newStatus model.AgentStatus, lastOutput string) (bool, error) {
	for i := range m.st.Agents {
		if m.st.Agents[i].Task == task {
			for _, exp := range expected {
				if m.st.Agents[i].Status == exp {
					m.st.Agents[i].Status = newStatus
					m.st.Agents[i].LastOutput = lastOutput
					return true, nil
				}
			}
			return false, nil
		}
	}
	return false, nil
}
func (m *mockStore) UpdateAgentResultIf(_ context.Context, task string, expected []model.AgentStatus, status model.AgentStatus, lastOutput, resumeToken string) (bool, error) {
	for i := range m.st.Agents {
		if m.st.Agents[i].Task == task {
			for _, exp := range expected {
				if m.st.Agents[i].Status == exp {
					m.st.Agents[i].Status = status
					m.st.Agents[i].LastOutput = lastOutput
					if resumeToken != "" {
						m.st.Agents[i].ResumeToken = resumeToken
					}
					return true, nil
				}
			}
			return false, nil
		}
	}
	return false, nil
}

// --- Test Fixtures ---

func setupIntegrationTest(t *testing.T) (root string, store state.Store, wsMock *mockWorkspace) {
	t.Helper()
	root = t.TempDir()

	fractaDir := filepath.Join(root, model.FractaDir)
	if err := os.MkdirAll(fractaDir, 0755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(fractaDir, "state.db")
	ss, err := sqlitestore.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })

	store = ss
	wsMock = newMockWorkspace(root)
	return
}

func newTestOrchestrator(ws workspace.Workspace, store state.Store, backend runtime.Backend, root string) *Orchestrator {
	reg := host.NewMapRegistry("claude")
	reg.Register("claude", claude.Host{})
	orch := &Orchestrator{
		HostRegistry: reg,
		Workspace:    ws,
		Store:        store,
		Backend:      backend,
		Root:         root,
		Config: &config.Config{
			Project: config.ProjectConfig{
				DefaultBaseBranch: "main",
				AllowedTools:      []string{"Bash(*)"},
			},
			Agents: config.AgentsConfig{DefaultMode: "batch"},
			Runtimes: map[string]config.RuntimeEntry{
				"claude": {Adapter: "claude"},
			},
		},
		Lifecycle: agentlifecycle.New(store, events.NoopBus{}),
	}
	orch.Mailbox = store.Mailbox()
	return orch
}

// ========== Phase 5: Backend + Spawn Integration Tests ==========

func TestIntegration_Spawn_WithBackend_Success(t *testing.T) {
	root, store, wsMock := setupIntegrationTest(t)

	resp := claude.Response{SessionID: "sess-1", Result: "task completed", IsError: false}
	respJSON, _ := json.Marshal(resp)

	backend := &mockBackend{
		handle: &mockAgentHandle{output: respJSON, exitCode: 0, startTime: time.Now()},
	}

	orch := newTestOrchestrator(wsMock, store, backend, root)

	if err := orch.Spawn("test-agent", "", "main", "", "", ""); err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	if len(backend.spawnCalls) != 1 {
		t.Fatalf("expected 1 Spawn call, got %d", len(backend.spawnCalls))
	}
	if backend.spawnCalls[0].ID != "test-agent" {
		t.Errorf("SpawnOpts.ID = %q, want %q", backend.spawnCalls[0].ID, "test-agent")
	}

	st, _ := store.Load(context.Background())
	if len(st.Agents) != 1 || st.Agents[0].Status != model.StatusCompleted {
		t.Errorf("agent status = %q, want Completed", st.Agents[0].Status)
	}
}

func TestIntegration_SpawnAsync_WithBackend_StateTransitions(t *testing.T) {
	root, store, wsMock := setupIntegrationTest(t)

	resp := claude.Response{SessionID: "sess-2", Result: "async done", IsError: false}
	respJSON, _ := json.Marshal(resp)

	backend := &mockBackend{
		handle: &mockAgentHandle{output: respJSON, exitCode: 0, startTime: time.Now()},
	}

	orch := newTestOrchestrator(wsMock, store, backend, root)

	if err := orch.SpawnAsync("async-agent", "", "main", "", "", ""); err != nil {
		t.Fatalf("SpawnAsync failed: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for agent to complete")
		default:
			st, _ := store.Load(context.Background())
			if st.Agents[0].Status == model.StatusCompleted {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestIntegration_Spawn_DuplicateName_Rejected(t *testing.T) {
	root, store, wsMock := setupIntegrationTest(t)

	resp := claude.Response{Result: "ok"}
	respJSON, _ := json.Marshal(resp)

	backend := &mockBackend{
		handle: &mockAgentHandle{output: respJSON, startTime: time.Now()},
	}

	orch := newTestOrchestrator(wsMock, store, backend, root)

	if err := orch.Spawn("dupe", "", "main", "", "", ""); err != nil {
		t.Fatalf("first Spawn failed: %v", err)
	}

	if err := orch.Spawn("dupe", "", "main", "", "", ""); err == nil {
		t.Fatal("expected error for duplicate agent name, got nil")
	}
}

func TestIntegration_Spawn_ThenKill(t *testing.T) {
	root, store, wsMock := setupIntegrationTest(t)

	resp := claude.Response{SessionID: "sess-3", Result: "done"}
	respJSON, _ := json.Marshal(resp)

	backend := &mockBackend{
		handle: &mockAgentHandle{output: respJSON, startTime: time.Now()},
	}

	orch := newTestOrchestrator(wsMock, store, backend, root)

	if err := orch.Spawn("killme", "", "main", "", "", ""); err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	if err := orch.Kill("killme", false); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}

	st, _ := store.Load(context.Background())
	if len(st.Agents) != 0 {
		t.Errorf("expected 0 agents after kill, got %d", len(st.Agents))
	}
}

func TestIntegration_Spawn_BackendError_CleansUpWorktree(t *testing.T) {
	root, store, wsMock := setupIntegrationTest(t)

	backend := &mockBackend{spawnErr: context.DeadlineExceeded}

	orch := newTestOrchestrator(wsMock, store, backend, root)

	if err := orch.Spawn("fail-agent", "", "main", "", "", ""); err == nil {
		t.Fatal("expected error from failed backend spawn")
	}

	st, _ := store.Load(context.Background())
	if len(st.Agents) != 0 {
		t.Errorf("expected 0 agents after failure, got %d", len(st.Agents))
	}
}

func TestIntegration_Spawn_AgentError_RecordsFailed(t *testing.T) {
	root, store, wsMock := setupIntegrationTest(t)

	resp := claude.Response{SessionID: "sess-err", Result: "something went wrong", IsError: true}
	respJSON, _ := json.Marshal(resp)

	backend := &mockBackend{
		handle: &mockAgentHandle{output: respJSON, exitCode: 1, startTime: time.Now()},
	}

	orch := newTestOrchestrator(wsMock, store, backend, root)

	if err := orch.Spawn("err-agent", "", "main", "", "", ""); err != nil {
		t.Fatalf("Spawn should not fail for agent-level errors: %v", err)
	}

	st, _ := store.Load(context.Background())
	if st.Agents[0].Status != model.StatusFailed {
		t.Errorf("status = %q, want Failed", st.Agents[0].Status)
	}
}

// ========== Phase 6: CheckSpawnAllowed + MCP Delivery Tests ==========

func TestCheckSpawnAllowed_RejectsAtLimit(t *testing.T) {
	st := &model.State{
		Agents: []model.AgentEntry{
			{Task: "agent-1", Status: model.StatusRunning, StartTime: time.Now()},
			{Task: "agent-2", Status: model.StatusRunning, StartTime: time.Now()},
		},
	}

	reaper := controlplane.NewReaper(&mockStore{}, nil, config.ReaperConfig{
		MaxConcurrent: 2,
		Interval:      config.Duration{Duration: 1 * time.Hour},
	})

	err := reaper.CheckSpawnAllowed(st)
	if err == nil {
		t.Fatal("expected CheckSpawnAllowed to return error when at max_concurrent")
	}

	if _, ok := err.(*controlplane.MaxConcurrentError); !ok {
		t.Fatalf("expected MaxConcurrentError, got %T: %v", err, err)
	}
}

func TestCheckSpawnAllowed_AllowsBelowLimit(t *testing.T) {
	st := &model.State{
		Agents: []model.AgentEntry{
			{Task: "agent-1", Status: model.StatusRunning, StartTime: time.Now()},
			{Task: "agent-2", Status: model.StatusStopped, StartTime: time.Now()},
		},
	}

	reaper := controlplane.NewReaper(&mockStore{}, nil, config.ReaperConfig{
		MaxConcurrent: 2,
		Interval:      config.Duration{Duration: 1 * time.Hour},
	})

	if err := reaper.CheckSpawnAllowed(st); err != nil {
		t.Fatalf("expected allow when below limit, got: %v", err)
	}
}

func TestCheckSpawnAllowed_AllowsNoLimit(t *testing.T) {
	st := &model.State{
		Agents: []model.AgentEntry{
			{Task: "a1", Status: model.StatusRunning, StartTime: time.Now()},
			{Task: "a2", Status: model.StatusRunning, StartTime: time.Now()},
			{Task: "a3", Status: model.StatusRunning, StartTime: time.Now()},
		},
	}

	reaper := controlplane.NewReaper(&mockStore{}, nil, config.ReaperConfig{
		MaxConcurrent: 0,
		Interval:      config.Duration{Duration: 1 * time.Hour},
	})

	if err := reaper.CheckSpawnAllowed(st); err != nil {
		t.Fatalf("expected allow with no limit, got: %v", err)
	}
}

// ========== Phase 1: Capability Enforcement Tests ==========

func TestResolveHost_NilDefault_ReturnsError(t *testing.T) {
	reg := host.NewMapRegistry("missing")
	// Don't register "missing" — Default() returns nil Host.
	orch := &Orchestrator{HostRegistry: reg}

	_, _, err := orch.resolveHost("")
	if err == nil {
		t.Fatal("expected error when default host is nil")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error %q should mention the missing key", err.Error())
	}
}

func TestSpawnStream_RejectsHostWithoutStreamCapability(t *testing.T) {
	root, store, wsMock := setupIntegrationTest(t)

	// NoopHost has Stream: false
	reg := host.NewMapRegistry("noop")
	reg.Register("noop", host.NoopHost{})

	orch := &Orchestrator{
		HostRegistry: reg,
		Workspace:    wsMock,
		Store:        store,
		Root:         root,
		Config: &config.Config{
			Project: config.ProjectConfig{AllowedTools: []string{"Bash(*)"}},
			Runtimes: map[string]config.RuntimeEntry{
				"claude":     {Adapter: "claude"},
				"noop":       {Adapter: "noop"},
				"batch-host": {Adapter: "batch", Model: "batch-model"},
			},
		},
	}

	err := orch.SpawnStream("test-task", "do stuff", "main", "", "", "", nil)
	if err == nil {
		t.Fatal("expected error for host without stream capability")
	}
	if !strings.Contains(err.Error(), "does not support streaming") {
		t.Errorf("error %q should mention streaming", err.Error())
	}
}

func TestSay_RejectsHostWithoutResumeTokenCapability(t *testing.T) {
	root, store, _ := setupIntegrationTest(t)

	// Seed an agent in the store that uses "noop" host type.
	err := store.WithLock(context.Background(), func(st *model.State) error {
		st.Agents = append(st.Agents, model.AgentEntry{
			Task:        "resume-test",
			RuntimeType:    "noop",
			ResumeToken: "some-token",
			Status:      model.StatusCompleted,
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	reg := host.NewMapRegistry("noop")
	reg.Register("noop", host.NoopHost{})

	orch := &Orchestrator{
		HostRegistry: reg,
		Store:        store,
		Root:         root,
	}

	_, err = orch.Say("resume-test", "hello")
	if err == nil {
		t.Fatal("expected error for host without ResumeToken capability")
	}
	if !strings.Contains(err.Error(), "does not support session resumption") {
		t.Errorf("error %q should mention session resumption", err.Error())
	}
}

func TestSayAsync_RejectsHostWithoutResumeTokenCapability(t *testing.T) {
	root, store, _ := setupIntegrationTest(t)

	err := store.WithLock(context.Background(), func(st *model.State) error {
		st.Agents = append(st.Agents, model.AgentEntry{
			Task:        "resume-async-test",
			RuntimeType:    "noop",
			ResumeToken: "some-token",
			Status:      model.StatusCompleted,
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	reg := host.NewMapRegistry("noop")
	reg.Register("noop", host.NoopHost{})

	orch := &Orchestrator{
		HostRegistry: reg,
		Store:        store,
		Root:         root,
	}

	err = orch.SayAsync("resume-async-test", "hello")
	if err == nil {
		t.Fatal("expected error for host without ResumeToken capability")
	}
	if !strings.Contains(err.Error(), "does not support session resumption") {
		t.Errorf("error %q should mention session resumption", err.Error())
	}
}

func TestPrepareSpawn_DegradesAgentMCP_WhenHostLacksCapability(t *testing.T) {
	root, store, wsMock := setupIntegrationTest(t)

	// NoopHost has AgentMCP: false
	reg := host.NewMapRegistry("noop")
	reg.Register("noop", host.NoopHost{})

	backend := &mockBackend{
		handle: &mockAgentHandle{output: []byte("{}"), startTime: time.Now()},
	}

	orch := &Orchestrator{
		HostRegistry: reg,
		Workspace:    wsMock,
		Store:        store,
		Backend:      backend,
		Root:         root,
		Config: &config.Config{
			Project: config.ProjectConfig{AllowedTools: []string{"Bash(*)"}},
			Runtimes: map[string]config.RuntimeEntry{
				"claude":     {Adapter: "claude"},
				"noop":       {Adapter: "noop"},
				"batch-host": {Adapter: "batch", Model: "batch-model"},
			},
		},
	}

	// prepareSpawn will fail at WriteWorkspace for NoopHost, but that's fine —
	// we can check the error message to confirm it got past capability enforcement.
	// Actually, NoopHost.WriteWorkspace returns ErrStreamNotSupported, so let's
	// use a custom batch-only host instead.
	// For this test we just verify the code path doesn't crash.
	// The detailed T22b/T22c tests will use a proper BatchOnlyHost fixture.
	resolved, err := orch.ResolveSpawn("noop", "", "", "main", "batch")
	if err != nil {
		t.Fatalf("ResolveSpawn failed: %v", err)
	}
	_, err = orch.prepareSpawn("degrade-test", "contract", resolved)
	// We expect failure from WriteWorkspace (NoopHost returns error), not from capability enforcement.
	if err == nil {
		t.Fatal("expected error (NoopHost.WriteWorkspace fails)")
	}
	// The error should be from WriteWorkspace, wrapped as "writing agent settings".
	if !strings.Contains(err.Error(), "writing agent settings") {
		t.Errorf("expected WriteWorkspace error, got: %v", err)
	}
}

func TestOrchestratorMCPServersPassthrough(t *testing.T) {
	dir := t.TempDir()
	fractaDir := filepath.Join(dir, ".fracta")
	if err := os.MkdirAll(fractaDir, 0755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(fractaDir, "state.db")
	ss, err := sqlitestore.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })

	mb := ss.Mailbox()
	reg := host.NewMapRegistry("claude")
	reg.Register("claude", claude.Host{})
	orch := New(reg, nil, ss, mb, dir)
	orch.MCPServers = config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"elastic-mcp": {
				Local: config.MCPServerLocal{
					Command: "npx",
					Args:    []string{"-y", "@elastic/mcp-server"},
				},
				Kubernetes: config.MCPServerKubernetes{
					URL: "http://elastic-mcp.fracta.svc.cluster.local:8080/sse",
				},
			},
		},
	}
	orch.RuntimeBackend = "local"

	worktree := t.TempDir()
	err = claude.Host{}.WriteWorkspace(worktree, nil, host.WorkspaceConfig{
		AgentMCP:    true,
		Servers:     orch.MCPServers,
		Backend:     orch.RuntimeBackend,
		ProjectRoot: dir,
	})
	if err != nil {
		t.Fatalf("claude.Host{}.WriteWorkspace: %v", err)
	}

	mcpData, err := os.ReadFile(filepath.Join(worktree, ".mcp.json"))
	if err != nil {
		t.Fatalf("reading .mcp.json: %v", err)
	}

	var mcpJSON struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(mcpData, &mcpJSON); err != nil {
		t.Fatalf("parsing .mcp.json: %v", err)
	}

	if _, ok := mcpJSON.MCPServers["elastic-mcp"]; !ok {
		t.Fatal("missing elastic-mcp in .mcp.json")
	}

	var elasticEntry map[string]interface{}
	json.Unmarshal(mcpJSON.MCPServers["elastic-mcp"], &elasticEntry)

	if elasticEntry["command"] != "npx" {
		t.Errorf("elastic-mcp command = %v, want npx", elasticEntry["command"])
	}
	if _, hasURL := elasticEntry["url"]; hasURL {
		t.Error("elastic-mcp should not have url in local mode")
	}

	// Test kubernetes mode
	worktreeK8s := t.TempDir()
	err = claude.Host{}.WriteWorkspace(worktreeK8s, nil, host.WorkspaceConfig{
		AgentMCP:    true,
		Servers:     orch.MCPServers,
		Backend:     "kubernetes",
		ProjectRoot: dir,
	})
	if err != nil {
		t.Fatalf("claude.Host{}.WriteWorkspace (k8s): %v", err)
	}

	mcpData, _ = os.ReadFile(filepath.Join(worktreeK8s, ".mcp.json"))
	json.Unmarshal(mcpData, &mcpJSON)

	var k8sEntry map[string]interface{}
	json.Unmarshal(mcpJSON.MCPServers["elastic-mcp"], &k8sEntry)

	if k8sEntry["url"] != "http://elastic-mcp.fracta.svc.cluster.local:8080/sse" {
		t.Errorf("elastic-mcp url = %v", k8sEntry["url"])
	}
	if _, hasCmd := k8sEntry["command"]; hasCmd {
		t.Error("elastic-mcp should not have command in kubernetes mode")
	}
}

// ========== BatchOnlyHost fixture (T22b) ==========

// batchOnlyHost implements host.Host as a minimal Tier-1 batch host.
// It has no streaming, no resume, no AgentMCP, no tool permissions.
// Used to prove that a non-Claude host works end-to-end without
// changing orchestrator or runtime code.
type batchOnlyHost struct{}

var _ host.Host = batchOnlyHost{}

func (batchOnlyHost) WriteWorkspace(string, []string, host.WorkspaceConfig) error {
	return nil // no-op — no workspace artifacts needed
}

func (batchOnlyHost) Bootstrap(task, baseBranch, contract string) host.BootstrapResult {
	return host.BootstrapResult{
		FileName:      "",                                 // no file artifact
		InitialPrompt: "run the task: " + task + "\n" + contract,
	}
}

func (batchOnlyHost) BuildBatchCommand(prompt, model, resumeToken string) host.CommandSpec {
	// Minimal: echo the prompt as output
	return host.CommandSpec{
		Command: "echo",
		Args:    []string{`{"result":"batch-done","session_id":"","is_error":false}`},
	}
}

func (batchOnlyHost) ParseBatchOutput(stdout []byte, waitErr error) (host.Result, error) {
	if waitErr != nil {
		return host.Result{}, waitErr
	}
	return host.Result{
		Output:  string(stdout),
		IsError: false,
	}, nil
}

func (batchOnlyHost) StartStream(string, string, string) (host.StreamSession, error) {
	return nil, host.ErrStreamNotSupported
}

func (batchOnlyHost) Capabilities() host.Capabilities {
	return host.Capabilities{
		Stream:           false,
		ResumeToken:      false,
		AgentMCP:         false,
		ToolPermissions:  false,
		StructuredEvents: false,
	}
}

// ========== T22: ResolveSpawn Integration Tests ==========

func TestResolveSpawn_WithConfig(t *testing.T) {
	root, store, wsMock := setupIntegrationTest(t)

	reg := host.NewMapRegistry("claude")
	reg.Register("claude", claude.Host{})

	orch := &Orchestrator{
		HostRegistry: reg,
		Workspace:    wsMock,
		Store:        store,
		Root:         root,
		Config: &config.Config{
			Project: config.ProjectConfig{
				DefaultBaseBranch: "develop",
				AllowedTools:      []string{"Read", "Write"},
			},
			Agents: config.AgentsConfig{
				DefaultRuntime: "claude",
				DefaultMode:     "batch",
			},
			Runtimes: map[string]config.RuntimeEntry{
				"claude": {
					Adapter: "claude",
					Model:   "test-model-from-config",
				},
			},
		},
	}

	resolved, err := orch.ResolveSpawn("", "", "", "", "")
	if err != nil {
		t.Fatalf("ResolveSpawn: %v", err)
	}

	if resolved.RuntimeType != "claude" {
		t.Errorf("HostType = %q, want %q", resolved.RuntimeType, "claude")
	}
	if resolved.Model != "test-model-from-config" {
		t.Errorf("Model = %q, want %q", resolved.Model, "test-model-from-config")
	}
	if resolved.BaseBranch != "develop" {
		t.Errorf("BaseBranch = %q, want %q", resolved.BaseBranch, "develop")
	}
	if resolved.Mode != "batch" {
		t.Errorf("Mode = %q, want %q", resolved.Mode, "batch")
	}
	if len(resolved.AllowedTools) != 2 || resolved.AllowedTools[0] != "Read" {
		t.Errorf("AllowedTools = %v, want [Read Write]", resolved.AllowedTools)
	}
}

func TestResolveSpawn_ExplicitOverridesDefaults(t *testing.T) {
	root, store, wsMock := setupIntegrationTest(t)

	reg := host.NewMapRegistry("claude")
	reg.Register("claude", claude.Host{})
	reg.Register("batch-host", batchOnlyHost{})

	orch := &Orchestrator{
		HostRegistry: reg,
		Workspace:    wsMock,
		Store:        store,
		Root:         root,
		Config: &config.Config{
			Agents: config.AgentsConfig{DefaultRuntime: "claude", DefaultMode: "batch"},
			Runtimes: map[string]config.RuntimeEntry{
				"claude":     {Model: "default-model"},
				"batch-host": {Model: "batch-model"},
			},
		},
	}

	resolved, err := orch.ResolveSpawn("batch-host", "explicit-model", "", "feature-branch", "batch")
	if err != nil {
		t.Fatalf("ResolveSpawn: %v", err)
	}

	if resolved.RuntimeType != "batch-host" {
		t.Errorf("HostType = %q, want %q", resolved.RuntimeType, "batch-host")
	}
	if resolved.Model != "explicit-model" {
		t.Errorf("Model = %q, want %q", resolved.Model, "explicit-model")
	}
	if resolved.BaseBranch != "feature-branch" {
		t.Errorf("BaseBranch = %q, want %q", resolved.BaseBranch, "feature-branch")
	}
}

func TestResolveSpawn_StreamModeRejectedForBatchHost(t *testing.T) {
	root, store, wsMock := setupIntegrationTest(t)

	reg := host.NewMapRegistry("batch-host")
	reg.Register("batch-host", batchOnlyHost{})

	orch := &Orchestrator{
		HostRegistry: reg,
		Workspace:    wsMock,
		Store:        store,
		Root:         root,
		Config: &config.Config{
			Runtimes: map[string]config.RuntimeEntry{
				"batch-host": {Model: "batch-model"},
			},
		},
	}

	_, err := orch.ResolveSpawn("", "", "", "", "stream")
	if err == nil {
		t.Fatal("expected error for stream mode on batch-only host")
	}
	if !strings.Contains(err.Error(), "does not support streaming") {
		t.Errorf("error %q should mention streaming", err.Error())
	}
}

func TestResolveSpawn_MissingHostConfig_ReturnsError(t *testing.T) {
	root, store, wsMock := setupIntegrationTest(t)

	reg := host.NewMapRegistry("claude")
	reg.Register("claude", claude.Host{})

	orch := &Orchestrator{
		HostRegistry: reg,
		Workspace:    wsMock,
		Store:        store,
		Root:         root,
		Config: &config.Config{
			Runtimes: map[string]config.RuntimeEntry{}, // empty — no hosts configured
		},
	}

	_, err := orch.ResolveSpawn("", "", "", "", "")
	if err == nil {
		t.Fatal("expected error when host config is missing")
	}
	if !strings.Contains(err.Error(), "not configured in fracta.yaml") {
		t.Errorf("error %q should mention fracta.yaml", err.Error())
	}
}

// ========== T22b: Batch-Only Host End-to-End ==========

func TestBatchOnlyHost_SpawnEndToEnd(t *testing.T) {
	root, store, wsMock := setupIntegrationTest(t)

	reg := host.NewMapRegistry("batch-host")
	reg.Register("batch-host", batchOnlyHost{})

	backend := &mockBackend{
		handle: &mockAgentHandle{
			output:    []byte(`{"result":"batch-done","session_id":"","is_error":false}`),
			startTime: time.Now(),
		},
	}

	orch := &Orchestrator{
		HostRegistry: reg,
		Workspace:    wsMock,
		Store:        store,
		Backend:      backend,
		Root:         root,
		Config: &config.Config{
			Project: config.ProjectConfig{AllowedTools: []string{"Bash(*)"}},
			Runtimes: map[string]config.RuntimeEntry{
				"claude":     {Adapter: "claude"},
				"noop":       {Adapter: "noop"},
				"batch-host": {Adapter: "batch", Model: "batch-model"},
			},
		},
	}

	// Spawn succeeds end-to-end with the batch-only host.
	err := orch.Spawn("batch-task", "do the work", "main", "", "", "")
	if err != nil {
		t.Fatalf("Spawn with batch-only host failed: %v", err)
	}

	if len(backend.spawnCalls) != 1 {
		t.Fatalf("expected 1 Spawn call, got %d", len(backend.spawnCalls))
	}

	// Verify the command came from batchOnlyHost.BuildBatchCommand
	if backend.spawnCalls[0].Command != "echo" {
		t.Errorf("Command = %q, want echo", backend.spawnCalls[0].Command)
	}

	// Agent should be recorded
	st, _ := store.Load(context.Background())
	if len(st.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(st.Agents))
	}
	if st.Agents[0].RuntimeType != "batch-host" {
		t.Errorf("HostType = %q, want batch-host", st.Agents[0].RuntimeType)
	}
}

// ========== T22c: No-Assumption Integration Tests ==========

func TestBatchOnlyHost_EmptyFileName_SkipsFileCreation(t *testing.T) {
	root, store, wsMock := setupIntegrationTest(t)

	reg := host.NewMapRegistry("batch-host")
	reg.Register("batch-host", batchOnlyHost{})

	backend := &mockBackend{
		handle: &mockAgentHandle{
			output:    []byte(`done`),
			startTime: time.Now(),
		},
	}

	orch := &Orchestrator{
		HostRegistry: reg,
		Workspace:    wsMock,
		Store:        store,
		Backend:      backend,
		Root:         root,
		Config: &config.Config{
			Project: config.ProjectConfig{AllowedTools: []string{"Bash(*)"}},
			Runtimes: map[string]config.RuntimeEntry{
				"claude":     {Adapter: "claude"},
				"noop":       {Adapter: "noop"},
				"batch-host": {Adapter: "batch", Model: "batch-model"},
			},
		},
	}

	// Spawn with contract content: batchOnlyHost.Bootstrap returns FileName="",
	// so no file should be created in the workspace.
	err := orch.Spawn("no-file-task", "some contract", "main", "", "", "")
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	// Verify no CLAUDE.md or TASK.md was created in the workspace
	wsPath := filepath.Join(root, ".worktrees", "no-file-task")
	entries, _ := os.ReadDir(wsPath)
	for _, e := range entries {
		if e.Name() == "CLAUDE.md" || e.Name() == "TASK.md" {
			t.Errorf("unexpected file %q in workspace — batch-only host should not create task files", e.Name())
		}
	}
}

func TestBatchOnlyHost_AgentMCPDegraded(t *testing.T) {
	root, store, wsMock := setupIntegrationTest(t)

	reg := host.NewMapRegistry("batch-host")
	reg.Register("batch-host", batchOnlyHost{})

	backend := &mockBackend{
		handle: &mockAgentHandle{
			output:    []byte(`done`),
			startTime: time.Now(),
		},
	}

	orch := &Orchestrator{
		HostRegistry: reg,
		Workspace:    wsMock,
		Store:        store,
		Backend:      backend,
		Root:         root,
		Config: &config.Config{
			Project: config.ProjectConfig{AllowedTools: []string{"Bash(*)"}},
			Runtimes: map[string]config.RuntimeEntry{
				"claude":     {Adapter: "claude"},
				"noop":       {Adapter: "noop"},
				"batch-host": {Adapter: "batch", Model: "batch-model"},
			},
		},
	}

	// Spawn should succeed — AgentMCP should be degraded to false
	// (no crash from missing MCP support).
	err := orch.Spawn("mcp-degrade-task", "", "main", "", "", "")
	if err != nil {
		t.Fatalf("Spawn failed (should degrade AgentMCP): %v", err)
	}
}

func TestBatchOnlyHost_SayFailsCleanly(t *testing.T) {
	root, store, _ := setupIntegrationTest(t)

	// Seed an agent that uses the batch-only host
	err := store.WithLock(context.Background(), func(st *model.State) error {
		st.Agents = append(st.Agents, model.AgentEntry{
			Task:        "batch-say-test",
			RuntimeType:    "batch-host",
			ResumeToken: "fake-token",
			Status:      model.StatusCompleted,
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	reg := host.NewMapRegistry("batch-host")
	reg.Register("batch-host", batchOnlyHost{})

	orch := &Orchestrator{
		HostRegistry: reg,
		Store:        store,
		Root:         root,
	}

	// Say should fail cleanly with capability error
	_, err = orch.Say("batch-say-test", "hello")
	if err == nil {
		t.Fatal("expected error — batch-only host does not support session resumption")
	}
	if !strings.Contains(err.Error(), "does not support session resumption") {
		t.Errorf("error %q should mention session resumption", err.Error())
	}
}

func TestBatchOnlyHost_StreamSpawnFailsCleanly(t *testing.T) {
	root, store, wsMock := setupIntegrationTest(t)

	reg := host.NewMapRegistry("batch-host")
	reg.Register("batch-host", batchOnlyHost{})

	orch := &Orchestrator{
		HostRegistry: reg,
		Workspace:    wsMock,
		Store:        store,
		Root:         root,
		Config: &config.Config{
			Project: config.ProjectConfig{AllowedTools: []string{"Bash(*)"}},
			Runtimes: map[string]config.RuntimeEntry{
				"claude":     {Adapter: "claude"},
				"noop":       {Adapter: "noop"},
				"batch-host": {Adapter: "batch", Model: "batch-model"},
			},
		},
	}

	// SpawnStream should fail cleanly
	err := orch.SpawnStream("stream-fail-test", "task", "main", "", "", "", nil)
	if err == nil {
		t.Fatal("expected error — batch-only host does not support streaming")
	}
	if !strings.Contains(err.Error(), "does not support streaming") {
		t.Errorf("error %q should mention streaming", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Say with Backend: HostEnv flows through Backend.Spawn
// ---------------------------------------------------------------------------

func TestSay_BackendReceivesHostEnv(t *testing.T) {
	root, store, _ := setupIntegrationTest(t)

	// Seed an agent with a resume token.
	err := store.WithLock(context.Background(), func(st *model.State) error {
		st.Agents = append(st.Agents, model.AgentEntry{
			Task:          "env-test",
			RuntimeType:      "claude",
			ResumeToken:   "tok-123",
			WorkspacePath: root,
			Status:        model.StatusCompleted,
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	backend := &mockBackend{
		handle: &mockAgentHandle{
			output:    []byte(`{"session_id":"tok-456","result":"done","is_error":false}`),
			startTime: time.Now(),
		},
	}

	reg := host.NewMapRegistry("claude")
	reg.Register("claude", claude.Host{})

	orch := &Orchestrator{
		HostRegistry:   reg,
		Store:          store,
		Backend:        backend,
		Root:           root,
		RuntimeBackend: "kubernetes",
		Config: &config.Config{
			Project: config.ProjectConfig{AllowedTools: []string{"Bash(*)"}},
			Runtimes: map[string]config.RuntimeEntry{
				"claude": {
					Adapter: "claude",
					Kubernetes: config.HostKubernetesConfig{
						Env: []config.HostEnvVar{
							{Name: "TEST_TOKEN", Value: "secret-value"},
						},
					},
				},
			},
		},
	}
	orch.Mailbox = store.Mailbox()

	output, err := orch.Say("env-test", "follow-up message")
	if err != nil {
		t.Fatalf("Say failed: %v", err)
	}
	if output != "done" {
		t.Errorf("output = %q, want 'done'", output)
	}

	// Verify Backend.Spawn was called (not direct exec).
	if len(backend.spawnCalls) != 1 {
		t.Fatalf("expected 1 Backend.Spawn call, got %d", len(backend.spawnCalls))
	}

	// Verify HostEnv was passed through.
	opts := backend.spawnCalls[0]
	if len(opts.HostEnv) != 1 {
		t.Fatalf("HostEnv len = %d, want 1", len(opts.HostEnv))
	}
	if opts.HostEnv[0].Name != "TEST_TOKEN" || opts.HostEnv[0].Value != "secret-value" {
		t.Errorf("HostEnv[0] = {%q, %q}, want {TEST_TOKEN, secret-value}",
			opts.HostEnv[0].Name, opts.HostEnv[0].Value)
	}

	// Verify the resume token was updated.
	agent, _ := store.FindAgent(context.Background(), "env-test")
	if agent.ResumeToken != "tok-456" {
		t.Errorf("ResumeToken = %q, want tok-456", agent.ResumeToken)
	}
}

// ---------------------------------------------------------------------------
// Say with Backend: SecretRef HostEnv flows through to Backend.Spawn
// ---------------------------------------------------------------------------

func TestSay_BackendReceivesSecretRefHostEnv(t *testing.T) {
	root, store, _ := setupIntegrationTest(t)

	err := store.WithLock(context.Background(), func(st *model.State) error {
		st.Agents = append(st.Agents, model.AgentEntry{
			Task:          "secret-env-test",
			RuntimeType:      "claude",
			ResumeToken:   "tok-sec",
			WorkspacePath: root,
			Status:        model.StatusCompleted,
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	backend := &mockBackend{
		handle: &mockAgentHandle{
			output:    []byte(`{"session_id":"tok-sec2","result":"ok","is_error":false}`),
			startTime: time.Now(),
		},
	}

	reg := host.NewMapRegistry("claude")
	reg.Register("claude", claude.Host{})

	orch := &Orchestrator{
		HostRegistry:   reg,
		Store:          store,
		Backend:        backend,
		Root:           root,
		RuntimeBackend: "kubernetes",
		Config: &config.Config{
			Project: config.ProjectConfig{AllowedTools: []string{"Bash(*)"}},
			Runtimes: map[string]config.RuntimeEntry{
				"claude": {
					Adapter: "claude",
					Kubernetes: config.HostKubernetesConfig{
						Env: []config.HostEnvVar{
							{Name: "TEST_PLAIN", Value: "plain-val"},
							{Name: "TEST_SECRET", SecretRef: &config.HostSecretRef{
								Name: "my-k8s-secret",
								Key:  "api-key",
							}},
						},
					},
				},
			},
		},
	}
	orch.Mailbox = store.Mailbox()

	_, err = orch.Say("secret-env-test", "resume with secrets")
	if err != nil {
		t.Fatalf("Say failed: %v", err)
	}

	if len(backend.spawnCalls) != 1 {
		t.Fatalf("expected 1 Backend.Spawn call, got %d", len(backend.spawnCalls))
	}

	opts := backend.spawnCalls[0]
	if len(opts.HostEnv) != 2 {
		t.Fatalf("HostEnv len = %d, want 2", len(opts.HostEnv))
	}

	// First entry: plain value.
	if opts.HostEnv[0].Name != "TEST_PLAIN" || opts.HostEnv[0].Value != "plain-val" {
		t.Errorf("HostEnv[0] = {%q, %q}, want {TEST_PLAIN, plain-val}",
			opts.HostEnv[0].Name, opts.HostEnv[0].Value)
	}

	// Second entry: SecretRef (K8s backend will render as secretKeyRef).
	if opts.HostEnv[1].Name != "TEST_SECRET" {
		t.Errorf("HostEnv[1].Name = %q, want TEST_SECRET", opts.HostEnv[1].Name)
	}
	if opts.HostEnv[1].SecretRef == nil {
		t.Fatal("HostEnv[1].SecretRef should not be nil")
	}
	if opts.HostEnv[1].SecretRef.Name != "my-k8s-secret" || opts.HostEnv[1].SecretRef.Key != "api-key" {
		t.Errorf("SecretRef = {%q, %q}, want {my-k8s-secret, api-key}",
			opts.HostEnv[1].SecretRef.Name, opts.HostEnv[1].SecretRef.Key)
	}
}

// ---------------------------------------------------------------------------
// SayAsync with Backend: rollback on state-write failure
// ---------------------------------------------------------------------------

func TestSayAsync_BackendRollbackOnStateFailure(t *testing.T) {
	root, store, _ := setupIntegrationTest(t)

	// Seed agent.
	err := store.WithLock(context.Background(), func(st *model.State) error {
		st.Agents = append(st.Agents, model.AgentEntry{
			Task:          "rollback-test",
			RuntimeType:      "claude",
			ResumeToken:   "tok-abc",
			WorkspacePath: root,
			Status:        model.StatusIdle,
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	backend := &mockBackend{
		handle: &mockAgentHandle{
			output:    []byte(`{"session_id":"tok-def","result":"ok","is_error":false}`),
			startTime: time.Now(),
		},
	}

	reg := host.NewMapRegistry("claude")
	reg.Register("claude", claude.Host{})

	// Use a store wrapper that fails UpdateAgentStatus.
	failStore := &statusFailStore{Store: store}

	orch := &Orchestrator{
		HostRegistry:   reg,
		Store:          failStore,
		Backend:        backend,
		Root:           root,
		RuntimeBackend: "local",
		Config: &config.Config{
			Project: config.ProjectConfig{AllowedTools: []string{"Bash(*)"}},
			Runtimes: map[string]config.RuntimeEntry{
				"claude": {Adapter: "claude"},
			},
		},
		Logger: slog.Default(),
	}
	orch.Mailbox = store.Mailbox()

	err = orch.SayAsync("rollback-test", "follow-up")
	if err == nil {
		t.Fatal("expected error from UpdateAgentStatus failure")
	}
	if !strings.Contains(err.Error(), "updating state") {
		t.Errorf("error %q should mention state update", err.Error())
	}

	// Verify Backend.Kill was called (rollback).
	if len(backend.killCalls) != 1 {
		t.Fatalf("expected 1 Kill call (rollback), got %d", len(backend.killCalls))
	}
	if backend.killCalls[0] != "rollback-test-say" {
		t.Errorf("Kill ID = %q, want rollback-test-say", backend.killCalls[0])
	}
}

// statusFailStore wraps a Store and makes UpdateAgentStatus always fail.
type statusFailStore struct {
	state.Store
}

func (s *statusFailStore) UpdateAgentStatus(_ context.Context, _ string, _ model.AgentStatus, _ string) error {
	return fmt.Errorf("injected status update failure")
}
