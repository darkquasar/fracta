package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/host/claude"
)

func TestBuildMCPServersJSON_LocalBackend(t *testing.T) {
	mcp := host.WorkspaceConfig{
		AgentMCP: true,
		Servers: config.MCPServersConfig{
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
		},
		Backend:     "local",
		ProjectRoot: "/tmp/test-project",
	}

	servers := claude.BuildMCPServersJSON(mcp)

	// Must include fracta-agent
	if _, ok := servers["fracta-agent"]; !ok {
		t.Fatal("expected fracta-agent in servers")
	}

	// Must include elastic-mcp with local command
	em, ok := servers["elastic-mcp"]
	if !ok {
		t.Fatal("expected elastic-mcp in servers")
	}
	emMap := em.(map[string]interface{})
	if emMap["command"] != "npx" {
		t.Errorf("command = %v, want npx", emMap["command"])
	}
	args := emMap["args"].([]string)
	if len(args) != 2 || args[0] != "-y" {
		t.Errorf("args = %v, want [-y @elastic/mcp-server]", args)
	}

	// Must NOT have a "url" key (local mode)
	if _, ok := emMap["url"]; ok {
		t.Error("local mode should not have url key")
	}
}

func TestBuildMCPServersJSON_KubernetesBackend(t *testing.T) {
	mcp := host.WorkspaceConfig{
		AgentMCP: true,
		Servers: config.MCPServersConfig{
			Servers: map[string]config.MCPServerEntry{
				"vendor-mcp": {
					Local: config.MCPServerLocal{
						Command: "vendor-mcp-server",
					},
					Kubernetes: config.MCPServerKubernetes{
						URL: "http://vendor-mcp.fracta.svc.cluster.local:8080/sse",
					},
				},
			},
		},
		Backend:     "kubernetes",
		ProjectRoot: "/tmp/test-project",
	}

	servers := claude.BuildMCPServersJSON(mcp)

	// Must include fracta-agent
	if _, ok := servers["fracta-agent"]; !ok {
		t.Fatal("expected fracta-agent in servers")
	}

	// Must include vendor-mcp with URL
	pm, ok := servers["vendor-mcp"]
	if !ok {
		t.Fatal("expected vendor-mcp in servers")
	}
	pmMap := pm.(map[string]interface{})
	if pmMap["url"] != "http://vendor-mcp.fracta.svc.cluster.local:8080/sse" {
		t.Errorf("url = %v", pmMap["url"])
	}

	// Must NOT have "command" key (k8s mode)
	if _, ok := pmMap["command"]; ok {
		t.Error("kubernetes mode should not have command key")
	}
}

func TestBuildMCPServersJSON_KubernetesFractaAgent(t *testing.T) {
	mcp := host.WorkspaceConfig{
		AgentMCP:    true,
		Servers:     config.MCPServersConfig{},
		Backend:     "kubernetes",
		ProjectRoot: "/Users/me/project", // should be ignored in K8s mode
	}

	servers := claude.BuildMCPServersJSON(mcp)

	agent, ok := servers["fracta-agent"]
	if !ok {
		t.Fatal("expected fracta-agent in servers")
	}
	agentMap := agent.(map[string]interface{})

	// K8s mode: fixed binary path, PVC mount root
	if agentMap["command"] != "/usr/local/bin/fracta" {
		t.Errorf("command = %v, want /usr/local/bin/fracta", agentMap["command"])
	}
	args := agentMap["args"].([]string)
	wantArgs := []string{"serve", "--agent-mode", "--root", "/workspace", "--config", "/etc/fracta/agent-config.yaml"}
	if len(args) != len(wantArgs) {
		t.Fatalf("args = %v, want %v", args, wantArgs)
	}
	for i, a := range args {
		if a != wantArgs[i] {
			t.Errorf("args[%d] = %q, want %q", i, a, wantArgs[i])
		}
	}
}

func TestBuildMCPServersJSON_NoExternalServers(t *testing.T) {
	mcp := host.WorkspaceConfig{
		AgentMCP:    true,
		Servers:     config.MCPServersConfig{},
		Backend:     "local",
		ProjectRoot: "/tmp/test-project",
	}

	servers := claude.BuildMCPServersJSON(mcp)

	if len(servers) != 1 {
		t.Errorf("expected 1 server (fracta-agent), got %d", len(servers))
	}
	if _, ok := servers["fracta-agent"]; !ok {
		t.Fatal("expected fracta-agent")
	}
}

func TestWriteClaudeSettings_MCPPermissions(t *testing.T) {
	dir := t.TempDir()

	mcp := host.WorkspaceConfig{
		AgentMCP: true,
		Servers: config.MCPServersConfig{
			Servers: map[string]config.MCPServerEntry{
				"elastic-mcp": {
					Local: config.MCPServerLocal{Command: "elastic-mcp"},
				},
				"vendor-mcp": {
					Local: config.MCPServerLocal{Command: "vendor-mcp"},
				},
			},
		},
		Backend:     "local",
		ProjectRoot: "/tmp/test",
	}

	err := claude.Host{}.WriteWorkspace(dir, nil, mcp)
	if err != nil {
		t.Fatalf("claude.Host{}.WriteWorkspace: %v", err)
	}

	// Check settings.json contains MCP permissions
	data, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("reading settings.json: %v", err)
	}

	var settings struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parsing settings.json: %v", err)
	}

	hasElastic := false
	hasVendor := false
	hasFractaAgent := false
	for _, perm := range settings.Permissions.Allow {
		switch perm {
		case "mcp__elastic-mcp__*":
			hasElastic = true
		case "mcp__vendor-mcp__*":
			hasVendor = true
		case "mcp__fracta-agent__fracta_list":
			hasFractaAgent = true
		}
	}

	if !hasElastic {
		t.Error("missing mcp__elastic-mcp__* permission")
	}
	if !hasVendor {
		t.Error("missing mcp__vendor-mcp__* permission")
	}
	if !hasFractaAgent {
		t.Error("missing fracta-agent MCP permissions")
	}

	// Check .mcp.json contains the servers
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
	if _, ok := mcpJSON.MCPServers["elastic-mcp"]; !ok {
		t.Error("missing elastic-mcp in .mcp.json")
	}
	if _, ok := mcpJSON.MCPServers["vendor-mcp"]; !ok {
		t.Error("missing vendor-mcp in .mcp.json")
	}
}

func TestWriteClaudeSettings_NoAgentMCP(t *testing.T) {
	dir := t.TempDir()

	mcp := host.WorkspaceConfig{
		AgentMCP:    false,
		ProjectRoot: "/tmp/test",
	}

	err := claude.Host{}.WriteWorkspace(dir, []string{"Bash(git status)"}, mcp)
	if err != nil {
		t.Fatalf("claude.Host{}.WriteWorkspace: %v", err)
	}

	// .mcp.json should be empty object
	data, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatalf("reading .mcp.json: %v", err)
	}
	if string(data) != "{}\n" {
		t.Errorf(".mcp.json = %q, want {}", string(data))
	}
}

func TestBuildMCPServersJSON_EnvShape(t *testing.T) {
	mcp := host.WorkspaceConfig{
		AgentMCP: true,
		Servers: config.MCPServersConfig{
			Servers: map[string]config.MCPServerEntry{
				"elastic-mcp": {
					Local: config.MCPServerLocal{
						Command: "npx",
						Args:    []string{"-y", "@elastic/mcp-server"},
						Env: map[string]string{
							"ELASTIC_URL":     "https://example.com",
							"ELASTIC_API_KEY": "secret-key-123",
						},
					},
				},
			},
		},
		Backend:     "local",
		ProjectRoot: "/tmp/test",
	}

	servers := claude.BuildMCPServersJSON(mcp)
	em := servers["elastic-mcp"].(map[string]interface{})

	// env MUST be a map[string]string (Claude Code expects {"KEY": "val"}, not ["KEY=val"])
	envRaw, ok := em["env"]
	if !ok {
		t.Fatal("expected env key in elastic-mcp")
	}
	envMap, ok := envRaw.(map[string]string)
	if !ok {
		t.Fatalf("env should be map[string]string, got %T", envRaw)
	}
	if envMap["ELASTIC_URL"] != "https://example.com" {
		t.Errorf("ELASTIC_URL = %q", envMap["ELASTIC_URL"])
	}
	if envMap["ELASTIC_API_KEY"] != "secret-key-123" {
		t.Errorf("ELASTIC_API_KEY = %q", envMap["ELASTIC_API_KEY"])
	}
}
