package opencode

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/darkquasar/fracta/internal/agentpolicy"
	"github.com/darkquasar/fracta/internal/assets"
	"github.com/darkquasar/fracta/internal/host"
)

//go:embed instructions/*
var instructionFS embed.FS

var instructions = assets.New(instructionFS, "instructions")

// Host implements the host.Host interface for the OpenCode CLI.
// Phase 1: Batch mode with MCP, permissions, and resume token support.
type Host struct{}

var _ host.Host = Host{}

// WriteWorkspace writes opencode.json into the agent workspace.
// MCP section is written only when cfg.AgentMCP && cfg.GatewayURL != "".
// Permission section is always written with "task":"deny" default (spec Risk 2 mitigation).
func (Host) WriteWorkspace(workdir string, allowedTools []string, cfg host.WorkspaceConfig) error {
	oc := openCodeConfig{
		Permission: buildPermissions(allowedTools, cfg.RuntimeWorkDir),
	}

	// Only write MCP section when gateway is configured.
	// OpenCode's "type":"remote" only supports HTTP MCP, not stdio.
	// Phase 1: gateway endpoint only (spec §4 Rule 4).
	if cfg.AgentMCP && cfg.GatewayURL != "" {
		url := cfg.GatewayURL
		if cfg.AgentTask != "" {
			url = cfg.GatewayURL + "/agents/" + cfg.AgentTask + "/mcp"
		}
		oc.MCP = map[string]mcpEntry{
			"fracta": {
				Type: "remote",
				URL:  url,
				Headers: map[string]string{
					"Authorization": "Bearer {env:FRACTA_GATEWAY_TOKEN}",
				},
			},
		}

		// Expand fracta platform tools into OpenCode permission keys.
		// OpenCode synthesizes permission keys as "{server}_{tool}".
		// The MCP server is named "fracta", so keys become fracta_fracta_list, fracta_graph_query, etc.
		// This covers fracta platform tools, not arbitrary external backend tools.
		addMCPToolPermissions(oc.Permission, cfg.ObjectiveID)
	}

	data, err := json.MarshalIndent(oc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling opencode.json: %w", err)
	}

	path := filepath.Join(workdir, "opencode.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing opencode.json: %w", err)
	}
	return nil
}

// Bootstrap returns the OpenCode-specific task file and initial prompt.
// Uses AGENTS.md — OpenCode's convention for agent instructions.
func (Host) Bootstrap(task, baseBranch, contract string) host.BootstrapResult {
	body := contract + agentInstructions(task, baseBranch)
	return host.BootstrapResult{
		FileName:      "AGENTS.md",
		FileBody:      body,
		InitialPrompt: fmt.Sprintf("Read AGENTS.md and execute the task described in it autonomously. You are agent %q.", task),
	}
}

// BuildBatchCommand returns the CommandSpec for OpenCode CLI batch mode.
func (Host) BuildBatchCommand(prompt, model, resumeToken string) host.CommandSpec {
	return BuildBatchCommand(prompt, model, resumeToken)
}

// ParseBatchOutput parses OpenCode nd-JSON output.
func (Host) ParseBatchOutput(stdout []byte, waitErr error) (host.Result, error) {
	return ParseBatchOutput(stdout, waitErr)
}

// StartStream launches an opencode serve subprocess and returns a ServeSession.
// Permissions come from two layers: opencode.json (written by WriteWorkspace) sets
// the base policy, and PermissionRules passed here reinforce critical overrides
// at the session API level (belt-and-suspenders for task:deny).
func (Host) StartStream(workdir, model, logPath string) (host.StreamSession, error) {
	return StartServeSession(workdir, model, logPath, StreamPermissionRules(workdir, ""))
}

// StreamPermissionRules returns the session-level permission rules for OpenCode
// stream sessions. Both local and K8s stream paths use this to stay consistent.
// Includes MCP tool allows so stream session-level overrides don't mask file-layer config.
func StreamPermissionRules(workdir, objectiveID string) []PermissionRule {
	rules := []PermissionRule{
		{Permission: "task", Action: "deny", Pattern: "*"},
	}
	if workdir != "" {
		rules = append(rules, PermissionRule{
			Permission: "external_directory", Action: "allow", Pattern: workdir,
		})
		rules = append(rules, PermissionRule{
			Permission: "external_directory", Action: "allow", Pattern: workdir + "/*",
		})
	}
	for _, t := range agentpolicy.ExpandFractaTools(mcpPermissionPrefix, objectiveID, nil) {
		rules = append(rules, PermissionRule{
			Permission: t, Action: "allow", Pattern: "*",
		})
	}
	return rules
}

// mcpPermissionPrefix is the OpenCode permission key prefix for fracta gateway tools.
// OpenCode synthesizes permission keys as "{server}_{tool}" where server is the MCP
// server name from opencode.json. Our server is named "fracta".
const mcpPermissionPrefix = "fracta_"

// addMCPToolPermissions expands fracta platform tools into concrete OpenCode permission
// keys and adds them to the permission map. Covers coordination, graph, strategy,
// and discovery tools. Does not cover arbitrary external backend tools proxied by
// the gateway.
func addMCPToolPermissions(perms map[string]interface{}, objectiveID string) {
	for _, t := range agentpolicy.ExpandFractaTools(mcpPermissionPrefix, objectiveID, nil) {
		perms[t] = "allow"
	}
}

// Capabilities returns what OpenCode CLI supports.
// Phase 2: streaming via opencode serve + HTTP API.
func (Host) Capabilities() host.Capabilities {
	return host.Capabilities{
		Stream:           true,
		AgentMCP:         true,
		ToolPermissions:  true,
		ResumeToken:      true,
		StructuredEvents: true,
	}
}

// agentInstructions returns the OpenCode-specific AGENTS.md section for spawned agents.
func agentInstructions(task, baseBranch string) string {
	return instructions.MustRender("base-agent.md.tmpl", map[string]string{
		"Task":       task,
		"BaseBranch": baseBranch,
	})
}

// buildPermissions maps allowedTools to OpenCode's hierarchical permission DSL.
// Always includes "task":"deny" to mitigate subagent overuse (spec §9 Risk 2).
func buildPermissions(allowedTools []string, workspacePath string) map[string]interface{} {
	perms := map[string]interface{}{
		"*":         "ask",
		"read":      "allow",
		"edit":      "allow",
		"todowrite": "allow",
		"bash": map[string]interface{}{
			"*":     "ask",
			"git *": "allow",
		},
		"task": "deny",
	}

	// If specific tools are allowed, map them to permission rules.
	for _, tool := range allowedTools {
		switch tool {
		case "Bash", "Bash(*)":
			perms["bash"] = "allow"
		case "Read", "Read(*)":
			perms["read"] = "allow"
		case "Edit", "Edit(*)":
			perms["edit"] = "allow"
		case "WebFetch", "WebFetch(*)":
			perms["webfetch"] = "allow"
		case "WebSearch", "WebSearch(*)":
			perms["websearch"] = "allow"
		}
	}

	if workspacePath != "" {
		perms["external_directory"] = map[string]interface{}{
			workspacePath:        "allow",
			workspacePath + "/*": "allow",
		}
	}

	return perms
}
