package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkquasar/fracta/internal/agentpolicy"
	"github.com/darkquasar/fracta/internal/auth/credentials"
	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/host"
)

func TestWriteWorkspace_SettingsAndMCP(t *testing.T) {
	dir := t.TempDir()
	h := Host{}

	err := h.WriteWorkspace(dir, []string{"Bash(ls *)"}, host.WorkspaceConfig{
		AgentMCP: true,
		Servers: config.MCPServersConfig{
			Servers: map[string]config.MCPServerEntry{
				"elastic": {Local: config.MCPServerLocal{Command: "npx"}},
			},
		},
		Backend:     "local",
		ProjectRoot: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("WriteWorkspace: %v", err)
	}

	// settings.json exists and has permissions
	data, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("reading settings.json: %v", err)
	}
	var settings struct {
		Permissions struct{ Allow []string } `json:"permissions"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parsing settings.json: %v", err)
	}
	if len(settings.Permissions.Allow) == 0 {
		t.Error("expected non-empty allowed tools")
	}

	// .mcp.json exists and has fracta-agent
	mcpData, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatalf("reading .mcp.json: %v", err)
	}
	var mcpJSON struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(mcpData, &mcpJSON); err != nil {
		t.Fatalf("parsing .mcp.json: %v", err)
	}
	if _, ok := mcpJSON.MCPServers["fracta-agent"]; !ok {
		t.Error("missing fracta-agent in .mcp.json")
	}
}

func TestWriteWorkspace_NoAgentMCP(t *testing.T) {
	dir := t.TempDir()
	h := Host{}

	err := h.WriteWorkspace(dir, nil, host.WorkspaceConfig{AgentMCP: false})
	if err != nil {
		t.Fatalf("WriteWorkspace: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{}\n" {
		t.Errorf(".mcp.json = %q, want empty object", data)
	}
}

func TestBuildMCPServersJSON_IncludesNewFlags(t *testing.T) {
	cfg := host.WorkspaceConfig{
		AgentMCP:    true,
		Backend:     "local",
		ProjectRoot: "/tmp/test",
		ConfigPath:  "/etc/fracta/fracta.yaml",
		GraphAddr:   "localhost:6379",
		StrategyDir: "/opt/strategies",
	}

	servers := BuildMCPServersJSON(cfg)
	agentRaw, ok := servers["fracta-agent"]
	if !ok {
		t.Fatal("missing fracta-agent")
	}
	agent := agentRaw.(map[string]interface{})
	args := agent["args"].([]string)
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "--config /etc/fracta/fracta.yaml") {
		t.Errorf("missing --config flag in args: %v", args)
	}
	if !strings.Contains(joined, "--graph-addr localhost:6379") {
		t.Errorf("missing --graph-addr flag in args: %v", args)
	}
	if !strings.Contains(joined, "--strategy-dir /opt/strategies") {
		t.Errorf("missing --strategy-dir flag in args: %v", args)
	}
}

func TestBuildMCPServersJSON_OmitsEmptyFlags(t *testing.T) {
	cfg := host.WorkspaceConfig{
		AgentMCP:    true,
		Backend:     "local",
		ProjectRoot: "/tmp/test",
	}

	servers := BuildMCPServersJSON(cfg)
	agent := servers["fracta-agent"].(map[string]interface{})
	args := agent["args"].([]string)
	joined := strings.Join(args, " ")

	if strings.Contains(joined, "--config") {
		t.Errorf("should not include --config when empty: %v", args)
	}
	if strings.Contains(joined, "--graph-addr") {
		t.Errorf("should not include --graph-addr when empty: %v", args)
	}
	if strings.Contains(joined, "--strategy-dir") {
		t.Errorf("should not include --strategy-dir when empty: %v", args)
	}
}

func TestCapabilities_Claude(t *testing.T) {
	h := Host{}
	caps := h.Capabilities()

	if !caps.Stream {
		t.Error("expected Stream=true")
	}
	if !caps.AgentMCP {
		t.Error("expected AgentMCP=true")
	}
	if !caps.ToolPermissions {
		t.Error("expected ToolPermissions=true")
	}
	if !caps.ResumeToken {
		t.Error("expected ResumeToken=true")
	}
}

func TestCapabilities_Noop(t *testing.T) {
	h := host.NoopHost{}
	caps := h.Capabilities()

	if caps.Stream || caps.AgentMCP || caps.ToolPermissions || caps.ResumeToken {
		t.Errorf("NoopHost should have all capabilities false, got %+v", caps)
	}
}

func TestBootstrap_ReturnsClaudeMD(t *testing.T) {
	h := Host{}
	boot := h.Bootstrap("my-agent", "main", "Do the thing.")

	if boot.FileName != "CLAUDE.md" {
		t.Errorf("FileName = %q, want CLAUDE.md", boot.FileName)
	}
	if boot.InitialPrompt != "Read CLAUDE.md and execute the task autonomously." {
		t.Errorf("InitialPrompt = %q", boot.InitialPrompt)
	}
	if boot.FileBody == "" {
		t.Error("FileBody should not be empty")
	}
}

func TestBootstrap_NoGraphOrStrategy(t *testing.T) {
	h := Host{}
	boot := h.Bootstrap("my-agent", "main", "Contract.")

	if strings.Contains(boot.FileBody, "Knowledge Graph Protocol") {
		t.Error("plain Bootstrap should not include graph instructions")
	}
	if strings.Contains(boot.FileBody, "Strategy Engine") {
		t.Error("plain Bootstrap should not include strategy instructions")
	}
}

func TestBootstrapWithConfig_GraphOnly(t *testing.T) {
	h := Host{}
	cfg := host.WorkspaceConfig{GraphAddr: "localhost:6379"}
	boot := h.BootstrapWithConfig("my-agent", "main", "Contract.", cfg)

	if !strings.Contains(boot.FileBody, "Knowledge Graph Protocol") {
		t.Error("expected graph instructions when GraphAddr is set")
	}
	if !strings.Contains(boot.FileBody, "graph_checkpoint") {
		t.Error("expected graph_checkpoint mentioned in graph instructions")
	}
	if !strings.Contains(boot.FileBody, "graph_schema") {
		t.Error("expected graph_schema mentioned in graph instructions")
	}
	if strings.Contains(boot.FileBody, "Strategy Engine") {
		t.Error("should not include strategy instructions when StrategyDir is empty")
	}
}

func TestBootstrapWithConfig_StrategyOnly(t *testing.T) {
	h := Host{}
	cfg := host.WorkspaceConfig{StrategyDir: "/opt/strategies"}
	boot := h.BootstrapWithConfig("my-agent", "main", "Contract.", cfg)

	if strings.Contains(boot.FileBody, "Knowledge Graph Protocol") {
		t.Error("should not include graph instructions when GraphAddr is empty")
	}
	if !strings.Contains(boot.FileBody, "Strategy Engine") {
		t.Error("expected strategy instructions when StrategyDir is set")
	}
	if !strings.Contains(boot.FileBody, "strategy_match") {
		t.Error("expected strategy_match mentioned in strategy instructions")
	}
	if !strings.Contains(boot.FileBody, "strategy_list") {
		t.Error("expected strategy_list mentioned in strategy instructions")
	}
	if !strings.Contains(boot.FileBody, "strategy_run") {
		t.Error("expected strategy_run mentioned in strategy instructions")
	}
}

func TestBootstrapWithConfig_Both(t *testing.T) {
	h := Host{}
	cfg := host.WorkspaceConfig{
		GraphAddr:   "localhost:6379",
		StrategyDir: "/opt/strategies",
	}
	boot := h.BootstrapWithConfig("my-agent", "main", "Contract.", cfg)

	if !strings.Contains(boot.FileBody, "Knowledge Graph Protocol") {
		t.Error("expected graph instructions")
	}
	if !strings.Contains(boot.FileBody, "Strategy Engine") {
		t.Error("expected strategy instructions")
	}
}

func TestBootstrapWithConfig_Neither(t *testing.T) {
	h := Host{}
	cfg := host.WorkspaceConfig{}
	boot := h.BootstrapWithConfig("my-agent", "main", "Contract.", cfg)

	if strings.Contains(boot.FileBody, "Knowledge Graph Protocol") {
		t.Error("should not include graph instructions")
	}
	if strings.Contains(boot.FileBody, "Strategy Engine") {
		t.Error("should not include strategy instructions")
	}
}

func TestBootstrapHost_DelegatesToConfigurable(t *testing.T) {
	h := Host{}
	cfg := host.WorkspaceConfig{GraphAddr: "localhost:6379"}

	// host.BootstrapHost should delegate to BootstrapWithConfig for claude.Host
	boot := host.BootstrapHost(h, "my-agent", "main", "Contract.", cfg)

	if !strings.Contains(boot.FileBody, "Knowledge Graph Protocol") {
		t.Error("BootstrapHost should delegate to BootstrapWithConfig and include graph instructions")
	}
}

func TestBootstrapHost_FallbackForNoop(t *testing.T) {
	h := host.NoopHost{}
	cfg := host.WorkspaceConfig{GraphAddr: "localhost:6379"}

	// NoopHost doesn't implement ConfigurableBootstrap, should fall back
	boot := host.BootstrapHost(h, "my-agent", "main", "Contract.", cfg)

	// NoopHost returns empty BootstrapResult
	if boot.FileBody != "" {
		t.Errorf("NoopHost should return empty FileBody, got %q", boot.FileBody)
	}
}

func TestWriteWorkspace_AuthPodScript(t *testing.T) {
	dir := t.TempDir()
	h := Host{}

	err := h.WriteWorkspace(dir, []string{"Bash(ls *)"}, host.WorkspaceConfig{
		AgentMCP: false,
		CredentialOutput: &credentials.CredentialOutput{
			Plan: &credentials.CredentialPlan{
				Binding: &credentials.CredentialBinding{
					Type:                "claude_api_key_helper",
					RuntimeAuthResolver: "bedrock_helper",
				},
				RuntimeAuthResolver: &credentials.CredentialResolver{
					Type:    "command",
					Command: "/usr/local/bin/fetch-bedrock-token",
					TTLMs:   30000,
				},
				Env: map[string]string{
					"CLAUDE_CODE_USE_BEDROCK":       "1",
					"CLAUDE_CODE_SKIP_BEDROCK_AUTH": "1",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("WriteWorkspace: %v", err)
	}

	// Verify .fracta/user-settings.json was created
	data, err := os.ReadFile(filepath.Join(dir, ".fracta", "user-settings.json"))
	if err != nil {
		t.Fatalf("reading user-settings.json: %v", err)
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parsing user-settings.json: %v", err)
	}

	if settings["apiKeyHelper"] != "/usr/local/bin/fetch-bedrock-token" {
		t.Errorf("apiKeyHelper = %v, want /usr/local/bin/fetch-bedrock-token", settings["apiKeyHelper"])
	}

	env, ok := settings["env"].(map[string]interface{})
	if !ok {
		t.Fatal("env field missing or wrong type")
	}
	if env["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Errorf("CLAUDE_CODE_USE_BEDROCK = %v, want 1", env["CLAUDE_CODE_USE_BEDROCK"])
	}
	if env["CLAUDE_CODE_API_KEY_HELPER_TTL_MS"] != "30000" {
		t.Errorf("TTL = %v, want 30000", env["CLAUDE_CODE_API_KEY_HELPER_TTL_MS"])
	}
}

func TestWriteWorkspace_CredentialBearerEnv_NoUserSettings(t *testing.T) {
	dir := t.TempDir()
	h := Host{}

	// bearer_env binding should NOT produce user-settings.json
	err := h.WriteWorkspace(dir, []string{"Bash(ls *)"}, host.WorkspaceConfig{
		AgentMCP: false,
		CredentialOutput: &credentials.CredentialOutput{
			Plan: &credentials.CredentialPlan{
				Binding: &credentials.CredentialBinding{
					Type:       "bearer_env",
					AuthOrigin: "proxy",
					EnvName:    "API_TOKEN",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("WriteWorkspace: %v", err)
	}

	_, err = os.Stat(filepath.Join(dir, ".fracta", "user-settings.json"))
	if err == nil {
		t.Error("user-settings.json should not exist for bearer_env binding")
	}
}

func TestWriteWorkspace_CredentialNilPlan(t *testing.T) {
	dir := t.TempDir()
	h := Host{}

	// CredentialOutput with nil Plan should be a no-op
	err := h.WriteWorkspace(dir, []string{"Bash(ls *)"}, host.WorkspaceConfig{
		AgentMCP:         false,
		CredentialOutput: &credentials.CredentialOutput{},
	})
	if err != nil {
		t.Fatalf("WriteWorkspace: %v", err)
	}

	_, err = os.Stat(filepath.Join(dir, ".fracta", "user-settings.json"))
	if err == nil {
		t.Error("user-settings.json should not exist when plan is nil")
	}
}

func TestWriteWorkspace_CredentialDefaultTTL(t *testing.T) {
	dir := t.TempDir()
	h := Host{}

	// claude_api_key_helper with TTLMs=0 should default to 60000
	err := h.WriteWorkspace(dir, []string{"Bash(ls *)"}, host.WorkspaceConfig{
		AgentMCP: false,
		CredentialOutput: &credentials.CredentialOutput{
			Plan: &credentials.CredentialPlan{
				Binding: &credentials.CredentialBinding{
					Type:                "claude_api_key_helper",
					RuntimeAuthResolver: "bedrock_helper",
				},
				RuntimeAuthResolver: &credentials.CredentialResolver{
					Type:    "command",
					Command: "/usr/local/bin/fetch-token",
					TTLMs:   0,
				},
				Env: map[string]string{},
			},
		},
	})
	if err != nil {
		t.Fatalf("WriteWorkspace: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".fracta", "user-settings.json"))
	if err != nil {
		t.Fatalf("reading user-settings.json: %v", err)
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parsing user-settings.json: %v", err)
	}

	env := settings["env"].(map[string]interface{})
	if env["CLAUDE_CODE_API_KEY_HELPER_TTL_MS"] != "60000" {
		t.Errorf("default TTL = %v, want 60000", env["CLAUDE_CODE_API_KEY_HELPER_TTL_MS"])
	}
}

func TestWriteWorkspace_AuthNone(t *testing.T) {
	dir := t.TempDir()
	h := Host{}

	err := h.WriteWorkspace(dir, []string{"Bash(ls *)"}, host.WorkspaceConfig{
		AgentMCP: false,
	})
	if err != nil {
		t.Fatalf("WriteWorkspace: %v", err)
	}

	// Verify .fracta/user-settings.json was NOT created
	_, err = os.Stat(filepath.Join(dir, ".fracta", "user-settings.json"))
	if err == nil {
		t.Error("user-settings.json should not exist when Auth is nil")
	}
}

func TestBuildMCPServersJSON_GatewayMode(t *testing.T) {
	cfg := host.WorkspaceConfig{
		AgentMCP:    true,
		Backend:     "kubernetes",
		ProjectRoot: "/workspace",
		GatewayURL:  "http://fracta-gateway.fracta.svc:8080",
		AgentTask:   "scout-1",
		Servers: config.MCPServersConfig{
			Servers: map[string]config.MCPServerEntry{
				"elastic": {Kubernetes: config.MCPServerKubernetes{URL: "http://elastic:8000"}},
				"vendor":  {Kubernetes: config.MCPServerKubernetes{URL: "http://vendor:3000"}},
			},
		},
	}

	servers := BuildMCPServersJSON(cfg)

	// Should have exactly one entry: "fracta"
	if len(servers) != 1 {
		t.Errorf("gateway mode should emit exactly 1 server entry, got %d: %v", len(servers), servers)
	}

	fractaRaw, ok := servers["fracta"]
	if !ok {
		t.Fatal("missing 'fracta' entry in gateway mode")
	}

	fracta := fractaRaw.(map[string]interface{})
	typ, ok := fracta["type"].(string)
	if !ok || typ != "http" {
		t.Fatalf("fracta entry should have type=http, got %q", typ)
	}
	url, ok := fracta["url"].(string)
	if !ok {
		t.Fatal("fracta entry should have 'url' string field")
	}
	expectedURL := "http://fracta-gateway.fracta.svc:8080/agents/scout-1/mcp"
	if url != expectedURL {
		t.Errorf("url = %q, want %q", url, expectedURL)
	}

	// Should NOT have fracta-agent subprocess entry
	if _, ok := servers["fracta-agent"]; ok {
		t.Error("gateway mode should not emit fracta-agent subprocess entry")
	}
	// Should NOT have individual backend entries
	if _, ok := servers["elastic"]; ok {
		t.Error("gateway mode should not emit individual backend entries")
	}
	if _, ok := servers["vendor"]; ok {
		t.Error("gateway mode should not emit individual backend entries")
	}
}

func TestBuildMCPServersJSON_GatewayPermissions(t *testing.T) {
	dir := t.TempDir()
	h := Host{}

	err := h.WriteWorkspace(dir, []string{"Bash(ls *)"}, host.WorkspaceConfig{
		AgentMCP:   true,
		Backend:    "kubernetes",
		GatewayURL: "http://fracta-gateway:8080",
		AgentTask:  "scout-1",
	})
	if err != nil {
		t.Fatalf("WriteWorkspace: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("reading settings.json: %v", err)
	}

	var settings struct {
		Permissions struct{ Allow []string } `json:"permissions"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parsing settings.json: %v", err)
	}

	// Check that gateway mode uses mcp__fracta__ prefix
	hasFractaPrefix := false
	hasOldPrefix := false
	for _, tool := range settings.Permissions.Allow {
		if strings.HasPrefix(tool, "mcp__fracta__") {
			hasFractaPrefix = true
		}
		if strings.HasPrefix(tool, "mcp__fracta-agent__") {
			hasOldPrefix = true
		}
	}

	if !hasFractaPrefix {
		t.Error("gateway mode should use mcp__fracta__ permission prefix")
	}
	if hasOldPrefix {
		t.Error("gateway mode should NOT use mcp__fracta-agent__ permission prefix")
	}
}

func TestBuildMCPServersJSON_NoGateway(t *testing.T) {
	cfg := host.WorkspaceConfig{
		AgentMCP:    true,
		Backend:     "local",
		ProjectRoot: "/tmp/test",
		Servers: config.MCPServersConfig{
			Servers: map[string]config.MCPServerEntry{
				"elastic": {Local: config.MCPServerLocal{Command: "npx"}},
			},
		},
	}

	servers := BuildMCPServersJSON(cfg)

	// Should have fracta-agent subprocess entry
	if _, ok := servers["fracta-agent"]; !ok {
		t.Error("non-gateway mode should have fracta-agent entry")
	}

	// Should NOT have "fracta" URL entry
	if _, ok := servers["fracta"]; ok {
		t.Error("non-gateway mode should not have 'fracta' URL entry")
	}

	// Should have individual backend entries
	if _, ok := servers["elastic"]; !ok {
		t.Error("non-gateway mode should have individual backend entries")
	}
}

func TestWriteWorkspace_NoGatewayPermissions(t *testing.T) {
	dir := t.TempDir()
	h := Host{}

	err := h.WriteWorkspace(dir, []string{"Bash(ls *)"}, host.WorkspaceConfig{
		AgentMCP:    true,
		Backend:     "local",
		ProjectRoot: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("WriteWorkspace: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("reading settings.json: %v", err)
	}

	var settings struct {
		Permissions struct{ Allow []string } `json:"permissions"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parsing settings.json: %v", err)
	}

	// Check that non-gateway mode uses mcp__fracta-agent__ prefix
	hasOldPrefix := false
	hasFractaOnly := false
	for _, tool := range settings.Permissions.Allow {
		if strings.HasPrefix(tool, "mcp__fracta-agent__") {
			hasOldPrefix = true
		}
		if tool == "mcp__fracta__*" {
			hasFractaOnly = true
		}
	}

	if !hasOldPrefix {
		t.Error("non-gateway mode should use mcp__fracta-agent__ permission prefix")
	}
	if hasFractaOnly {
		t.Error("non-gateway mode should NOT have mcp__fracta__* wildcard")
	}
}

func TestBuildMCPServersJSON_K8sConfigPath(t *testing.T) {
	cfg := host.WorkspaceConfig{
		AgentMCP:   true,
		Backend:    "kubernetes",
		ConfigPath: "/Users/me/fracta/fracta.yaml", // host-local path (should be overridden)
	}

	servers := BuildMCPServersJSON(cfg)
	agent := servers["fracta-agent"].(map[string]interface{})
	args := agent["args"].([]string)
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "--config /etc/fracta/agent-config.yaml") {
		t.Errorf("K8s backend should use /etc/fracta/agent-config.yaml, got args: %v", args)
	}
	if strings.Contains(joined, "/Users/me/fracta/fracta.yaml") {
		t.Errorf("K8s backend should not use host-local config path, got args: %v", args)
	}
}

// === Golden tests for extracted instructions ===
//
// These capture the full rendered output and compare against golden files.
// Set UPDATE_GOLDEN=1 to regenerate: UPDATE_GOLDEN=1 go test -run Golden

func goldenTest(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated golden file %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden file %s not found — run with -update to create: %v", path, err)
	}
	if got != string(want) {
		t.Errorf("output differs from golden file %s — run with -update to regenerate", path)
	}
}

func TestGolden_BaseOnly(t *testing.T) {
	h := Host{}
	boot := h.Bootstrap("test-agent", "main", "Contract.")
	goldenTest(t, "golden-base-only.txt", boot.FileBody)
}

func TestGolden_Full(t *testing.T) {
	h := Host{}
	cfg := host.WorkspaceConfig{
		GraphAddr:   "localhost:6379",
		StrategyDir: "/opt/strategies",
	}
	boot := h.BootstrapWithConfig("test-agent", "main", "Contract.", cfg)
	goldenTest(t, "golden-full.txt", boot.FileBody)
}

func TestGolden_WithObjective(t *testing.T) {
	got := agentInstructions("test-agent", "main", agentpolicy.InstructionOpts{
		ObjectiveID:          "obj-123",
		ObjectiveDescription: "Investigate lateral movement",
		MissionID:            42,
	})
	goldenTest(t, "golden-with-objective.txt", got)
}

// === Golden tests for settings.json and .mcp.json ===
//
// These capture the full rendered settings.json and .mcp.json output for
// multiple spawn modes. Set UPDATE_GOLDEN=1 to regenerate.

// goldenWriteWorkspace runs WriteWorkspace with the given config and returns
// the rendered settings.json and .mcp.json content. The fracta binary path
// (from os.Executable) is normalized to "fracta" for deterministic comparison.
func goldenWriteWorkspace(t *testing.T, allowedTools []string, cfg host.WorkspaceConfig) (settingsJSON, mcpJSON string) {
	t.Helper()
	dir := t.TempDir()
	h := Host{}
	if err := h.WriteWorkspace(dir, allowedTools, cfg); err != nil {
		t.Fatalf("WriteWorkspace: %v", err)
	}
	sData, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("reading settings.json: %v", err)
	}
	mData, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatalf("reading .mcp.json: %v", err)
	}
	// Normalize the fracta binary path in .mcp.json to "fracta" so golden files
	// are deterministic across machines and test runs.
	mcpStr := string(mData)
	if exe, err := os.Executable(); err == nil && exe != "fracta" {
		mcpStr = strings.ReplaceAll(mcpStr, exe, "fracta")
	}
	return string(sData), mcpStr
}

func TestGoldenSettingsJSON_LocalSubprocess(t *testing.T) {
	settings, _ := goldenWriteWorkspace(t,
		[]string{"Bash(*)", "Read", "Write"},
		host.WorkspaceConfig{
			AgentMCP:    true,
			Backend:     "local",
			ProjectRoot: "/tmp/test-project",
			Servers: config.MCPServersConfig{
				Servers: map[string]config.MCPServerEntry{
					"elastic": {Local: config.MCPServerLocal{Command: "npx", Args: []string{"-y", "@elastic/mcp-server"}}},
				},
			},
		},
	)
	goldenTest(t, "golden-settings-local-subprocess.json", settings)
}

func TestGoldenMCPJSON_LocalSubprocess(t *testing.T) {
	_, mcp := goldenWriteWorkspace(t,
		[]string{"Bash(*)", "Read", "Write"},
		host.WorkspaceConfig{
			AgentMCP:    true,
			Backend:     "local",
			ProjectRoot: "/tmp/test-project",
			Servers: config.MCPServersConfig{
				Servers: map[string]config.MCPServerEntry{
					"elastic": {Local: config.MCPServerLocal{Command: "npx", Args: []string{"-y", "@elastic/mcp-server"}}},
				},
			},
		},
	)
	goldenTest(t, "golden-mcp-local-subprocess.json", mcp)
}

func TestGoldenSettingsJSON_K8sQueued(t *testing.T) {
	settings, _ := goldenWriteWorkspace(t,
		[]string{"Bash(*)", "Read", "Write"},
		host.WorkspaceConfig{
			AgentMCP:    true,
			Backend:     "kubernetes",
			ProjectRoot: "/workspace",
			ConfigPath:  "/etc/fracta/fracta.yaml",
			GraphAddr:   "falkordb.fracta.svc:6379",
			StrategyDir: "/opt/strategies",
			GatewayURL:  "http://fracta-gateway.fracta.svc:8080",
			AgentTask:   "scout-1",
			Servers: config.MCPServersConfig{
				Servers: map[string]config.MCPServerEntry{
					"elastic": {Kubernetes: config.MCPServerKubernetes{URL: "http://elastic:8000"}},
				},
			},
		},
	)
	goldenTest(t, "golden-settings-k8s-queued.json", settings)
}

func TestGoldenMCPJSON_K8sQueued(t *testing.T) {
	_, mcp := goldenWriteWorkspace(t,
		[]string{"Bash(*)", "Read", "Write"},
		host.WorkspaceConfig{
			AgentMCP:    true,
			Backend:     "kubernetes",
			ProjectRoot: "/workspace",
			ConfigPath:  "/etc/fracta/fracta.yaml",
			GraphAddr:   "falkordb.fracta.svc:6379",
			StrategyDir: "/opt/strategies",
			GatewayURL:  "http://fracta-gateway.fracta.svc:8080",
			AgentTask:   "scout-1",
			Servers: config.MCPServersConfig{
				Servers: map[string]config.MCPServerEntry{
					"elastic": {Kubernetes: config.MCPServerKubernetes{URL: "http://elastic:8000"}},
				},
			},
		},
	)
	goldenTest(t, "golden-mcp-k8s-queued.json", mcp)
}

func TestGoldenSettingsJSON_ObjectiveMission(t *testing.T) {
	settings, _ := goldenWriteWorkspace(t,
		[]string{"Bash(*)", "Read", "Write"},
		host.WorkspaceConfig{
			AgentMCP:    true,
			Backend:     "local",
			ProjectRoot: "/tmp/test-project",
			ObjectiveID: "obj-456",
			MissionID:   99,
			AgentTask:   "hunter-3",
		},
	)
	goldenTest(t, "golden-settings-objective-mission.json", settings)
}

func TestGoldenMCPJSON_ObjectiveMission(t *testing.T) {
	_, mcp := goldenWriteWorkspace(t,
		[]string{"Bash(*)", "Read", "Write"},
		host.WorkspaceConfig{
			AgentMCP:    true,
			Backend:     "local",
			ProjectRoot: "/tmp/test-project",
			ObjectiveID: "obj-456",
			MissionID:   99,
			AgentTask:   "hunter-3",
		},
	)
	goldenTest(t, "golden-mcp-objective-mission.json", mcp)
}

func TestGoldenSettingsJSON_GatewayOnly(t *testing.T) {
	settings, _ := goldenWriteWorkspace(t,
		[]string{"Bash(*)", "Read"},
		host.WorkspaceConfig{
			AgentMCP:    true,
			Backend:     "kubernetes",
			ProjectRoot: "/workspace",
			GatewayURL:  "http://fracta-gateway.fracta.svc:8080",
			AgentTask:   "analyst-1",
		},
	)
	goldenTest(t, "golden-settings-gateway-only.json", settings)
}

func TestGoldenMCPJSON_GatewayOnly(t *testing.T) {
	_, mcp := goldenWriteWorkspace(t,
		[]string{"Bash(*)", "Read"},
		host.WorkspaceConfig{
			AgentMCP:    true,
			Backend:     "kubernetes",
			ProjectRoot: "/workspace",
			GatewayURL:  "http://fracta-gateway.fracta.svc:8080",
			AgentTask:   "analyst-1",
		},
	)
	goldenTest(t, "golden-mcp-gateway-only.json", mcp)
}

func TestGoldenSettingsJSON_NoAgentMCP(t *testing.T) {
	settings, _ := goldenWriteWorkspace(t,
		[]string{"Bash(*)", "Read"},
		host.WorkspaceConfig{
			AgentMCP: false,
			Backend:  "local",
		},
	)
	goldenTest(t, "golden-settings-no-agent-mcp.json", settings)
}

func TestGoldenMCPJSON_NoAgentMCP(t *testing.T) {
	_, mcp := goldenWriteWorkspace(t,
		[]string{"Bash(*)", "Read"},
		host.WorkspaceConfig{
			AgentMCP: false,
			Backend:  "local",
		},
	)
	goldenTest(t, "golden-mcp-no-agent-mcp.json", mcp)
}

func TestGoldenSettingsJSON_CredentialPlan(t *testing.T) {
	settings, _ := goldenWriteWorkspace(t,
		[]string{"Bash(*)", "Read", "Write"},
		host.WorkspaceConfig{
			AgentMCP: true,
			Backend:  "kubernetes",
			CredentialOutput: &credentials.CredentialOutput{
				Plan: &credentials.CredentialPlan{
					Binding: &credentials.CredentialBinding{
						Type:                "claude_api_key_helper",
						RuntimeAuthResolver: "bedrock_helper",
					},
					RuntimeAuthResolver: &credentials.CredentialResolver{
						Type:    "command",
						Command: "/usr/local/bin/fetch-bedrock-token",
						TTLMs:   30000,
					},
					Env: map[string]string{
						"CLAUDE_CODE_USE_BEDROCK":       "1",
						"CLAUDE_CODE_SKIP_BEDROCK_AUTH": "1",
					},
				},
			},
		},
	)
	goldenTest(t, "golden-settings-credential-plan.json", settings)
}
