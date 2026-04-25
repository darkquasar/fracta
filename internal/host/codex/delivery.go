package codex

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/darkquasar/fracta/internal/assets"
	"github.com/darkquasar/fracta/internal/host"
)

//go:embed instructions/*
var instructionFS embed.FS

var instructions = assets.New(instructionFS, "instructions")

// Host implements the host.Host interface for OpenAI's Codex CLI.
// Stage 2: Full parity via app-server (Stream=true, AgentMCP=true,
// ToolPermissions=true, ResumeToken=true, StructuredEvents=true).
type Host struct{}

var _ host.Host = Host{}

// WriteWorkspace writes Codex workspace config files into the agent workspace.
// When AgentMCP and GatewayURL are set, writes .codex/config.toml with the
// MCP gateway endpoint. Codex only supports HTTP MCP transports (no stdio),
// so when GatewayURL is empty the MCP section is omitted.
func (Host) WriteWorkspace(workdir string, allowedTools []string, cfg host.WorkspaceConfig) error {
	if !cfg.AgentMCP || cfg.GatewayURL == "" {
		return nil
	}

	codexDir := filepath.Join(workdir, ".codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		return fmt.Errorf("creating .codex dir: %w", err)
	}

	url := cfg.GatewayURL
	if cfg.AgentTask != "" {
		url = cfg.GatewayURL + "/agents/" + cfg.AgentTask + "/mcp"
	}

	var b strings.Builder
	b.WriteString("[mcp_servers.fracta]\n")
	b.WriteString(fmt.Sprintf("url = %q\n", url))
	b.WriteString("bearer_token_env_var = \"FRACTA_GATEWAY_TOKEN\"\n")

	configPath := filepath.Join(codexDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("writing .codex/config.toml: %w", err)
	}

	return nil
}

// Bootstrap returns the Codex-specific task file and initial prompt.
// Uses AGENTS.md — Codex's documented convention for agent instructions.
func (Host) Bootstrap(task, baseBranch, contract string) host.BootstrapResult {
	body := contract + agentInstructions(task, baseBranch)
	return host.BootstrapResult{
		FileName:      "AGENTS.md",
		FileBody:      body,
		InitialPrompt: fmt.Sprintf("Read AGENTS.md and execute the task described in it autonomously. You are agent %q.", task),
	}
}

// BuildBatchCommand returns the CommandSpec for Codex CLI batch mode.
func (Host) BuildBatchCommand(prompt, model, resumeToken string) host.CommandSpec {
	return BuildBatchCommand(prompt, model, resumeToken)
}

// ParseBatchOutput parses Codex CLI JSONL output.
func (Host) ParseBatchOutput(stdout []byte, waitErr error) (host.Result, error) {
	return ParseBatchOutput(stdout, waitErr)
}

// StartStream launches a Codex app-server subprocess in streaming mode.
// The app-server provides full JSON-RPC streaming via stdio.
func (Host) StartStream(workdir, model, logPath string) (host.StreamSession, error) {
	return NewAppServerSession(workdir, model, logPath)
}

// Capabilities returns what Codex CLI supports (Phase 2 target via app-server).
func (Host) Capabilities() host.Capabilities {
	return host.Capabilities{
		Stream:           true,
		AgentMCP:         true,
		ToolPermissions:  true,
		ResumeToken:      true,
		StructuredEvents: true,
	}
}

// agentInstructions returns the Codex-specific AGENTS.md section for spawned agents.
func agentInstructions(task, baseBranch string) string {
	return instructions.MustRender("base-agent.md.tmpl", map[string]string{
		"Task":       task,
		"BaseBranch": baseBranch,
	})
}

// PermissionBaseline returns Codex-specific permission patterns.
// Codex manages its own sandbox policy via --full-auto, so this is
// informational only — used by WriteWorkspace if needed in the future.
func (Host) PermissionBaseline() []string {
	return []string{}
}

// ExePath returns the resolved path to the codex binary, or "codex" if not found.
func ExePath() string {
	if p, err := os.Executable(); err == nil {
		dir := filepath.Dir(p)
		codexPath := filepath.Join(dir, "codex")
		if _, err := os.Stat(codexPath); err == nil {
			return codexPath
		}
	}
	return "codex"
}
