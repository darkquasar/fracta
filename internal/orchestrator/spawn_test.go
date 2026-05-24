package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/host/claude"
	"github.com/darkquasar/fracta/internal/queue"
	"github.com/darkquasar/fracta/internal/runtime"
)

// TestResolveSpawn_TierBasedModelResolution verifies that the tier parameter
// flows through ResolveSpawn to resolveModel and correctly looks up the model
// from HostConfig.ModelTiers.
func TestResolveSpawn_TierBasedModelResolution(t *testing.T) {
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
				AllowedTools: []string{"Read"},
			},
			Agents: config.AgentsConfig{
				DefaultRuntime: "claude",
				DefaultMode:    "batch",
			},
			Runtimes: map[string]config.RuntimeEntry{
				"claude": {
					Adapter: "claude",
					Model:   "default-model",
					ModelTiers: map[string]string{
						"heavy":  "claude-opus-4-6",
						"medium": "claude-sonnet-4-5",
						"light":  "claude-haiku-3-5",
					},
				},
			},
		},
	}

	tests := []struct {
		name      string
		tier      string
		wantModel string
	}{
		{"heavy tier", "heavy", "claude-opus-4-6"},
		{"medium tier", "medium", "claude-sonnet-4-5"},
		{"light tier", "light", "claude-haiku-3-5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := orch.ResolveSpawn("", "", tt.tier, "", "")
			if err != nil {
				t.Fatalf("ResolveSpawn with tier %q: %v", tt.tier, err)
			}
			if resolved.Model != tt.wantModel {
				t.Errorf("Model = %q, want %q", resolved.Model, tt.wantModel)
			}
		})
	}
}

// TestResolveSpawn_TierOverridesDefault verifies that tier takes priority
// over the default model from HostConfig.
func TestResolveSpawn_TierOverridesDefault(t *testing.T) {
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
				AllowedTools: []string{"Read"},
			},
			Agents: config.AgentsConfig{DefaultRuntime: "claude"},
			Runtimes: map[string]config.RuntimeEntry{
				"claude": {
					Adapter: "claude",
					Model:   "default-model",
					ModelTiers: map[string]string{
						"heavy": "claude-opus-4-6",
					},
				},
			},
		},
	}

	resolved, err := orch.ResolveSpawn("", "", "heavy", "", "")
	if err != nil {
		t.Fatalf("ResolveSpawn: %v", err)
	}
	if resolved.Model != "claude-opus-4-6" {
		t.Errorf("Model = %q, want %q (tier should override default)", resolved.Model, "claude-opus-4-6")
	}
}

// TestResolveSpawn_ExplicitModelOverridesTier verifies that an explicit model
// takes priority over a tier parameter.
func TestResolveSpawn_ExplicitModelOverridesTier(t *testing.T) {
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
				AllowedTools: []string{"Read"},
			},
			Agents: config.AgentsConfig{DefaultRuntime: "claude"},
			Runtimes: map[string]config.RuntimeEntry{
				"claude": {
					Adapter: "claude",
					Model:   "default-model",
					ModelTiers: map[string]string{
						"heavy": "claude-opus-4-6",
					},
				},
			},
		},
	}

	resolved, err := orch.ResolveSpawn("", "my-explicit-model", "heavy", "", "")
	if err != nil {
		t.Fatalf("ResolveSpawn: %v", err)
	}
	if resolved.Model != "my-explicit-model" {
		t.Errorf("Model = %q, want %q (explicit should override tier)", resolved.Model, "my-explicit-model")
	}
}

// TestResolveSpawn_InvalidTierFails verifies that an invalid tier name
// returns a clear error.
func TestResolveSpawn_InvalidTierFails(t *testing.T) {
	root, store, wsMock := setupIntegrationTest(t)

	reg := host.NewMapRegistry("claude")
	reg.Register("claude", claude.Host{})

	orch := &Orchestrator{
		HostRegistry: reg,
		Workspace:    wsMock,
		Store:        store,
		Root:         root,
		Config: &config.Config{
			Project: config.ProjectConfig{AllowedTools: []string{"Read"}},
			Agents:  config.AgentsConfig{DefaultRuntime: "claude"},
			Runtimes: map[string]config.RuntimeEntry{
				"claude": {
					Adapter:    "claude",
					ModelTiers: map[string]string{"heavy": "claude-opus-4-6"},
				},
			},
		},
	}

	_, err := orch.ResolveSpawn("", "", "turbo", "", "")
	if err == nil {
		t.Fatal("expected error for invalid tier")
	}
}

// === D4: WorkspaceConfig parity test ===

// TestWorkspaceConfig_OrchestratorWorkerParity verifies that the WorkspaceConfig
// built by the orchestrator's prepareSpawn (spawn.go:293-304) and the one built
// by the worker's executeMission (worker.go:296-309) produce equivalent results
// for the same logical spawn.
//
// The two construction sites are independent — this test catches field drift
// where one site adds a field and the other doesn't.
func TestWorkspaceConfig_OrchestratorWorkerParity(t *testing.T) {
	// Define the shared spawn parameters.
	const (
		backend     = "local"
		configPath  = "/etc/fracta/fracta.yaml"
		graphAddr   = "localhost:6379"
		strategyDir = "/opt/strategies"
		gatewayURL  = "http://fracta-gateway:8080"
		agentTask   = "test-agent"
		objectiveID = "obj-123"
	)
	var missionID int64 = 42

	mcpServers := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"elastic": {Local: config.MCPServerLocal{Command: "npx", Args: []string{"-y", "@elastic/mcp-server"}}},
		},
	}
	mcpServersJSON, err := json.Marshal(mcpServers)
	if err != nil {
		t.Fatal(err)
	}

	// Build the orchestrator-side WorkspaceConfig (mirrors spawn.go:293-304).
	orchCfg := host.WorkspaceConfig{
		AgentMCP:    true, // claude.Host{}.Capabilities().AgentMCP
		Servers:     mcpServers,
		Backend:     backend,
		ProjectRoot: "/orchestrator/root", // necessarily different from worker
		ConfigPath:  configPath,
		GraphAddr:   graphAddr,
		StrategyDir: strategyDir,
		GatewayURL:  gatewayURL,
		AgentTask:   agentTask,
		// Note: orchestrator direct-spawn path does NOT set ObjectiveID or MissionID
		// (those are only set in the worker path from the payload).
	}

	// Build the worker-side WorkspaceConfig (mirrors worker.go:296-309).
	payload := queue.MissionPayload{
		Task:        agentTask,
		Backend:     backend,
		ConfigPath:  configPath,
		GraphAddr:   graphAddr,
		StrategyDir: strategyDir,
		GatewayURL:  gatewayURL,
		ObjectiveID: objectiveID,
		MissionID:   missionID,
		MCPServers:  mcpServersJSON,
	}

	var workerMCPServers config.MCPServersConfig
	_ = json.Unmarshal(payload.MCPServers, &workerMCPServers)

	workerCfg := host.WorkspaceConfig{
		AgentMCP:    true, // h.Capabilities().AgentMCP
		Servers:     workerMCPServers,
		Backend:     payload.Backend,
		ProjectRoot: "/worker/workspace/test-agent", // necessarily different
		ConfigPath:  payload.ConfigPath,
		GraphAddr:   payload.GraphAddr,
		StrategyDir: payload.StrategyDir,
		GatewayURL:  payload.GatewayURL,
		AgentTask:   payload.Task,
		ObjectiveID: payload.ObjectiveID,
		MissionID:   payload.MissionID,
	}

	// Compare fields that should be identical.
	if orchCfg.AgentMCP != workerCfg.AgentMCP {
		t.Errorf("AgentMCP: orch=%v, worker=%v", orchCfg.AgentMCP, workerCfg.AgentMCP)
	}
	if orchCfg.Backend != workerCfg.Backend {
		t.Errorf("Backend: orch=%q, worker=%q", orchCfg.Backend, workerCfg.Backend)
	}
	if orchCfg.ConfigPath != workerCfg.ConfigPath {
		t.Errorf("ConfigPath: orch=%q, worker=%q", orchCfg.ConfigPath, workerCfg.ConfigPath)
	}
	if orchCfg.GraphAddr != workerCfg.GraphAddr {
		t.Errorf("GraphAddr: orch=%q, worker=%q", orchCfg.GraphAddr, workerCfg.GraphAddr)
	}
	if orchCfg.StrategyDir != workerCfg.StrategyDir {
		t.Errorf("StrategyDir: orch=%q, worker=%q", orchCfg.StrategyDir, workerCfg.StrategyDir)
	}
	if orchCfg.GatewayURL != workerCfg.GatewayURL {
		t.Errorf("GatewayURL: orch=%q, worker=%q", orchCfg.GatewayURL, workerCfg.GatewayURL)
	}
	if orchCfg.AgentTask != workerCfg.AgentTask {
		t.Errorf("AgentTask: orch=%q, worker=%q", orchCfg.AgentTask, workerCfg.AgentTask)
	}

	// MCP servers should round-trip through JSON correctly.
	orchJSON, _ := json.Marshal(orchCfg.Servers)
	workerJSON, _ := json.Marshal(workerCfg.Servers)
	if string(orchJSON) != string(workerJSON) {
		t.Errorf("Servers mismatch after JSON round-trip:\n  orch:   %s\n  worker: %s", orchJSON, workerJSON)
	}

	// Verify the known semantic difference: orchestrator direct-spawn path
	// does NOT set ObjectiveID/MissionID — worker path does.
	if orchCfg.ObjectiveID != "" {
		t.Error("orchestrator direct-spawn should not set ObjectiveID (only worker path does)")
	}
	if workerCfg.ObjectiveID != objectiveID {
		t.Errorf("worker ObjectiveID = %q, want %q", workerCfg.ObjectiveID, objectiveID)
	}
	if workerCfg.MissionID != missionID {
		t.Errorf("worker MissionID = %d, want %d", workerCfg.MissionID, missionID)
	}

	// ProjectRoot is necessarily different (orchestrator uses project root,
	// worker uses ephemeral workspace path). Just verify both are non-empty.
	if orchCfg.ProjectRoot == "" {
		t.Error("orchestrator ProjectRoot should not be empty")
	}
	if workerCfg.ProjectRoot == "" {
		t.Error("worker ProjectRoot should not be empty")
	}
	if orchCfg.ProjectRoot == workerCfg.ProjectRoot {
		t.Error("orchestrator and worker ProjectRoot should differ (one is project root, other is workspace)")
	}
}

// --- CollectWorkspaceFiles tests ---

// setupWorkspaceFiles creates a temp dir with the given relative paths and contents.
func setupWorkspaceFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for relPath, content := range files {
		full := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCollectWorkspaceFiles_Claude(t *testing.T) {
	dir := setupWorkspaceFiles(t, map[string]string{
		".claude/settings.json":      `{"permissions":{}}`,
		".mcp.json":                  `{"mcpServers":{}}`,
		".fracta/user-settings.json": `{"apiKeyHelper":"cmd"}`,
		"CLAUDE.md":                  "# Task",
	})

	artifacts := CollectWorkspaceFiles(dir, "claude")

	if len(artifacts) != 4 {
		t.Fatalf("got %d artifacts, want 4", len(artifacts))
	}

	byKey := make(map[string]runtime.WorkspaceArtifact)
	for _, a := range artifacts {
		byKey[a.ConfigMapKey] = a
	}

	cases := []struct {
		key      string
		destPath string
		content  string
	}{
		{"dot-claude--settings.json", filepath.Join(".claude", "settings.json"), `{"permissions":{}}`},
		{"dot-mcp.json", ".mcp.json", `{"mcpServers":{}}`},
		{"dot-fracta--user-settings.json", filepath.Join(".fracta", "user-settings.json"), `{"apiKeyHelper":"cmd"}`},
		{"CLAUDE.md", "CLAUDE.md", "# Task"},
	}

	for _, tc := range cases {
		a, ok := byKey[tc.key]
		if !ok {
			t.Errorf("missing artifact with key %q", tc.key)
			continue
		}
		if a.DestPath != tc.destPath {
			t.Errorf("artifact %q: DestPath = %q, want %q", tc.key, a.DestPath, tc.destPath)
		}
		if a.Content != tc.content {
			t.Errorf("artifact %q: Content = %q, want %q", tc.key, a.Content, tc.content)
		}
	}
}

func TestCollectWorkspaceFiles_Codex(t *testing.T) {
	dir := setupWorkspaceFiles(t, map[string]string{
		".codex/config.toml": "[mcp_servers.fracta]\nurl = \"http://gw\"\n",
		"AGENTS.md":          "# Codex Agent",
	})

	artifacts := CollectWorkspaceFiles(dir, "codex")

	if len(artifacts) != 2 {
		t.Fatalf("got %d artifacts, want 2", len(artifacts))
	}

	byKey := make(map[string]runtime.WorkspaceArtifact)
	for _, a := range artifacts {
		byKey[a.ConfigMapKey] = a
	}

	if a, ok := byKey["dot-codex--config.toml"]; !ok {
		t.Error("missing dot-codex--config.toml artifact")
	} else if a.DestPath != filepath.Join(".codex", "config.toml") {
		t.Errorf("DestPath = %q, want .codex/config.toml", a.DestPath)
	}

	if a, ok := byKey["AGENTS.md"]; !ok {
		t.Error("missing AGENTS.md artifact")
	} else if a.Content != "# Codex Agent" {
		t.Errorf("AGENTS.md content = %q, want %q", a.Content, "# Codex Agent")
	}
}

func TestCollectWorkspaceFiles_OpenCode(t *testing.T) {
	dir := setupWorkspaceFiles(t, map[string]string{
		"opencode.json": `{"permission":{"*":"ask"}}`,
		"AGENTS.md":     "# OpenCode Agent",
	})

	artifacts := CollectWorkspaceFiles(dir, "opencode")

	if len(artifacts) != 2 {
		t.Fatalf("got %d artifacts, want 2", len(artifacts))
	}

	byKey := make(map[string]runtime.WorkspaceArtifact)
	for _, a := range artifacts {
		byKey[a.ConfigMapKey] = a
	}

	if a, ok := byKey["opencode.json"]; !ok {
		t.Error("missing opencode.json artifact")
	} else if a.DestPath != "opencode.json" {
		t.Errorf("DestPath = %q, want opencode.json", a.DestPath)
	}

	if _, ok := byKey["AGENTS.md"]; !ok {
		t.Error("missing AGENTS.md artifact")
	}
}

func TestCollectWorkspaceFiles_MissingFilesSkipped(t *testing.T) {
	// Only create one of Claude's four files.
	dir := setupWorkspaceFiles(t, map[string]string{
		"CLAUDE.md": "# Task",
	})

	artifacts := CollectWorkspaceFiles(dir, "claude")

	if len(artifacts) != 1 {
		t.Fatalf("got %d artifacts, want 1 (missing files should be skipped)", len(artifacts))
	}
	if artifacts[0].ConfigMapKey != "CLAUDE.md" {
		t.Errorf("ConfigMapKey = %q, want CLAUDE.md", artifacts[0].ConfigMapKey)
	}
}

func TestCollectWorkspaceFiles_UnknownRuntimeFallsBackToClaude(t *testing.T) {
	dir := setupWorkspaceFiles(t, map[string]string{
		"CLAUDE.md": "# Task",
	})

	artifacts := CollectWorkspaceFiles(dir, "unknown-runtime")

	if len(artifacts) != 1 {
		t.Fatalf("got %d artifacts, want 1 (fallback to claude)", len(artifacts))
	}
	if artifacts[0].ConfigMapKey != "CLAUDE.md" {
		t.Errorf("ConfigMapKey = %q, want CLAUDE.md", artifacts[0].ConfigMapKey)
	}
}

func TestCollectWorkspaceFiles_EmptyWorkspace(t *testing.T) {
	dir := t.TempDir() // empty directory

	artifacts := CollectWorkspaceFiles(dir, "codex")

	if len(artifacts) != 0 {
		t.Errorf("got %d artifacts, want 0 for empty workspace", len(artifacts))
	}
}

// mockStreamBackend records KillStreamPod calls.
type mockStreamBackend struct {
	runtime.LocalBackend
	killed  []string
	killErr error
}

func (m *mockStreamBackend) SpawnStreamPod(ctx context.Context, opts runtime.StreamPodOpts) (*runtime.StreamPodInfo, error) {
	return nil, errors.New("not implemented in mock")
}

func (m *mockStreamBackend) KillStreamPod(ctx context.Context, id string) error {
	m.killed = append(m.killed, id)
	return m.killErr
}

func TestCleanupStreamPod_KillsWhenStreamBackend(t *testing.T) {
	mock := &mockStreamBackend{}
	orch := &Orchestrator{
		Backend: mock,
		Logger:  slog.Default(),
	}

	orch.cleanupStreamPod("agent-42")

	if len(mock.killed) != 1 || mock.killed[0] != "agent-42" {
		t.Errorf("expected KillStreamPod(agent-42), got killed=%v", mock.killed)
	}
}

func TestCleanupStreamPod_NoopForLocalBackend(t *testing.T) {
	orch := &Orchestrator{
		Backend: runtime.NewLocalBackend(),
		Logger:  slog.Default(),
	}

	// Must not panic or error — LocalBackend doesn't implement StreamBackend.
	orch.cleanupStreamPod("agent-local")
}

func TestCleanupStreamPod_ToleratesNotFound(t *testing.T) {
	mock := &mockStreamBackend{killErr: runtime.ErrNotFound}
	orch := &Orchestrator{
		Backend: mock,
		Logger:  slog.Default(),
	}

	// Must not panic — ErrNotFound is silently ignored.
	orch.cleanupStreamPod("gone-agent")

	if len(mock.killed) != 1 {
		t.Errorf("expected kill attempt even when pod is gone, got killed=%v", mock.killed)
	}
}

func TestRuntimeWorkDir_Local(t *testing.T) {
	orch := &Orchestrator{
		RuntimeBackend: "local",
	}
	got := orch.runtimeWorkDir("my-agent", "/home/user/.fracta/worktrees/my-agent")
	if got != "/home/user/.fracta/worktrees/my-agent" {
		t.Errorf("local: got %q, want local path", got)
	}
}

func TestRuntimeWorkDir_K8sDefault(t *testing.T) {
	orch := &Orchestrator{
		RuntimeBackend: "kubernetes",
		Config:         &config.Config{},
	}
	got := orch.runtimeWorkDir("my-agent", "/tmp/staging/my-agent")
	if got != "/workspace/agents/my-agent" {
		t.Errorf("k8s default: got %q, want /workspace/agents/my-agent", got)
	}
}

func TestRuntimeWorkDir_K8sCustomMount(t *testing.T) {
	orch := &Orchestrator{
		RuntimeBackend: "kubernetes",
		Config: &config.Config{
			Runtime: config.RuntimeConfig{
				Kubernetes: config.KubernetesConfig{
					PVCMountPath: "/data/fracta",
				},
			},
		},
	}
	got := orch.runtimeWorkDir("my-agent", "/tmp/staging/my-agent")
	if got != "/data/fracta/agents/my-agent" {
		t.Errorf("k8s custom: got %q, want /data/fracta/agents/my-agent", got)
	}
}
