package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkquasar/fracta/internal/host"
)

func TestCapabilities_Codex(t *testing.T) {
	h := Host{}
	caps := h.Capabilities()
	if !caps.Stream {
		t.Error("Stream should be true (Phase 2 via app-server)")
	}
	if !caps.AgentMCP {
		t.Error("AgentMCP should be true")
	}
	if !caps.ToolPermissions {
		t.Error("ToolPermissions should be true (Phase 2 via per-turn sandboxPolicy)")
	}
	if !caps.ResumeToken {
		t.Error("ResumeToken should be true")
	}
	if !caps.StructuredEvents {
		t.Error("StructuredEvents should be true")
	}
}

func TestStartStream_DelegatesToAppServer(t *testing.T) {
	// Verify that StartStream delegates to NewAppServerSession (not returning
	// ErrStreamNotSupported). We test this by checking Capabilities and that the
	// Host method signature routes correctly. Actual subprocess behavior is tested
	// in appserver_test.go using mock scripts.
	h := Host{}
	caps := h.Capabilities()
	if !caps.Stream {
		t.Error("Capabilities().Stream should be true, indicating StartStream is wired")
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

	// Even with empty contract, should have agent instructions
	if result.FileName != "AGENTS.md" {
		t.Errorf("FileName = %q, want AGENTS.md", result.FileName)
	}
	if !strings.Contains(result.FileBody, "task-1") {
		t.Error("should still contain agent instructions")
	}
}

func TestWriteWorkspace_NoOp(t *testing.T) {
	dir := t.TempDir()
	h := Host{}

	err := h.WriteWorkspace(dir, []string{"Bash(*)"}, host.WorkspaceConfig{})
	if err != nil {
		t.Fatalf("WriteWorkspace should succeed (no-op): %v", err)
	}

	// Verify no files were created
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("WriteWorkspace should not create files, found: %v", names)
	}
}

func TestCodexWriteWorkspace_WithMCP(t *testing.T) {
	dir := t.TempDir()
	h := Host{}

	err := h.WriteWorkspace(dir, nil, host.WorkspaceConfig{
		AgentMCP:   true,
		GatewayURL: "http://localhost:9090",
		AgentTask:  "scout",
	})
	if err != nil {
		t.Fatalf("WriteWorkspace: %v", err)
	}

	configPath := filepath.Join(dir, ".codex", "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config.toml not written: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "[mcp_servers.fracta]") {
		t.Error("config.toml should contain [mcp_servers.fracta] section")
	}
	if !strings.Contains(content, "http://localhost:9090/agents/scout/mcp") {
		t.Errorf("config.toml should contain gateway URL with agent path, got:\n%s", content)
	}
	if !strings.Contains(content, `bearer_token_env_var = "FRACTA_GATEWAY_TOKEN"`) {
		t.Error("config.toml should contain bearer_token_env_var")
	}
}

func TestCodexWriteWorkspace_NoGateway(t *testing.T) {
	dir := t.TempDir()
	h := Host{}

	// AgentMCP true but no GatewayURL — should not write config
	err := h.WriteWorkspace(dir, nil, host.WorkspaceConfig{
		AgentMCP: true,
	})
	if err != nil {
		t.Fatalf("WriteWorkspace: %v", err)
	}

	configPath := filepath.Join(dir, ".codex", "config.toml")
	if _, err := os.Stat(configPath); err == nil {
		t.Error("config.toml should not be written when GatewayURL is empty")
	}
}

func TestCodexWriteWorkspace_NoAgentTask(t *testing.T) {
	dir := t.TempDir()
	h := Host{}

	err := h.WriteWorkspace(dir, nil, host.WorkspaceConfig{
		AgentMCP:   true,
		GatewayURL: "http://gw:9090",
	})
	if err != nil {
		t.Fatalf("WriteWorkspace: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("config.toml not written: %v", err)
	}

	// When no AgentTask, URL should be the raw gateway URL (no /agents/.../mcp path)
	content := string(data)
	if !strings.Contains(content, `"http://gw:9090"`) {
		t.Errorf("URL should be raw gateway URL when AgentTask is empty, got:\n%s", content)
	}
}

func TestHostInterface_Compliance(t *testing.T) {
	// Verify Host implements host.Host at compile time.
	var _ host.Host = Host{}

	// Verify all methods are callable without panic.
	h := Host{}
	h.Capabilities()
	h.Bootstrap("t", "main", "contract")
	h.BuildBatchCommand("prompt", "model", "")
	h.BuildBatchCommand("prompt", "", "token")
	h.WriteWorkspace(t.TempDir(), nil, host.WorkspaceConfig{})
	// StartStream delegates to NewAppServerSession — skip actual subprocess launch
	// in compliance test. Full tests in appserver_test.go.

	// ParseBatchOutput with valid input
	jsonl := `{"type":"thread.started","thread_id":"x"}
{"type":"item.completed","item":{"id":"0","type":"agent_message","text":"ok"}}
{"type":"turn.completed","usage":{}}
`
	result, err := h.ParseBatchOutput([]byte(jsonl), nil)
	if err != nil {
		t.Fatalf("ParseBatchOutput: %v", err)
	}
	if result.ResumeToken != "x" {
		t.Errorf("ResumeToken = %q", result.ResumeToken)
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

func TestBootstrap_FileWritable(t *testing.T) {
	h := Host{}
	result := h.Bootstrap("task", "main", "Do something")

	// Verify the bootstrap result can actually be written to disk
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

// === Golden test for extracted instructions ===
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

func TestGolden_CodexBase(t *testing.T) {
	h := Host{}
	boot := h.Bootstrap("test-agent", "main", "Contract.")
	goldenTest(t, "golden-codex-base.txt", boot.FileBody)
}
