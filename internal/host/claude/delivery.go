// Package claude implements the AgentDelivery interface for the Claude CLI host.
// It generates .claude/settings.json, .mcp.json, and agent instructions
// specific to Claude Code's expected formats.
package claude

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/darkquasar/fracta/internal/agentpolicy"
	"github.com/darkquasar/fracta/internal/assets"
	"github.com/darkquasar/fracta/internal/auth/credentials"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/fractalog"
)

//go:embed instructions/*
var instructionFS embed.FS

var instructions = assets.New(instructionFS, "instructions")

// Host implements the host.Host interface for the Claude CLI.
// It owns all Claude-specific protocol: workspace artifacts, batch command
// building/parsing, and streaming session management.
type Host struct{}

// Verify Host satisfies the interface at compile time.
var _ host.Host = Host{}

// PermissionBaseline returns Claude Code permission patterns for agent processes.
func (Host) PermissionBaseline() []string {
	return []string{
		"Bash(git add * && git commit -m *)",
		"Bash(git add . && git commit -m *)",
		"Bash(git add *)",
		"Bash(git commit *)",
		"Bash(git merge main)",
		"Bash(git merge green)",
		"Bash(git merge *)",
		"Bash(git status)",
		"Bash(git status *)",
		"Bash(git diff *)",
		"Bash(git log *)",
		"Bash(git branch *)",
		"Bash(go *)",
		"Bash(go get *)",
		"Bash(mkdir -p * && cd * && go mod init *)",
		"Bash(mkdir *)",
		"Bash(ls *)",
		"Bash(ls -a *)",
		"Bash(ls -l *)",
		"Bash(ls -la *)",
		"Bash(cat *)",
		"Bash(find *)",
		"Bash(grep *)",
	}
}

// WriteWorkspace writes .claude/settings.json and .mcp.json into the agent
// workspace so Claude Code finds them at startup.
func (d Host) WriteWorkspace(worktreePath string, allowedTools []string, cfg host.WorkspaceConfig) error {
	claudeDir := filepath.Join(worktreePath, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		return fmt.Errorf("creating .claude dir: %w", err)
	}

	tools := make([]string, len(allowedTools))
	copy(tools, allowedTools)
	tools = mergePermissions(tools, d.PermissionBaseline())

	if cfg.AgentMCP {
		prefix := agentpolicy.MCPPermissionPrefix(cfg.GatewayURL)
		serverNames := agentpolicy.ServerNames(cfg.Servers)
		wildcards := agentpolicy.BackendWildcards(cfg.GatewayURL, serverNames)
		tools = append(tools, agentpolicy.ExpandFractaTools(prefix, cfg.ObjectiveID, wildcards)...)
	}

	settings := map[string]interface{}{
		"permissions": map[string]interface{}{
			"allow": tools,
		},
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling settings: %w", err)
	}

	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("writing settings.json: %w", err)
	}

	mcpPath := filepath.Join(worktreePath, ".mcp.json")
	if cfg.AgentMCP {
		mcpServers := BuildMCPServersJSON(cfg)
		mcpConfig := map[string]interface{}{
			"mcpServers": mcpServers,
		}
		mcpData, err := json.MarshalIndent(mcpConfig, "", "  ")
		if err != nil {
			return fmt.Errorf("marshaling mcp config: %w", err)
		}
		if err := os.WriteFile(mcpPath, append(mcpData, '\n'), 0644); err != nil {
			return fmt.Errorf("writing .mcp.json: %w", err)
		}
	} else {
		if err := os.WriteFile(mcpPath, []byte("{}\n"), 0644); err != nil {
			return fmt.Errorf("writing .mcp.json: %w", err)
		}
	}

	// Write user-settings.json from credential output.
	// The generic entrypoint copies this to ~/.claude/settings.json at pod start.
	if cfg.CredentialOutput != nil {
		if err := writeUserSettings(worktreePath, cfg.CredentialOutput); err != nil {
			return fmt.Errorf("writing user-settings.json: %w", err)
		}
	}

	return nil
}

// writeUserSettings writes .fracta/user-settings.json by projecting CredentialOutput
// into Claude's expected format. Claude CLI reads apiKeyHelper from settings.json
// to fetch API keys.
//
// Projection depends on binding type:
//   - claude_api_key_helper: writes apiKeyHelper command + env with TTL
//   - bearer_env / token_file: no user-settings needed (credentials delivered via env/file)
func writeUserSettings(worktreePath string, output *credentials.CredentialOutput) error {
	if output.Plan == nil || output.Plan.Binding == nil {
		return nil // no binding = nothing to project
	}

	binding := output.Plan.Binding
	if binding.Type != "claude_api_key_helper" {
		return nil // only claude_api_key_helper needs user-settings.json
	}

	// Resolve the resolver command from the plan.
	if output.Plan.RuntimeAuthResolver == nil || output.Plan.RuntimeAuthResolver.Command == "" {
		return fmt.Errorf("claude_api_key_helper binding requires a resolver with a command")
	}

	fractaDir := filepath.Join(worktreePath, ".fracta")
	if err := os.MkdirAll(fractaDir, 0755); err != nil {
		return fmt.Errorf("creating .fracta dir: %w", err)
	}

	userSettings := map[string]interface{}{
		"apiKeyHelper": output.Plan.RuntimeAuthResolver.Command,
	}

	ttl := output.Plan.RuntimeAuthResolver.TTLMs
	if ttl <= 0 {
		ttl = 60000 // default 60s
	}

	env := make(map[string]string)
	for k, v := range output.Plan.Env {
		env[k] = v
	}
	env["CLAUDE_CODE_API_KEY_HELPER_TTL_MS"] = fmt.Sprintf("%d", ttl)

	userSettings["env"] = env

	data, err := json.MarshalIndent(userSettings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(fractaDir, "user-settings.json"), append(data, '\n'), 0644); err != nil {
		return err
	}

	// Stage 8: credentials.binding.project — log adapter projection summary.
	envKeys := make([]string, 0, len(env))
	for k := range env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	fractalog.Component("credentials").Info("credentials.binding.project",
		"binding_type", binding.Type,
		"helper_command", output.Plan.RuntimeAuthResolver.Command,
		"ttl_ms", ttl,
		"env_keys_written", strings.Join(envKeys, ","),
		"projected_file_path", filepath.Join(fractaDir, "user-settings.json"),
	)

	return nil
}

// BuildMCPServersJSON constructs the mcpServers map for .mcp.json.
// It consumes agentpolicy.BuildMCPTopology for host-neutral topology
// and serializes to Claude Code's expected JSON format.
func BuildMCPServersJSON(cfg host.WorkspaceConfig) map[string]interface{} {
	topo := agentpolicy.BuildMCPTopology(cfg)
	return serializeTopologyToClaudeJSON(topo)
}

// serializeTopologyToClaudeJSON converts an MCPTopology to Claude Code's
// .mcp.json format (map of server name to {command, args, env} or {type, url}).
func serializeTopologyToClaudeJSON(topo agentpolicy.MCPTopology) map[string]interface{} {
	servers := make(map[string]interface{})

	if topo.GatewayURL != "" {
		url := topo.GatewayURL + "/agents/" + topo.AgentTask + "/mcp"
		servers["fracta"] = map[string]interface{}{
			"type": "http",
			"url":  url,
		}
		return servers
	}

	if topo.Agent != nil {
		servers["fracta-agent"] = map[string]interface{}{
			"command": topo.Agent.Binary,
			"args":    topo.Agent.Args,
		}
	}

	for name, be := range topo.Backends {
		if be.RemoteURL != "" {
			servers[name] = map[string]interface{}{
				"url": be.RemoteURL,
			}
		} else if be.LocalCommand != "" {
			server := map[string]interface{}{
				"command": be.LocalCommand,
			}
			if len(be.LocalArgs) > 0 {
				server["args"] = be.LocalArgs
			}
			if len(be.LocalEnv) > 0 {
				server["env"] = be.LocalEnv
			}
			servers[name] = server
		}
	}

	return servers
}


// AgentInstructions returns the Claude-specific CLAUDE.md section for agents.
func (Host) AgentInstructions(task, baseBranch string) string {
	return agentInstructions(task, baseBranch, agentpolicy.InstructionOpts{})
}

// agentInstructions generates instructions with optional graph/strategy sections.
// The base template is Claude-specific; supplementary sections (graph, strategy,
// objective) come from the shared agentpolicy package.
func agentInstructions(task, baseBranch string, opts agentpolicy.InstructionOpts) string {
	base := instructions.MustRender("base-agent.md.tmpl", map[string]string{
		"Task":       task,
		"BaseBranch": baseBranch,
	})
	return base + agentpolicy.RenderInstructionSections(opts)
}

func mergePermissions(existing []string, baseline []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(baseline))
	result := make([]string, 0, len(existing)+len(baseline))
	for _, tool := range existing {
		if _, ok := seen[tool]; ok {
			continue
		}
		seen[tool] = struct{}{}
		result = append(result, tool)
	}
	for _, tool := range baseline {
		if _, ok := seen[tool]; ok {
			continue
		}
		seen[tool] = struct{}{}
		result = append(result, tool)
	}
	return result
}

// --- Host interface method implementations ---

// Bootstrap returns the Claude-specific task file and initial prompt.
// Uses base instructions without graph/strategy sections. Callers with a
// WorkspaceConfig should use host.BootstrapHost() which delegates to
// BootstrapWithConfig when available.
func (h Host) Bootstrap(task, baseBranch, contract string) host.BootstrapResult {
	body := contract + agentInstructions(task, baseBranch, agentpolicy.InstructionOpts{})
	return host.BootstrapResult{
		FileName:      "CLAUDE.md",
		FileBody:      body,
		InitialPrompt: "Read CLAUDE.md and execute the task autonomously.",
	}
}

// BootstrapWithConfig returns bootstrap result with conditional instructions
// based on workspace tool availability (graph, strategy).
func (h Host) BootstrapWithConfig(task, baseBranch, contract string, cfg host.WorkspaceConfig) host.BootstrapResult {
	opts := agentpolicy.OptsFromConfig(cfg)
	body := contract + agentInstructions(task, baseBranch, opts)
	return host.BootstrapResult{
		FileName:      "CLAUDE.md",
		FileBody:      body,
		InitialPrompt: "Read CLAUDE.md and execute the task autonomously.",
	}
}

// Verify claude.Host satisfies ConfigurableBootstrap at compile time.
var _ host.ConfigurableBootstrap = Host{}

// BuildBatchCommand returns the CommandSpec for Claude CLI batch mode.
func (Host) BuildBatchCommand(prompt, model, resumeToken string) host.CommandSpec {
	return BuildBatchCommand(prompt, model, resumeToken)
}

// ParseBatchOutput parses Claude CLI JSON output.
func (Host) ParseBatchOutput(stdout []byte, waitErr error) (host.Result, error) {
	return ParseBatchOutput(stdout, waitErr)
}

// StartStream launches a Claude CLI streaming session.
func (Host) StartStream(workdir, model, logPath string) (host.StreamSession, error) {
	return StartStream(workdir, model, logPath)
}

// Capabilities returns what Claude CLI supports.
func (Host) Capabilities() host.Capabilities {
	return host.Capabilities{
		Stream:           true,
		AgentMCP:         true,
		ToolPermissions:  true,
		ResumeToken:      true,
		StructuredEvents: true,
	}
}
