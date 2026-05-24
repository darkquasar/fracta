package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/host/claude"
	"github.com/darkquasar/fracta/internal/queue"
)

// goldenResolvedSpawn captures the serializable subset of ResolvedSpawn.
type goldenResolvedSpawn struct {
	RuntimeType  string   `json:"host_type"`
	Model        string   `json:"model"`
	BaseBranch   string   `json:"base_branch"`
	Mode         string   `json:"mode"`
	AllowedTools []string `json:"allowed_tools"`
	ConfigPath   string   `json:"config_path"`
	GraphAddr    string   `json:"graph_addr"`
	StrategyDir  string   `json:"strategy_dir"`
}

// goldenPayload captures the serializable MissionPayload built from ResolvedSpawn.
type goldenPayload struct {
	Task        string          `json:"task"`
	Contract    string          `json:"contract"`
	BaseBranch  string          `json:"base_branch"`
	Model       string          `json:"model"`
	RuntimeType string          `json:"host_type"`
	Backend     string          `json:"backend"`
	ConfigPath  string          `json:"config_path"`
	GraphAddr   string          `json:"graph_addr"`
	StrategyDir string          `json:"strategy_dir"`
	GatewayURL  string          `json:"gateway_url"`
	ObjectiveID string          `json:"objective_id,omitempty"`
	MCPServers  json.RawMessage `json:"mcp_servers,omitempty"`
}

// goldenWorkspaceConfig captures the WorkspaceConfig fields.
type goldenWorkspaceConfig struct {
	AgentMCP    bool   `json:"agent_mcp"`
	Backend     string `json:"backend"`
	ConfigPath  string `json:"config_path"`
	GraphAddr   string `json:"graph_addr"`
	StrategyDir string `json:"strategy_dir"`
	GatewayURL  string `json:"gateway_url"`
	AgentTask   string `json:"agent_task"`
	ObjectiveID string `json:"objective_id,omitempty"`
	MissionID   int64  `json:"mission_id,omitempty"`
}

// goldenSpawnChain captures the full data flow through the spawn chain.
type goldenSpawnChain struct {
	ResolvedSpawn   goldenResolvedSpawn   `json:"resolved_spawn"`
	MissionPayload  goldenPayload         `json:"mission_payload"`
	WorkspaceConfig goldenWorkspaceConfig `json:"workspace_config"`
}

// spawnChainInput defines the test input for a spawn chain golden test.
type spawnChainInput struct {
	Config      *config.Config
	Task        string
	Contract    string
	RuntimeType string
	Model       string
	Tier        string
	BaseBranch  string
	Mode        string
	Backend     string
	GatewayURL  string
	ObjectiveID string
	MissionID   int64
	MCPServers  config.MCPServersConfig
}

func runSpawnChain(t *testing.T, input spawnChainInput) goldenSpawnChain {
	t.Helper()
	root, store, wsMock := setupIntegrationTest(t)

	h := host.Host(claude.Host{})
	reg := host.NewMapRegistry("claude")
	reg.Register("claude", h)

	// Register batch-only host if needed.
	if input.RuntimeType == "batch-host" {
		reg = host.NewMapRegistry("batch-host")
		reg.Register("batch-host", batchOnlyHost{})
		h = batchOnlyHost{}
	}

	orch := &Orchestrator{
		HostRegistry:   reg,
		Workspace:      wsMock,
		Store:          store,
		Root:           root,
		Config:         input.Config,
		RuntimeBackend: input.Backend,
	}

	// ResolveSpawn
	resolved, err := orch.ResolveSpawn(input.RuntimeType, input.Model, input.Tier, input.BaseBranch, input.Mode)
	if err != nil {
		t.Fatalf("ResolveSpawn: %v", err)
	}

	grs := goldenResolvedSpawn{
		RuntimeType:  resolved.RuntimeType,
		Model:        resolved.Model,
		BaseBranch:   resolved.BaseBranch,
		Mode:         resolved.Mode,
		AllowedTools: resolved.AllowedTools,
		ConfigPath:   resolved.ConfigPath,
		GraphAddr:    resolved.GraphAddr,
		StrategyDir:  resolved.StrategyDir,
	}

	// Build MissionPayload (mirrors cpapi/local_client.go and admin_tools.go).
	mcpServersJSON, _ := json.Marshal(input.MCPServers)
	if len(input.MCPServers.Servers) == 0 {
		mcpServersJSON = nil
	}

	payload := goldenPayload{
		Task:        input.Task,
		Contract:    input.Contract,
		BaseBranch:  resolved.BaseBranch,
		Model:       resolved.Model,
		RuntimeType: resolved.RuntimeType,
		Backend:     input.Backend,
		ConfigPath:  resolved.ConfigPath,
		GraphAddr:   resolved.GraphAddr,
		StrategyDir: resolved.StrategyDir,
		GatewayURL:  input.GatewayURL,
		ObjectiveID: input.ObjectiveID,
		MCPServers:  mcpServersJSON,
	}

	// Build WorkspaceConfig (mirrors worker.go:296-309).
	agentMCP := h.Capabilities().AgentMCP
	if input.RuntimeType == "batch-host" {
		agentMCP = batchOnlyHost{}.Capabilities().AgentMCP
	}

	gwsCfg := goldenWorkspaceConfig{
		AgentMCP:    agentMCP,
		Backend:     input.Backend,
		ConfigPath:  resolved.ConfigPath,
		GraphAddr:   resolved.GraphAddr,
		StrategyDir: resolved.StrategyDir,
		GatewayURL:  input.GatewayURL,
		AgentTask:   input.Task,
		ObjectiveID: input.ObjectiveID,
		MissionID:   input.MissionID,
	}

	return goldenSpawnChain{
		ResolvedSpawn:   grs,
		MissionPayload:  payload,
		WorkspaceConfig: gwsCfg,
	}
}

func goldenSpawnChainTest(t *testing.T, name string, got goldenSpawnChain) {
	t.Helper()
	path := filepath.Join("testdata", "spawn-chain", name)
	data, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated golden file %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden file %s not found — run with UPDATE_GOLDEN=1 to create: %v", path, err)
	}
	if string(data) != string(want) {
		t.Errorf("output differs from golden file %s — run with UPDATE_GOLDEN=1 to regenerate\ngot:\n%s\nwant:\n%s", path, data, want)
	}
}

func TestGoldenSpawnChain_LocalSubprocess(t *testing.T) {
	got := runSpawnChain(t, spawnChainInput{
		Config: &config.Config{
			Project: config.ProjectConfig{
				AllowedTools:      []string{"Bash(*)", "Read", "Write"},
				DefaultBaseBranch: "main",
			},
			Agents: config.AgentsConfig{
				DefaultRuntime: "claude",
				DefaultMode:    "batch",
			},
			Runtimes: map[string]config.RuntimeEntry{
				"claude": {Adapter: "claude", Model: "claude-sonnet-4-5"},
			},
		},
		Task:     "scout-1",
		Contract: "Investigate the alert.",
		Backend:  "local",
		MCPServers: config.MCPServersConfig{
			Servers: map[string]config.MCPServerEntry{
				"elastic": {Local: config.MCPServerLocal{Command: "npx", Args: []string{"-y", "@elastic/mcp-server"}}},
			},
		},
	})
	goldenSpawnChainTest(t, "local-subprocess.golden.json", got)
}

func TestGoldenSpawnChain_K8sQueued(t *testing.T) {
	got := runSpawnChain(t, spawnChainInput{
		Config: &config.Config{
			Project: config.ProjectConfig{
				AllowedTools:      []string{"Bash(*)", "Read", "Write"},
				DefaultBaseBranch: "main",
			},
			Agents: config.AgentsConfig{
				DefaultRuntime: "claude",
				DefaultMode:    "batch",
			},
			Runtimes: map[string]config.RuntimeEntry{
				"claude": {Adapter: "claude", Model: "claude-sonnet-4-5"},
			},
			Gateway: config.GatewayConfig{
				URL: "http://fracta-gateway.fracta.svc:8080",
			},
		},
		Task:       "k8s-agent-1",
		Contract:   "Run the hunt.",
		Backend:    "kubernetes",
		GatewayURL: "http://fracta-gateway.fracta.svc:8080",
		MCPServers: config.MCPServersConfig{
			Servers: map[string]config.MCPServerEntry{
				"elastic": {Kubernetes: config.MCPServerKubernetes{URL: "http://elastic:8000"}},
			},
		},
	})
	goldenSpawnChainTest(t, "k8s-queued.golden.json", got)
}

func TestGoldenSpawnChain_ChildMission(t *testing.T) {
	got := runSpawnChain(t, spawnChainInput{
		Config: &config.Config{
			Project: config.ProjectConfig{
				AllowedTools:      []string{"Bash(*)", "Read"},
				DefaultBaseBranch: "main",
			},
			Agents: config.AgentsConfig{
				DefaultRuntime: "claude",
				DefaultMode:    "batch",
			},
			Runtimes: map[string]config.RuntimeEntry{
				"claude": {Adapter: "claude", Model: "claude-sonnet-4-5"},
			},
		},
		Task:        "child-hunter-3",
		Contract:    "Investigate lateral movement.",
		Backend:     "local",
		ObjectiveID: "obj-456",
		MissionID:   99,
	})
	goldenSpawnChainTest(t, "child-mission.golden.json", got)
}

func TestGoldenSpawnChain_GatewayServed(t *testing.T) {
	got := runSpawnChain(t, spawnChainInput{
		Config: &config.Config{
			Project: config.ProjectConfig{
				AllowedTools:      []string{"Bash(*)", "Read"},
				DefaultBaseBranch: "main",
			},
			Agents: config.AgentsConfig{
				DefaultRuntime: "claude",
				DefaultMode:    "batch",
			},
			Runtimes: map[string]config.RuntimeEntry{
				"claude": {Adapter: "claude", Model: "claude-sonnet-4-5"},
			},
			Gateway: config.GatewayConfig{
				URL: "http://fracta-gateway.fracta.svc:8080",
			},
		},
		Task:       "gateway-agent",
		Contract:   "Query the graph.",
		Backend:    "kubernetes",
		GatewayURL: "http://fracta-gateway.fracta.svc:8080",
	})
	goldenSpawnChainTest(t, "gateway-served.golden.json", got)
}

func TestGoldenSpawnChain_BatchOnlyHost(t *testing.T) {
	got := runSpawnChain(t, spawnChainInput{
		Config: &config.Config{
			Project: config.ProjectConfig{
				AllowedTools: []string{"Read"},
			},
			Agents: config.AgentsConfig{
				DefaultRuntime: "batch-host",
				DefaultMode:    "batch",
			},
			Runtimes: map[string]config.RuntimeEntry{
				"batch-host": {Adapter: "batch", Model: "batch-model"},
			},
		},
		Task:        "batch-task",
		Contract:    "Run batch job.",
		RuntimeType: "batch-host",
		Backend:     "local",
	})
	goldenSpawnChainTest(t, "batch-only-host.golden.json", got)
}

func TestGoldenSpawnChain_TierResolution(t *testing.T) {
	got := runSpawnChain(t, spawnChainInput{
		Config: &config.Config{
			Project: config.ProjectConfig{
				AllowedTools:      []string{"Bash(*)", "Read", "Write"},
				DefaultBaseBranch: "develop",
			},
			Agents: config.AgentsConfig{
				DefaultRuntime: "claude",
				DefaultMode:    "batch",
			},
			Runtimes: map[string]config.RuntimeEntry{
				"claude": {
					Adapter: "claude",
					Model:   "claude-sonnet-4-5",
					ModelTiers: map[string]string{
						"heavy":  "claude-opus-4-6",
						"medium": "claude-sonnet-4-5",
						"light":  "claude-haiku-3-5",
					},
				},
			},
		},
		Task:     "heavy-task",
		Contract: "Deep analysis required.",
		Tier:     "heavy",
		Backend:  "local",
	})
	goldenSpawnChainTest(t, "tier-resolution.golden.json", got)
}

// Verify MissionPayload round-trip through JSON is lossless.
func TestMissionPayload_JSONRoundTrip(t *testing.T) {
	original := queue.MissionPayload{
		Task:         "test-task",
		Contract:     "Do the thing.",
		BaseBranch:   "main",
		Model:        "claude-sonnet-4-5",
		RuntimeType:  "claude",
		AllowedTools: []string{"Bash(*)", "Read", "Write"},
		MCPServers:   json.RawMessage(`{"servers":{"elastic":{"local":{"command":"npx"}}}}`),
		Backend:      "kubernetes",
		ConfigPath:   "/etc/fracta/fracta.yaml",
		GraphAddr:    "localhost:6379",
		StrategyDir:  "/opt/strategies",
		ConfigHash:   "abc123",
		GatewayURL:   "http://fracta-gateway:8080",
		ObjectiveID:  "obj-123",
		MissionID:    42,
		StagedCredentialRefs: map[string]string{
			"bedrock": "staged-ref-abc",
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded queue.MissionPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Re-marshal to normalize JSON ordering.
	reEncoded, _ := json.Marshal(decoded)
	if string(data) != string(reEncoded) {
		t.Errorf("round-trip mismatch:\n  original: %s\n  decoded:  %s", data, reEncoded)
	}

	// Verify individual fields for clarity.
	if decoded.Task != original.Task {
		t.Errorf("Task = %q, want %q", decoded.Task, original.Task)
	}
	if decoded.Contract != original.Contract {
		t.Errorf("Contract = %q, want %q", decoded.Contract, original.Contract)
	}
	if decoded.BaseBranch != original.BaseBranch {
		t.Errorf("BaseBranch = %q, want %q", decoded.BaseBranch, original.BaseBranch)
	}
	if decoded.Model != original.Model {
		t.Errorf("Model = %q, want %q", decoded.Model, original.Model)
	}
	if decoded.RuntimeType != original.RuntimeType {
		t.Errorf("HostType = %q, want %q", decoded.RuntimeType, original.RuntimeType)
	}
	if len(decoded.AllowedTools) != len(original.AllowedTools) {
		t.Errorf("AllowedTools len = %d, want %d", len(decoded.AllowedTools), len(original.AllowedTools))
	}
	if decoded.Backend != original.Backend {
		t.Errorf("Backend = %q, want %q", decoded.Backend, original.Backend)
	}
	if decoded.ConfigPath != original.ConfigPath {
		t.Errorf("ConfigPath = %q, want %q", decoded.ConfigPath, original.ConfigPath)
	}
	if decoded.GraphAddr != original.GraphAddr {
		t.Errorf("GraphAddr = %q, want %q", decoded.GraphAddr, original.GraphAddr)
	}
	if decoded.StrategyDir != original.StrategyDir {
		t.Errorf("StrategyDir = %q, want %q", decoded.StrategyDir, original.StrategyDir)
	}
	if decoded.ConfigHash != original.ConfigHash {
		t.Errorf("ConfigHash = %q, want %q", decoded.ConfigHash, original.ConfigHash)
	}
	if decoded.GatewayURL != original.GatewayURL {
		t.Errorf("GatewayURL = %q, want %q", decoded.GatewayURL, original.GatewayURL)
	}
	if decoded.ObjectiveID != original.ObjectiveID {
		t.Errorf("ObjectiveID = %q, want %q", decoded.ObjectiveID, original.ObjectiveID)
	}
	if decoded.MissionID != original.MissionID {
		t.Errorf("MissionID = %d, want %d", decoded.MissionID, original.MissionID)
	}
	if len(decoded.StagedCredentialRefs) != len(original.StagedCredentialRefs) {
		t.Errorf("StagedCredentialRefs len = %d, want %d", len(decoded.StagedCredentialRefs), len(original.StagedCredentialRefs))
	}
	if decoded.StagedCredentialRefs["bedrock"] != original.StagedCredentialRefs["bedrock"] {
		t.Errorf("StagedCredentialRefs[bedrock] = %q, want %q", decoded.StagedCredentialRefs["bedrock"], original.StagedCredentialRefs["bedrock"])
	}
}
