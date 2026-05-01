package agentpolicy

import (
	"fmt"
	"os"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/host"
)

// MCPTopology is the host-neutral description of MCP server connectivity.
// Host adapters consume this and serialize to their own format (.mcp.json
// for Claude, .codex/config.toml for Codex).
type MCPTopology struct {
	// GatewayURL is non-empty when running in gateway mode. In this mode,
	// Agent and Backends are nil/empty — everything goes through the gateway.
	GatewayURL string

	// AgentTask is the agent's task name, used in gateway URL path.
	AgentTask string

	// Agent is the fracta agent-mode MCP server subprocess config.
	// Nil in gateway mode.
	Agent *MCPAgentArgs

	// Backends maps server name to its connection config.
	// Empty in gateway mode (backends proxied through gateway).
	Backends map[string]MCPBackendEntry
}

// MCPAgentArgs describes the fracta CLI subprocess that serves as the
// agent-mode MCP server.
type MCPAgentArgs struct {
	Binary string
	Args   []string
}

// MCPBackendEntry describes a single MCP backend server connection.
// Exactly one of the field groups is populated based on backend type.
type MCPBackendEntry struct {
	// Local subprocess mode
	LocalCommand string
	LocalArgs    []string
	LocalEnv     map[string]string

	// Remote URL mode (K8s or other remote backends)
	RemoteURL string
}

// BuildMCPTopology constructs the host-neutral MCP topology from a
// WorkspaceConfig. This absorbs all K8s vs local conditionals that
// previously lived in claude/delivery.go's BuildMCPServersJSON.
func BuildMCPTopology(cfg host.WorkspaceConfig) MCPTopology {
	if cfg.GatewayURL != "" {
		return MCPTopology{
			GatewayURL: cfg.GatewayURL,
			AgentTask:  cfg.AgentTask,
		}
	}

	// Build agent-mode MCP server args.
	var rootPath string
	if cfg.Backend == "kubernetes" {
		rootPath = "/workspace"
	} else {
		rootPath = cfg.ProjectRoot
	}

	agentArgs := []string{"serve", "--agent-mode", "--root", rootPath}

	if cfg.Backend == "kubernetes" {
		agentArgs = append(agentArgs, "--config", "/etc/fracta/agent-config.yaml")
	} else if cfg.ConfigPath != "" {
		agentArgs = append(agentArgs, "--config", cfg.ConfigPath)
	}
	if cfg.GraphAddr != "" {
		agentArgs = append(agentArgs, "--graph-addr", cfg.GraphAddr)
	}
	if cfg.StrategyDir != "" {
		agentArgs = append(agentArgs, "--strategy-dir", cfg.StrategyDir)
	}
	if cfg.AgentTask != "" {
		agentArgs = append(agentArgs, "--agent-task", cfg.AgentTask)
	}
	if cfg.ObjectiveID != "" {
		agentArgs = append(agentArgs, "--objective-id", cfg.ObjectiveID)
	}
	if cfg.MissionID != 0 {
		agentArgs = append(agentArgs, "--mission-id", fmt.Sprintf("%d", cfg.MissionID))
	}

	var binary string
	if cfg.Backend == "kubernetes" {
		binary = "/usr/local/bin/fracta"
	} else {
		var err error
		binary, err = os.Executable()
		if err != nil {
			binary = "fracta"
		}
	}

	// Build backend server entries.
	backends := make(map[string]MCPBackendEntry)
	for name, entry := range cfg.Servers.Servers {
		switch cfg.Backend {
		case "kubernetes":
			if remote, ok := entry.EffectiveRemote(); ok {
				backends[name] = MCPBackendEntry{
					RemoteURL: remote.URL,
				}
			}
		default:
			if entry.Remote != nil && entry.Remote.URL != "" {
				backends[name] = MCPBackendEntry{
					RemoteURL: entry.Remote.URL,
				}
			} else if entry.Local.Command != "" {
				be := MCPBackendEntry{
					LocalCommand: entry.Local.Command,
				}
				if len(entry.Local.Args) > 0 {
					be.LocalArgs = entry.Local.Args
				}
				if len(entry.Local.Env) > 0 {
					be.LocalEnv = entry.Local.Env
				}
				backends[name] = be
			} else if entry.Kubernetes.URL != "" {
				backends[name] = MCPBackendEntry{
					RemoteURL: entry.Kubernetes.URL,
				}
			}
		}
	}

	return MCPTopology{
		AgentTask: cfg.AgentTask,
		Agent: &MCPAgentArgs{
			Binary: binary,
			Args:   agentArgs,
		},
		Backends: backends,
	}
}

// ServerNames returns the sorted list of backend server names from a
// MCPServersConfig. Useful for computing backend wildcards.
func ServerNames(servers config.MCPServersConfig) []string {
	names := make([]string, 0, len(servers.Servers))
	for name := range servers.Servers {
		names = append(names, name)
	}
	return names
}
