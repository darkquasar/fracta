package agentpolicy

import (
	"sort"
	"strings"
	"testing"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/host"
)

func TestBuildMCPTopology_GatewayMode(t *testing.T) {
	cfg := host.WorkspaceConfig{
		GatewayURL: "http://fracta-gateway:8080",
		AgentTask:  "scout-1",
		Backend:    "kubernetes",
		Servers: config.MCPServersConfig{
			Servers: map[string]config.MCPServerEntry{
				"elastic": {Kubernetes: config.MCPServerKubernetes{URL: "http://elastic:8000"}},
			},
		},
	}

	topo := BuildMCPTopology(cfg)

	if topo.GatewayURL != "http://fracta-gateway:8080" {
		t.Errorf("GatewayURL = %q, want http://fracta-gateway:8080", topo.GatewayURL)
	}
	if topo.AgentTask != "scout-1" {
		t.Errorf("AgentTask = %q, want scout-1", topo.AgentTask)
	}
	if topo.Agent != nil {
		t.Error("Agent should be nil in gateway mode")
	}
	if len(topo.Backends) != 0 {
		t.Errorf("Backends should be empty in gateway mode, got %d", len(topo.Backends))
	}
}

func TestBuildMCPTopology_LocalMode(t *testing.T) {
	cfg := host.WorkspaceConfig{
		Backend:     "local",
		ProjectRoot: "/tmp/test",
		ConfigPath:  "/etc/fracta/fracta.yaml",
		GraphAddr:   "localhost:6379",
		StrategyDir: "/opt/strategies",
		AgentTask:   "worker-1",
		Servers: config.MCPServersConfig{
			Servers: map[string]config.MCPServerEntry{
				"elastic": {Local: config.MCPServerLocal{
					Command: "npx",
					Args:    []string{"@elastic/mcp-server"},
				}},
			},
		},
	}

	topo := BuildMCPTopology(cfg)

	if topo.GatewayURL != "" {
		t.Errorf("GatewayURL should be empty, got %q", topo.GatewayURL)
	}
	if topo.Agent == nil {
		t.Fatal("Agent should not be nil in local mode")
	}

	joined := strings.Join(topo.Agent.Args, " ")
	if !strings.Contains(joined, "--root /tmp/test") {
		t.Errorf("missing --root in args: %v", topo.Agent.Args)
	}
	if !strings.Contains(joined, "--config /etc/fracta/fracta.yaml") {
		t.Errorf("missing --config in args: %v", topo.Agent.Args)
	}
	if !strings.Contains(joined, "--graph-addr localhost:6379") {
		t.Errorf("missing --graph-addr in args: %v", topo.Agent.Args)
	}
	if !strings.Contains(joined, "--strategy-dir /opt/strategies") {
		t.Errorf("missing --strategy-dir in args: %v", topo.Agent.Args)
	}
	if !strings.Contains(joined, "--agent-task worker-1") {
		t.Errorf("missing --agent-task in args: %v", topo.Agent.Args)
	}

	// Backend entries
	if len(topo.Backends) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(topo.Backends))
	}
	be, ok := topo.Backends["elastic"]
	if !ok {
		t.Fatal("missing elastic backend")
	}
	if be.LocalCommand != "npx" {
		t.Errorf("LocalCommand = %q, want npx", be.LocalCommand)
	}
	if len(be.LocalArgs) != 1 || be.LocalArgs[0] != "@elastic/mcp-server" {
		t.Errorf("LocalArgs = %v, want [@elastic/mcp-server]", be.LocalArgs)
	}
}

func TestBuildMCPTopology_LocalModeRemoteServer(t *testing.T) {
	cfg := host.WorkspaceConfig{
		Backend:     "local",
		ProjectRoot: "/tmp/test",
		Servers: config.MCPServersConfig{
			Servers: map[string]config.MCPServerEntry{
				"elastic": {
					Remote: &config.MCPServerRemote{URL: "http://elastic:8000/mcp"},
				},
			},
		},
	}

	topo := BuildMCPTopology(cfg)

	be, ok := topo.Backends["elastic"]
	if !ok {
		t.Fatal("missing elastic backend")
	}
	if be.RemoteURL != "http://elastic:8000/mcp" {
		t.Errorf("RemoteURL = %q, want http://elastic:8000/mcp", be.RemoteURL)
	}
	if be.LocalCommand != "" {
		t.Error("remote backend should not have LocalCommand")
	}
}

func TestBuildMCPTopology_K8sMode(t *testing.T) {
	cfg := host.WorkspaceConfig{
		Backend:    "kubernetes",
		ConfigPath: "/Users/me/fracta.yaml", // host-local, should be overridden
		AgentTask:  "scout-1",
		Servers: config.MCPServersConfig{
			Servers: map[string]config.MCPServerEntry{
				"elastic": {Kubernetes: config.MCPServerKubernetes{URL: "http://elastic:8000"}},
			},
		},
	}

	topo := BuildMCPTopology(cfg)

	if topo.Agent == nil {
		t.Fatal("Agent should not be nil in K8s mode")
	}
	if topo.Agent.Binary != "/usr/local/bin/fracta" {
		t.Errorf("Binary = %q, want /usr/local/bin/fracta", topo.Agent.Binary)
	}

	joined := strings.Join(topo.Agent.Args, " ")
	if !strings.Contains(joined, "--root /workspace") {
		t.Errorf("K8s should use /workspace as root: %v", topo.Agent.Args)
	}
	if !strings.Contains(joined, "--config /etc/fracta/agent-config.yaml") {
		t.Errorf("K8s should use /etc/fracta/agent-config.yaml: %v", topo.Agent.Args)
	}
	if strings.Contains(joined, "/Users/me/fracta.yaml") {
		t.Errorf("K8s should not use host-local config path: %v", topo.Agent.Args)
	}

	// K8s backend entries use RemoteURL
	be, ok := topo.Backends["elastic"]
	if !ok {
		t.Fatal("missing elastic backend")
	}
	if be.RemoteURL != "http://elastic:8000" {
		t.Errorf("RemoteURL = %q, want http://elastic:8000", be.RemoteURL)
	}
	if be.LocalCommand != "" {
		t.Error("K8s backend should not have LocalCommand")
	}
}

func TestBuildMCPTopology_OmitsEmptyFlags(t *testing.T) {
	cfg := host.WorkspaceConfig{
		Backend:     "local",
		ProjectRoot: "/tmp/test",
	}

	topo := BuildMCPTopology(cfg)

	joined := strings.Join(topo.Agent.Args, " ")
	if strings.Contains(joined, "--config") {
		t.Errorf("should not include --config when empty: %v", topo.Agent.Args)
	}
	if strings.Contains(joined, "--graph-addr") {
		t.Errorf("should not include --graph-addr when empty: %v", topo.Agent.Args)
	}
	if strings.Contains(joined, "--strategy-dir") {
		t.Errorf("should not include --strategy-dir when empty: %v", topo.Agent.Args)
	}
}

func TestBuildMCPTopology_WithObjectiveAndMission(t *testing.T) {
	cfg := host.WorkspaceConfig{
		Backend:     "local",
		ProjectRoot: "/tmp/test",
		ObjectiveID: "obj-42",
		MissionID:   7,
	}

	topo := BuildMCPTopology(cfg)
	joined := strings.Join(topo.Agent.Args, " ")

	if !strings.Contains(joined, "--objective-id obj-42") {
		t.Errorf("missing --objective-id: %v", topo.Agent.Args)
	}
	if !strings.Contains(joined, "--mission-id 7") {
		t.Errorf("missing --mission-id: %v", topo.Agent.Args)
	}
}

func TestBuildMCPTopology_BackendWithEnv(t *testing.T) {
	cfg := host.WorkspaceConfig{
		Backend:     "local",
		ProjectRoot: "/tmp/test",
		Servers: config.MCPServersConfig{
			Servers: map[string]config.MCPServerEntry{
				"vendor": {Local: config.MCPServerLocal{
					Command: "npx",
					Args:    []string{"vendor-server"},
					Env:     map[string]string{"API_KEY": "secret"},
				}},
			},
		},
	}

	topo := BuildMCPTopology(cfg)

	be := topo.Backends["vendor"]
	if be.LocalEnv == nil || be.LocalEnv["API_KEY"] != "secret" {
		t.Errorf("expected API_KEY env, got %v", be.LocalEnv)
	}
}

func TestServerNames(t *testing.T) {
	servers := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"elastic": {},
			"vendor":  {},
			"aha":     {},
		},
	}
	names := ServerNames(servers)
	sort.Strings(names)
	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(names))
	}
	if names[0] != "aha" || names[1] != "elastic" || names[2] != "vendor" {
		t.Errorf("unexpected names: %v", names)
	}
}
