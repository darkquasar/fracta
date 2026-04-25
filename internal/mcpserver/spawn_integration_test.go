package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/agentlifecycle"
	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/controlplane"
	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/orchestrator"
	"github.com/darkquasar/fracta/internal/queue"
	"github.com/darkquasar/fracta/internal/runtime"
	"github.com/darkquasar/fracta/internal/state/sqlitestore"
	"github.com/darkquasar/fracta/internal/worker"
	"github.com/darkquasar/fracta/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
)

// ---------------------------------------------------------------------------
// Fake host that records calls and produces predictable outputs.
// ---------------------------------------------------------------------------

type recordingHost struct {
	writeWorkspaceCalled bool
	bootstrapCalled      bool
	buildBatchCalled     bool
	parseCalled          bool

	lastWorkdir      string
	lastAllowedTools []string
	lastWsCfg        host.WorkspaceConfig
	lastBootTask     string
	lastBootBase     string
	lastBootContract string

	writeErr    error
	bootstrap   host.BootstrapResult
	commandSpec host.CommandSpec
	parseResult host.Result
	parseErr    error
}

func (r *recordingHost) WriteWorkspace(workdir string, allowedTools []string, cfg host.WorkspaceConfig) error {
	r.writeWorkspaceCalled = true
	r.lastWorkdir = workdir
	r.lastAllowedTools = allowedTools
	r.lastWsCfg = cfg
	return r.writeErr
}

func (r *recordingHost) Bootstrap(task, baseBranch, contract string) host.BootstrapResult {
	r.bootstrapCalled = true
	r.lastBootTask = task
	r.lastBootBase = baseBranch
	r.lastBootContract = contract
	return r.bootstrap
}

func (r *recordingHost) BuildBatchCommand(prompt, modelID, resumeToken string) host.CommandSpec {
	r.buildBatchCalled = true
	return r.commandSpec
}

func (r *recordingHost) ParseBatchOutput(stdout []byte, waitErr error) (host.Result, error) {
	r.parseCalled = true
	return r.parseResult, r.parseErr
}

func (r *recordingHost) StartStream(_, _, _ string) (host.StreamSession, error) {
	return nil, host.ErrStreamNotSupported
}

func (r *recordingHost) Capabilities() host.Capabilities {
	return host.Capabilities{}
}

var _ host.Host = (*recordingHost)(nil)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func testSQLiteStore(t *testing.T) *sqlitestore.SQLiteStore {
	t.Helper()
	s, err := sqlitestore.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// testFixture holds the ControlPlane and config used in test setup, allowing
// test options to modify config before the LocalControlPlaneClient is built.
type testFixture struct {
	cp          *controlplane.ControlPlane
	root        string
	configPath  string
	graphAddr   string
	strategyDir string
}

type testServerOpt func(*testFixture)

func withAgentWiring(configPath, graphAddr, strategyDir string) testServerOpt {
	return func(f *testFixture) {
		f.configPath = configPath
		f.graphAddr = graphAddr
		f.strategyDir = strategyDir
	}
}

// withProject sets the project config on the test fixture.
func withProject(pc config.ProjectConfig) testServerOpt {
	return func(f *testFixture) {
		f.cp.Config.Project = pc
	}
}

// withMCPServers sets MCP servers config on the test fixture.
func withMCPServers(mcpServers config.MCPServersConfig) testServerOpt {
	return func(f *testFixture) {
		f.cp.Config.MCPServers = mcpServers
	}
}

// withHosts adds runtime configs to the test fixture so ResolveSpawn can look
// them up via EffectiveRuntimes.
func withHosts(hosts map[string]config.RuntimeEntry) testServerOpt {
	return func(f *testFixture) {
		if f.cp.Config.Agents.AgentRuntimes == nil {
			f.cp.Config.Agents.AgentRuntimes = make(map[string]config.RuntimeEntry)
		}
		for k, v := range hosts {
			f.cp.Config.Agents.AgentRuntimes[k] = v
		}
	}
}

// buildTestServer constructs a Server with minimal wiring sufficient for
// handleSpawn tests. Uses MemoryQueue, SQLiteStore, and the given host registry.
// All admin tools route through a LocalControlPlaneClient.
func buildTestServer(t *testing.T, store *sqlitestore.SQLiteStore, q queue.MissionQueue, hostReg host.HostRegistry, opts ...testServerOpt) *Server {
	t.Helper()

	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".fracta"), 0755)

	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Backend: "local",
			Queue:   config.QueueConfig{Backend: "memory"},
		},
		Reaper: config.ReaperConfig{
			Interval: config.Duration{Duration: 1 * time.Hour},
			MaxAge:   config.Duration{Duration: 1 * time.Hour},
		},
	}

	backend := runtime.NewLocalBackend()
	profile := controlplane.ResolveProfile(cfg, root)
	reaper := controlplane.NewReaper(store, backend, cfg.Reaper)
	if q != nil {
		reaper.SetQueue(q)
	}
	reaper.SetMailbox(store.Mailbox())
	reaper.Start()
	t.Cleanup(func() { reaper.Stop() })

	ws := workspace.NewDirectoryWorkspace(filepath.Join(root, "agents"))

	cp := &controlplane.ControlPlane{
		Backend:   backend,
		Store:     store,
		Mailbox:   store.Mailbox(),
		Workspace: ws,
		Queue:     q,
		Profile:   profile,
		Reaper:    reaper,
		Config:    cfg,
	}

	fixture := &testFixture{cp: cp, root: root}

	// Apply test options that modify config before building the client.
	for _, o := range opts {
		o(fixture)
	}

	// Build LocalControlPlaneClient.
	clientOpts := []cpapi.LocalClientOption{
		cpapi.WithProcessRegistry(orchestrator.NewProcessRegistry()),
	}
	if hostReg != nil {
		clientOpts = append(clientOpts, cpapi.WithRuntimeRegistry(hostReg))
	}
	clientOpts = append(clientOpts, cpapi.WithAgentWiring(fixture.configPath, fixture.graphAddr, fixture.strategyDir))
	cpClient := cpapi.NewLocalControlPlaneClient(cp, root, clientOpts...)

	s := &Server{
		root:     root,
		store:    store,
		mailbox:  store.Mailbox(),
		registry: orchestrator.NewProcessRegistry(),
		cpClient: cpClient,
	}

	return s
}

// makeMCPRequest builds a mcp.CallToolRequest with the given parameters.
func makeMCPRequest(params map[string]interface{}) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: params,
		},
	}
}

// ---------------------------------------------------------------------------
// Test 1: MCP handler queued e2e
//
// Exercise handleSpawn with dispatch=queued end-to-end. Verify the enqueued
// Mission.Payload contains AllowedTools, MCPServers, ConfigPath, GraphAddr,
// StrategyDir, and the correct HostType.
// ---------------------------------------------------------------------------

func TestHandleSpawn_QueuedE2E_PayloadFields(t *testing.T) {
	store := testSQLiteStore(t)
	q := queue.NewMemoryQueue(store, 10)
	t.Cleanup(func() { q.Close() })

	fakeReg := host.NewMapRegistry("test-host")
	fakeReg.Register("test-host", &recordingHost{})

	s := buildTestServer(t, store, q, fakeReg,
		withAgentWiring("/etc/fracta/config.yaml", "localhost:6379", "/opt/strategies"),
		withHosts(map[string]config.RuntimeEntry{"test-host": {
			Adapter: "test",
			Model:   "claude-sonnet-4-5-20250514",
		}}),
		withProject(config.ProjectConfig{
			AllowedTools: []string{"Bash(*)", "Read", "Write"},
		}),
		withMCPServers(config.MCPServersConfig{
			Servers: map[string]config.MCPServerEntry{
				"vendor": {Local: config.MCPServerLocal{Command: "vendor-mcp", Args: []string{"serve"}}},
			},
		}),
	)

	result, err := s.handleSpawn(context.Background(), makeMCPRequest(map[string]interface{}{
		"task":     "int-test-1",
		"contract": "Do something useful",
		"dispatch": "queued",
		"runtime":  "test-host",
	}))
	if err != nil {
		t.Fatalf("handleSpawn error: %v", err)
	}

	// Verify queued response.
	text := result.Content[0].(mcp.TextContent).Text
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["status"] != "Queued" {
		t.Fatalf("status = %v, want Queued", resp["status"])
	}

	// Dequeue and verify payload.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mission, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}

	var payload queue.MissionPayload
	if err := json.Unmarshal(mission.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	// Assert every required field.
	assertEqual(t, "Task", payload.Task, "int-test-1")
	assertEqual(t, "Contract", payload.Contract, "Do something useful")
	assertEqual(t, "HostType", payload.RuntimeType, "test-host")
	assertEqual(t, "Model", payload.Model, "claude-sonnet-4-5-20250514")
	assertEqual(t, "ConfigPath", payload.ConfigPath, "/etc/fracta/config.yaml")
	assertEqual(t, "GraphAddr", payload.GraphAddr, "localhost:6379")
	assertEqual(t, "StrategyDir", payload.StrategyDir, "/opt/strategies")

	if len(payload.AllowedTools) != 3 {
		t.Errorf("AllowedTools len = %d, want 3; got %v", len(payload.AllowedTools), payload.AllowedTools)
	}

	// MCPServers should be serialized.
	if payload.MCPServers == nil {
		t.Fatal("MCPServers is nil")
	}
	var mcpCfg config.MCPServersConfig
	if err := json.Unmarshal(payload.MCPServers, &mcpCfg); err != nil {
		t.Fatalf("unmarshal MCPServers: %v", err)
	}
	if _, ok := mcpCfg.Servers["vendor"]; !ok {
		t.Error("MCPServers missing 'vendor' entry")
	}

	// Agent persisted in store with correct fields.
	agent, err := store.FindAgent(ctx, "int-test-1")
	if err != nil {
		t.Fatalf("FindAgent: %v", err)
	}
	if agent == nil {
		t.Fatal("agent not found in store")
	}
	if agent.Status != model.StatusQueued {
		t.Errorf("agent.Status = %q, want %q", agent.Status, model.StatusQueued)
	}
	if agent.RuntimeType != "test-host" {
		t.Errorf("agent.RuntimeType = %q, want %q", agent.RuntimeType, "test-host")
	}
}

// ---------------------------------------------------------------------------
// Test 2: MCP handler direct e2e — verify correct host is resolved.
//
// We verify that handleQueuedSpawn's host validation path calls
// HostRegistry.Get with the right host_type, and that newOrchestrator
// threads the registry correctly. We also test the queued path's
// fail-fast behavior when an unknown host is specified.
// ---------------------------------------------------------------------------

func TestHandleSpawn_HostResolution(t *testing.T) {
	store := testSQLiteStore(t)
	q := queue.NewMemoryQueue(store, 10)
	t.Cleanup(func() { q.Close() })

	hostAlpha := &recordingHost{}
	hostBeta := &recordingHost{}

	reg := host.NewMapRegistry("alpha")
	reg.Register("alpha", hostAlpha)
	reg.Register("beta", hostBeta)

	s := buildTestServer(t, store, q, reg,
		withHosts(map[string]config.RuntimeEntry{
			"alpha": {Adapter: "alpha"},
			"beta":  {Adapter: "beta"},
		}),
	)
	os.WriteFile(filepath.Join(s.root, ".fracta", "config.json"), []byte(`{}`), 0644)

	t.Run("explicit host_type selects correct host", func(t *testing.T) {
		result, err := s.handleSpawn(context.Background(), makeMCPRequest(map[string]interface{}{
			"task":     "host-beta-test",
			"dispatch": "queued",
			"runtime":  "beta",
		}))
		if err != nil {
			t.Fatalf("handleSpawn error: %v", err)
		}

		text := result.Content[0].(mcp.TextContent).Text
		var resp map[string]interface{}
		json.Unmarshal([]byte(text), &resp)
		if resp["status"] != "Queued" {
			t.Fatalf("status = %v, want Queued", resp["status"])
		}

		// Dequeue and verify the payload has host_type=beta.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		m, err := q.Dequeue(ctx)
		if err != nil {
			t.Fatalf("dequeue: %v", err)
		}
		var p queue.MissionPayload
		json.Unmarshal(m.Payload, &p)
		if p.RuntimeType != "beta" {
			t.Errorf("HostType = %q, want %q", p.RuntimeType, "beta")
		}
	})

	t.Run("default host_type when omitted", func(t *testing.T) {
		result, err := s.handleSpawn(context.Background(), makeMCPRequest(map[string]interface{}{
			"task":     "host-default-test",
			"dispatch": "queued",
		}))
		if err != nil {
			t.Fatalf("handleSpawn error: %v", err)
		}

		text := result.Content[0].(mcp.TextContent).Text
		var resp map[string]interface{}
		json.Unmarshal([]byte(text), &resp)
		if resp["status"] != "Queued" {
			t.Fatalf("status = %v, want Queued", resp["status"])
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		m, err := q.Dequeue(ctx)
		if err != nil {
			t.Fatalf("dequeue: %v", err)
		}
		var p queue.MissionPayload
		json.Unmarshal(m.Payload, &p)

		// Default key is "alpha".
		if p.RuntimeType != "alpha" {
			t.Errorf("HostType = %q, want %q (default)", p.RuntimeType, "alpha")
		}
	})

	t.Run("unknown host_type fails fast", func(t *testing.T) {
		result, err := s.handleSpawn(context.Background(), makeMCPRequest(map[string]interface{}{
			"task":     "host-unknown-test",
			"dispatch": "queued",
			"runtime":  "nonexistent",
		}))
		if err != nil {
			t.Fatalf("handleSpawn error: %v", err)
		}

		text := result.Content[0].(mcp.TextContent).Text
		if !isErrorResult(result) {
			t.Errorf("expected error result for unknown host, got: %s", text)
		}
	})
}

// ---------------------------------------------------------------------------
// Test 3: Worker workspace e2e
//
// Dequeue a real mission from MemoryQueue, execute through the worker's
// full path with a fake host. Verify workspace config fields are threaded
// correctly (ConfigPath, GraphAddr, StrategyDir) and the task file is written.
// ---------------------------------------------------------------------------

func TestWorker_WorkspaceE2E(t *testing.T) {
	store := testSQLiteStore(t)
	q := queue.NewMemoryQueue(store, 10)
	t.Cleanup(func() { q.Close() })

	wsBase := t.TempDir()

	fh := &recordingHost{
		bootstrap: host.BootstrapResult{
			FileName:      "CLAUDE.md",
			FileBody:      "# Task\nBuild the widget.",
			InitialPrompt: "Read CLAUDE.md and execute.",
		},
		commandSpec: host.CommandSpec{Command: "echo", Args: []string{"done"}},
		parseResult: host.Result{Output: "completed"},
	}

	reg := host.NewMapRegistry("test-host")
	reg.Register("test-host", fh)

	// Build a payload with all config fields.
	mcpServers := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"elk": {Local: config.MCPServerLocal{Command: "elk-mcp", Args: []string{"serve"}}},
		},
	}
	mcpJSON, _ := json.Marshal(mcpServers)

	payload := queue.MissionPayload{
		Task:         "widget-builder",
		Contract:     "# Task\nBuild the widget.",
		BaseBranch:   "main",
		Model:        "claude-sonnet-4-5-20250514",
		RuntimeType:  "test-host",
		AllowedTools: []string{"Bash(*)", "Read", "Write"},
		MCPServers:   mcpJSON,
		Backend:      "local",
		ConfigPath:   "/etc/fracta/config.yaml",
		GraphAddr:    "localhost:6379",
		StrategyDir:  "/opt/strategies",
	}
	payloadBytes, _ := json.Marshal(payload)

	mission := &queue.Mission{AgentTask: "widget-builder", Payload: payloadBytes}
	agent := &model.AgentEntry{
		Task:        "widget-builder",
		RuntimeType: "test-host",
		Status:      model.StatusQueued,
		Mode:        "queued",
	}

	ctx := context.Background()
	if err := q.Enqueue(ctx, mission, agent); err != nil {
		t.Fatal(err)
	}

	w := worker.New("test-worker", q, store, reg, wsBase,
		worker.WithPollInterval(50*time.Millisecond),
		worker.WithBackend(runtime.NewLocalBackend()),
		worker.WithLifecycle(agentlifecycle.New(store, events.NoopBus{})))

	// Run the worker in background. It will dequeue, execute, and block waiting
	// for the next mission.
	runCtx, runCancel := context.WithTimeout(ctx, 10*time.Second)
	defer runCancel()

	done := make(chan error, 1)
	go func() { done <- w.Run(runCtx) }()

	// Poll for completion.
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for mission completion")
		default:
		}

		a, err := store.FindAgent(ctx, "widget-builder")
		if err != nil {
			t.Fatal(err)
		}
		if a != nil && (a.Status == model.StatusCompleted || a.Status == model.StatusFailed) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	runCancel()
	<-done

	// Verify the host received correct workspace config via the worker path.
	if !fh.writeWorkspaceCalled {
		t.Fatal("WriteWorkspace was not called")
	}
	if !fh.bootstrapCalled {
		t.Fatal("Bootstrap was not called")
	}

	// WorkspaceConfig fields should be threaded from the payload.
	if fh.lastWsCfg.ConfigPath != "/etc/fracta/config.yaml" {
		t.Errorf("wsCfg.ConfigPath = %q, want %q", fh.lastWsCfg.ConfigPath, "/etc/fracta/config.yaml")
	}
	if fh.lastWsCfg.GraphAddr != "localhost:6379" {
		t.Errorf("wsCfg.GraphAddr = %q, want %q", fh.lastWsCfg.GraphAddr, "localhost:6379")
	}
	if fh.lastWsCfg.StrategyDir != "/opt/strategies" {
		t.Errorf("wsCfg.StrategyDir = %q, want %q", fh.lastWsCfg.StrategyDir, "/opt/strategies")
	}
	if fh.lastWsCfg.Backend != "local" {
		t.Errorf("wsCfg.Backend = %q, want %q", fh.lastWsCfg.Backend, "local")
	}

	// AllowedTools should be passed through.
	if len(fh.lastAllowedTools) != 3 {
		t.Errorf("AllowedTools len = %d, want 3; got %v", len(fh.lastAllowedTools), fh.lastAllowedTools)
	}

	// Bootstrap should receive the contract and task.
	if fh.lastBootContract != "# Task\nBuild the widget." {
		t.Errorf("bootstrap contract = %q, want matching", fh.lastBootContract)
	}
	if fh.lastBootTask != "widget-builder" {
		t.Errorf("bootstrap task = %q, want %q", fh.lastBootTask, "widget-builder")
	}

	// Agent should be completed in the store.
	a, err := store.FindAgent(ctx, "widget-builder")
	if err != nil {
		t.Fatalf("FindAgent: %v", err)
	}
	if a.Status != model.StatusCompleted {
		t.Errorf("agent status = %q, want %q", a.Status, model.StatusCompleted)
	}
}

// ---------------------------------------------------------------------------
// Test 4: Config source — spawn reads from SpawnCfg, NOT state.ReadConfig.
//
// Verify that spawn reads model from Config.Hosts (fracta.yaml), not legacy config.
// ---------------------------------------------------------------------------

func TestHandleSpawn_ConfigSource_HostConfigNotLegacy(t *testing.T) {
	store := testSQLiteStore(t)
	q := queue.NewMemoryQueue(store, 10)
	t.Cleanup(func() { q.Close() })

	fakeReg := host.NewMapRegistry("test-host")
	fakeReg.Register("test-host", &recordingHost{})

	s := buildTestServer(t, store, q, fakeReg,
		withHosts(map[string]config.RuntimeEntry{"test-host": {
			Adapter: "test",
			Model:   "from-host-config",
		}}),
		withProject(config.ProjectConfig{
			AllowedTools: []string{"Bash(*)"},
		}),
	)

	result, err := s.handleSpawn(context.Background(), makeMCPRequest(map[string]interface{}{
		"task":     "config-source-test",
		"contract": "test contract",
		"dispatch": "queued",
		"runtime":  "test-host",
	}))
	if err != nil {
		t.Fatalf("handleSpawn error: %v", err)
	}

	text := result.Content[0].(mcp.TextContent).Text
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["status"] != "Queued" {
		t.Fatalf("status = %v, want Queued; response: %s", resp["status"], text)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mission, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}

	var payload queue.MissionPayload
	if err := json.Unmarshal(mission.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	// Model must come from the configured runtime entry.
	if payload.Model != "from-host-config" {
		t.Errorf("Model = %q, want %q — must come from hosts config", payload.Model, "from-host-config")
	}
}

// ---------------------------------------------------------------------------
// Test 5 (T10b): handleSay uses host capabilities, not backend type.
//
// Verify that handleSay checks h.Capabilities().ResumeToken instead of
// checking if the backend is "kubernetes". Now routed through cpClient.
// ---------------------------------------------------------------------------

func TestHandleSay_CapabilityCheck(t *testing.T) {
	store := testSQLiteStore(t)

	// Host with no resume capability.
	noResumeHost := &recordingHost{}
	reg := host.NewMapRegistry("no-resume")
	reg.Register("no-resume", noResumeHost)

	s := buildTestServer(t, store, nil, reg)

	// Insert an agent record so the lookup succeeds.
	ctx := context.Background()
	store.WithLock(ctx, func(st *model.State) error {
		st.Agents = append(st.Agents, model.AgentEntry{
			Task:        "test-agent",
			RuntimeType: "no-resume",
			Status:      model.StatusCompleted,
		})
		return nil
	})

	result, err := s.handleSay(ctx, makeMCPRequest(map[string]interface{}{
		"name":    "test-agent",
		"message": "hello",
	}))
	if err != nil {
		t.Fatalf("handleSay error: %v", err)
	}

	// The cpClient path (via LocalControlPlaneClient) checks capabilities
	// and returns an error for hosts without ResumeToken.
	if !isErrorResult(result) {
		t.Fatal("expected error result for host without ResumeToken capability")
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "does not support session resumption") {
		t.Errorf("error message = %q, want mention of session resumption", text)
	}
}

// ---------------------------------------------------------------------------
// Test 6 (T10b): queued default host comes from registry, not hardcoded.
//
// Verify that handleQueuedSpawn uses HostRegistry.Default() to get the
// default host type, not a hardcoded "claude" string.
// ---------------------------------------------------------------------------

func TestHandleQueuedSpawn_DefaultHostFromRegistry(t *testing.T) {
	store := testSQLiteStore(t)
	q := queue.NewMemoryQueue(store, 10)
	t.Cleanup(func() { q.Close() })

	// Registry default is "custom-host", NOT "claude".
	customHost := &recordingHost{}
	reg := host.NewMapRegistry("custom-host")
	reg.Register("custom-host", customHost)

	s := buildTestServer(t, store, q, reg,
		withHosts(map[string]config.RuntimeEntry{"custom-host": {Adapter: "custom"}}),
	)
	os.WriteFile(filepath.Join(s.root, ".fracta", "config.json"), []byte(`{}`), 0644)

	result, err := s.handleSpawn(context.Background(), makeMCPRequest(map[string]interface{}{
		"task":     "default-host-test",
		"dispatch": "queued",
		// host_type intentionally omitted — should use registry default.
	}))
	if err != nil {
		t.Fatalf("handleSpawn error: %v", err)
	}

	text := result.Content[0].(mcp.TextContent).Text
	var resp map[string]interface{}
	json.Unmarshal([]byte(text), &resp)
	if resp["status"] != "Queued" {
		t.Fatalf("status = %v, want Queued", resp["status"])
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	var p queue.MissionPayload
	json.Unmarshal(m.Payload, &p)

	if p.RuntimeType != "custom-host" {
		t.Errorf("HostType = %q, want %q — must come from registry default, not hardcoded", p.RuntimeType, "custom-host")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func assertEqual(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}

func isErrorResult(r *mcp.CallToolResult) bool {
	return r.IsError
}
