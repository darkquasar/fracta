package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkquasar/fracta/internal/host"
)

func TestCapabilities_OpenCode(t *testing.T) {
	h := Host{}
	caps := h.Capabilities()
	if !caps.Stream {
		t.Error("Stream should be true (Phase 2)")
	}
	if !caps.AgentMCP {
		t.Error("AgentMCP should be true")
	}
	if !caps.ToolPermissions {
		t.Error("ToolPermissions should be true")
	}
	if !caps.ResumeToken {
		t.Error("ResumeToken should be true")
	}
	if !caps.StructuredEvents {
		t.Error("StructuredEvents should be true")
	}
}

func TestStartStream_Supported(t *testing.T) {
	// StartStream now delegates to StartServeSession. It will fail without
	// a real opencode binary, but it should not return ErrStreamNotSupported.
	h := Host{}
	_, err := h.StartStream(t.TempDir(), "model", "/tmp/log")
	if err == host.ErrStreamNotSupported {
		t.Error("StartStream should no longer return ErrStreamNotSupported (Phase 2)")
	}
	// Error is expected (no opencode binary available in test), but it should
	// be a different error, e.g. "executable file not found" or startup probe failure.
	if err != nil {
		t.Logf("expected non-ErrStreamNotSupported error: %v", err)
	}
}

func TestWriteWorkspace_WithMCP(t *testing.T) {
	dir := t.TempDir()
	h := Host{}

	err := h.WriteWorkspace(dir, nil, host.WorkspaceConfig{
		AgentMCP:   true,
		GatewayURL: "http://localhost:8080/mcp",
	})
	if err != nil {
		t.Fatalf("WriteWorkspace: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
	if err != nil {
		t.Fatalf("reading opencode.json: %v", err)
	}

	var cfg openCodeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parsing opencode.json: %v", err)
	}

	// MCP section should be present.
	if cfg.MCP == nil {
		t.Fatal("MCP section should be present when AgentMCP && GatewayURL set")
	}
	fracta, ok := cfg.MCP["fracta"]
	if !ok {
		t.Fatal("MCP should contain 'fracta' entry")
	}
	if fracta.Type != "remote" {
		t.Errorf("MCP type = %q, want 'remote'", fracta.Type)
	}
	if fracta.URL != "http://localhost:8080/mcp" {
		t.Errorf("MCP URL = %q, want gateway URL", fracta.URL)
	}
	if fracta.Headers["Authorization"] != "Bearer {env:FRACTA_GATEWAY_TOKEN}" {
		t.Errorf("MCP auth header = %q, want Bearer token template", fracta.Headers["Authorization"])
	}

	// Permission section should always be present.
	if cfg.Permission == nil {
		t.Fatal("Permission section should always be present")
	}
	if cfg.Permission["task"] != "deny" {
		t.Errorf("Permission task = %v, want 'deny' (subagent mitigation)", cfg.Permission["task"])
	}
}

func TestWriteWorkspace_NoMCP_GatewayEmpty(t *testing.T) {
	dir := t.TempDir()
	h := Host{}

	err := h.WriteWorkspace(dir, nil, host.WorkspaceConfig{
		AgentMCP:   true,
		GatewayURL: "", // empty — no HTTP endpoint available
	})
	if err != nil {
		t.Fatalf("WriteWorkspace: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
	if err != nil {
		t.Fatalf("reading opencode.json: %v", err)
	}

	var cfg openCodeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parsing opencode.json: %v", err)
	}

	// MCP section should be absent (no gateway URL).
	if cfg.MCP != nil {
		t.Error("MCP section should be absent when GatewayURL is empty")
	}

	// Permission section should still be present.
	if cfg.Permission == nil {
		t.Fatal("Permission section should always be present")
	}
	if cfg.Permission["task"] != "deny" {
		t.Errorf("Permission task = %v, want 'deny'", cfg.Permission["task"])
	}
}

func TestWriteWorkspace_NoMCP_AgentMCPFalse(t *testing.T) {
	dir := t.TempDir()
	h := Host{}

	err := h.WriteWorkspace(dir, nil, host.WorkspaceConfig{
		AgentMCP:   false,
		GatewayURL: "http://example.com/mcp",
	})
	if err != nil {
		t.Fatalf("WriteWorkspace: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
	if err != nil {
		t.Fatalf("reading opencode.json: %v", err)
	}

	var cfg openCodeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parsing opencode.json: %v", err)
	}

	// MCP section should be absent (AgentMCP is false).
	if cfg.MCP != nil {
		t.Error("MCP section should be absent when AgentMCP is false")
	}
}

func TestWriteWorkspace_Permissions_TaskDeny(t *testing.T) {
	dir := t.TempDir()
	h := Host{}

	err := h.WriteWorkspace(dir, nil, host.WorkspaceConfig{})
	if err != nil {
		t.Fatalf("WriteWorkspace: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
	if err != nil {
		t.Fatalf("reading opencode.json: %v", err)
	}

	var cfg openCodeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parsing opencode.json: %v", err)
	}

	if cfg.Permission["task"] != "deny" {
		t.Errorf("task permission should always be 'deny', got %v", cfg.Permission["task"])
	}
	if cfg.Permission["read"] != "allow" {
		t.Errorf("read permission = %v, want 'allow'", cfg.Permission["read"])
	}
	if cfg.Permission["edit"] != "allow" {
		t.Errorf("edit permission = %v, want 'allow'", cfg.Permission["edit"])
	}
	if cfg.Permission["*"] != "ask" {
		t.Errorf("default permission = %v, want 'ask'", cfg.Permission["*"])
	}
}

func TestWriteWorkspace_Permissions_WithAllowedTools(t *testing.T) {
	dir := t.TempDir()
	h := Host{}

	err := h.WriteWorkspace(dir, []string{"Bash(*)", "WebFetch(*)"}, host.WorkspaceConfig{})
	if err != nil {
		t.Fatalf("WriteWorkspace: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
	if err != nil {
		t.Fatalf("reading opencode.json: %v", err)
	}

	var cfg openCodeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parsing opencode.json: %v", err)
	}

	if cfg.Permission["bash"] != "allow" {
		t.Errorf("bash permission = %v, want 'allow' (from Bash(*) allowed tool)", cfg.Permission["bash"])
	}
	if cfg.Permission["webfetch"] != "allow" {
		t.Errorf("webfetch permission = %v, want 'allow'", cfg.Permission["webfetch"])
	}
	// task:deny must survive regardless of allowedTools.
	if cfg.Permission["task"] != "deny" {
		t.Errorf("task permission should remain 'deny', got %v", cfg.Permission["task"])
	}
}

func TestBootstrap_AGENTS_MD(t *testing.T) {
	h := Host{}
	result := h.Bootstrap("my-task", "main", "Fix the auth bug")

	if result.FileName != "AGENTS.md" {
		t.Errorf("FileName = %q, want AGENTS.md", result.FileName)
	}

	if !strings.Contains(result.FileBody, "Fix the auth bug") {
		t.Error("FileBody should contain the contract text")
	}

	if !strings.Contains(result.FileBody, "my-task") {
		t.Error("FileBody should contain the task name")
	}

	if !strings.Contains(result.FileBody, "feature/my-task") {
		t.Error("FileBody should reference the feature branch")
	}

	if !strings.Contains(result.FileBody, "main") {
		t.Error("FileBody should reference the base branch")
	}

	if !strings.Contains(result.InitialPrompt, "AGENTS.md") {
		t.Error("InitialPrompt should reference AGENTS.md")
	}

	if !strings.Contains(result.InitialPrompt, "my-task") {
		t.Error("InitialPrompt should contain task name")
	}
}

func TestBootstrap_EmptyContract(t *testing.T) {
	h := Host{}
	result := h.Bootstrap("task-1", "develop", "")

	if result.FileName != "AGENTS.md" {
		t.Errorf("FileName = %q, want AGENTS.md", result.FileName)
	}
	if !strings.Contains(result.FileBody, "task-1") {
		t.Error("should still contain agent instructions")
	}
}

func TestBootstrap_FileWritable(t *testing.T) {
	h := Host{}
	result := h.Bootstrap("task", "main", "Do something")

	dir := t.TempDir()
	path := filepath.Join(dir, result.FileName)
	if err := os.WriteFile(path, []byte(result.FileBody), 0644); err != nil {
		t.Fatalf("failed to write bootstrap file: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read back: %v", err)
	}
	if !strings.Contains(string(data), "Do something") {
		t.Error("written file should contain contract")
	}
}

func TestHostInterface_Compliance(t *testing.T) {
	var _ host.Host = Host{}

	h := Host{}
	h.Capabilities()
	h.Bootstrap("t", "main", "contract")
	h.BuildBatchCommand("prompt", "model", "")
	h.BuildBatchCommand("prompt", "", "token")
	h.WriteWorkspace(t.TempDir(), nil, host.WorkspaceConfig{})
	h.StartStream("", "", "")

	// ParseBatchOutput with valid input
	ndjson := `{"type":"text","sessionID":"ses_abc","part":{"type":"text","text":"hello"}}
`
	result, err := h.ParseBatchOutput([]byte(ndjson), nil)
	if err != nil {
		t.Fatalf("ParseBatchOutput: %v", err)
	}
	if result.ResumeToken != "ses_abc" {
		t.Errorf("ResumeToken = %q, want ses_abc", result.ResumeToken)
	}
}

func TestAgentInstructions_Content(t *testing.T) {
	instructions := agentInstructions("test-task", "main")

	checks := []string{
		"test-task",
		"feature/test-task",
		"git add",
		"git commit",
		"git merge main",
		"fracta_send",
		"fracta_inbox",
	}

	for _, check := range checks {
		if !strings.Contains(instructions, check) {
			t.Errorf("instructions should contain %q", check)
		}
	}
}

func TestBuildPermissions_Default(t *testing.T) {
	perms := buildPermissions(nil, "")
	if perms["task"] != "deny" {
		t.Errorf("task = %v, want deny", perms["task"])
	}
	if perms["*"] != "ask" {
		t.Errorf("default = %v, want ask", perms["*"])
	}
	if perms["read"] != "allow" {
		t.Errorf("read = %v, want allow", perms["read"])
	}
	if perms["external_directory"] != nil {
		t.Errorf("external_directory should be absent when workspacePath is empty")
	}
	if perms["mcp"] != nil {
		t.Errorf("mcp should be absent — use concrete tool keys instead")
	}
}

func TestBuildPermissions_ExternalDirectory(t *testing.T) {
	perms := buildPermissions(nil, "/workspace/agents/test-agent")
	extDir, ok := perms["external_directory"].(map[string]interface{})
	if !ok {
		t.Fatalf("external_directory missing or wrong type: %v", perms["external_directory"])
	}
	if extDir["/workspace/agents/test-agent"] != "allow" {
		t.Errorf("root path not allowed: %v", extDir)
	}
	if extDir["/workspace/agents/test-agent/*"] != "allow" {
		t.Errorf("subtree path not allowed: %v", extDir)
	}
}

func TestWriteWorkspace_MCPToolPermissions(t *testing.T) {
	dir := t.TempDir()
	h := Host{}
	cfg := host.WorkspaceConfig{
		AgentMCP:       true,
		GatewayURL:     "http://fracta-gateway:8080",
		AgentTask:      "test-agent",
		RuntimeWorkDir: "/workspace/agents/test-agent",
	}
	if err := h.WriteWorkspace(dir, nil, cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oc map[string]interface{}
	if err := json.Unmarshal(data, &oc); err != nil {
		t.Fatal(err)
	}

	perms, ok := oc["permission"].(map[string]interface{})
	if !ok {
		t.Fatal("permission not found or wrong type")
	}

	for _, key := range []string{"fracta_fracta_list", "fracta_fracta_inbox", "fracta_graph_query", "fracta_search_tool"} {
		if perms[key] != "allow" {
			t.Errorf("expected %s: allow, got %v", key, perms[key])
		}
	}

	if perms["mcp"] != nil {
		t.Errorf("generic mcp key should be absent")
	}
}

func TestStreamPermissionRules(t *testing.T) {
	rules := StreamPermissionRules("/workspace/agents/my-agent", "")

	// task:deny + 2 external_directory + 20 MCP tools (no objective)
	if len(rules) < 23 {
		t.Fatalf("expected at least 23 rules, got %d", len(rules))
	}
	if rules[0].Permission != "task" || rules[0].Action != "deny" {
		t.Errorf("first rule should be task:deny, got %+v", rules[0])
	}
	if rules[1].Permission != "external_directory" || rules[1].Pattern != "/workspace/agents/my-agent" {
		t.Errorf("second rule should allow workspace root, got %+v", rules[1])
	}
	if rules[2].Permission != "external_directory" || rules[2].Pattern != "/workspace/agents/my-agent/*" {
		t.Errorf("third rule should allow workspace subtree, got %+v", rules[2])
	}

	// Check that MCP tool rules are present.
	ruleSet := make(map[string]bool)
	for _, r := range rules {
		ruleSet[r.Permission] = true
	}
	for _, key := range []string{"fracta_fracta_list", "fracta_fracta_inbox", "fracta_graph_query", "fracta_search_tool"} {
		if !ruleSet[key] {
			t.Errorf("missing MCP tool rule: %s", key)
		}
	}
}

func TestStreamPermissionRules_EmptyWorkdir(t *testing.T) {
	rules := StreamPermissionRules("", "")

	// task:deny + 20 MCP tools (no workspace, no objective)
	if len(rules) < 21 {
		t.Fatalf("expected at least 21 rules, got %d", len(rules))
	}
	if rules[0].Permission != "task" || rules[0].Action != "deny" {
		t.Errorf("first rule should be task:deny, got %+v", rules[0])
	}
}

// === Golden test ===

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
		t.Fatalf("golden file %s not found — run with UPDATE_GOLDEN=1 to create: %v", path, err)
	}
	if got != string(want) {
		t.Errorf("output differs from golden file %s — run with UPDATE_GOLDEN=1 to regenerate", path)
	}
}

func TestGolden_OpenCodeBase(t *testing.T) {
	h := Host{}
	boot := h.Bootstrap("test-agent", "main", "Contract.")
	goldenTest(t, "golden-opencode-base.txt", boot.FileBody)
}
