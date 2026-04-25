// Package host defines the interface boundary between fracta's orchestrator
// and host-specific agent delivery mechanisms (Claude CLI, future hosts).
//
// The Host interface covers the full agent lifecycle:
//   - Workspace: writing host-specific config files
//   - Bootstrap: creating the initial task file and prompt
//   - Batch: building the command to run and parsing its output
//   - Streaming: launching an interactive session
//
// Process execution is NOT part of this interface — that's runtime.Backend's job.
// Host owns protocol and format; Backend owns process lifecycle.
package host

import (
	"errors"

	"github.com/darkquasar/fracta/internal/auth/credentials"
	"github.com/darkquasar/fracta/internal/config"
)

// Host is the boundary between fracta's orchestrator and a specific LLM CLI.
// Today there is one implementation: internal/host/claude.
type Host interface {
	// WriteWorkspace writes host-specific config files (.mcp.json, settings, etc.)
	// into the agent workspace at workdir.
	WriteWorkspace(workdir string, allowedTools []string, cfg WorkspaceConfig) error

	// Bootstrap returns the host-specific task file and initial prompt for an agent.
	// FileName may be empty — meaning no file artifact is needed.
	Bootstrap(task, baseBranch, contract string) BootstrapResult

	// BuildBatchCommand returns the command spec for running a single-shot batch prompt.
	// The orchestrator passes the result to runtime.Backend.Spawn().
	BuildBatchCommand(prompt, model, resumeToken string) CommandSpec

	// ParseBatchOutput parses the stdout of a completed batch process into a Result.
	// waitErr is the error from cmd.Wait() — may be non-nil even with parseable output.
	ParseBatchOutput(stdout []byte, waitErr error) (Result, error)

	// StartStream launches an interactive streaming session.
	// Returns ErrStreamNotSupported if this host doesn't support streaming.
	StartStream(workdir, model, logPath string) (StreamSession, error)

	// Capabilities returns what this host supports.
	// Informational in v1 — no enforcement. Future: spawn handler
	// checks capabilities before dispatch.
	Capabilities() Capabilities
}

// Capabilities describes what a host implementation supports.
// Informational in v1 — callers may inspect but enforcement is future work.
type Capabilities struct {
	Stream           bool // can StartStream (interactive sessions)
	AgentMCP         bool // supports agent-mode MCP server
	ToolPermissions  bool // supports permission allow-lists
	ResumeToken      bool // supports session resume via token
	StructuredEvents bool // emits parseable structured events (JSONL, nd-JSON, etc.)
}

// CommandSpec is the host-specific command to execute. Includes the binary
// name so the orchestrator never hardcodes a CLI name.
type CommandSpec struct {
	Command string   // "claude", "llm", "ollama", etc.
	Args    []string
	Env     []string // additional env vars for this invocation
}

// BootstrapResult contains the host-specific task file and initial prompt.
type BootstrapResult struct {
	FileName      string // "CLAUDE.md", "TASK.md", etc. Empty = no file artifact.
	FileBody      string // Contract content + host-specific instructions.
	InitialPrompt string // "Read CLAUDE.md and execute the task autonomously."
}

// Result is the host-neutral outcome of a batch execution or stream turn.
type Result struct {
	ResumeToken string
	Output      string
	IsError     bool
}

// StreamSession is an interactive streaming connection to a host CLI.
// The orchestrator stores this in the ProcessRegistry and uses it for
// Say/SayStream operations.
type StreamSession interface {
	// Send sends a message and blocks until the host responds.
	Send(message string) (Result, error)

	// ResumeToken returns the session resume token (set after first Send).
	ResumeToken() string

	// RecentOutput returns the last maxBytes of semantic output.
	RecentOutput(maxBytes int) string

	// Done returns a channel that closes when the session exits.
	Done() <-chan struct{}

	// Close terminates the session and cleans up.
	Close() error
}

// ErrStreamNotSupported is returned by StartStream when the host doesn't support streaming.
var ErrStreamNotSupported = errors.New("host does not support streaming")

// LineObservable is an optional interface that StreamSession implementations
// may satisfy to allow external observation of raw protocol lines. The
// orchestrator uses this to feed lines to the HostStreamAdapter for event
// emission without coupling the host package to the events system.
type LineObservable interface {
	// SetLineObserver registers a callback invoked for each raw protocol line
	// read from the host process. Must be called before the first Send().
	SetLineObserver(func(line []byte))
}

// TurnSteerer is an optional interface implemented by StreamSessions that
// support mid-execution redirection (Codex-specific, no Claude equivalent).
type TurnSteerer interface {
	Steer(newDirection string) error
}

// EventObservable is an optional interface that StreamSession implementations
// may satisfy to allow external observation of structured events. This is the
// non-stdio counterpart to LineObservable: SSE events and WebSocket messages
// are not stdout lines, so forcing them through LineObservable would create an
// artificial serialize→deserialize round-trip. The orchestrator adds a type
// assertion for this interface alongside the existing LineObservable check.
type EventObservable interface {
	// SetEventObserver registers a callback invoked for each raw structured
	// event received from the host process (e.g., SSE event bytes).
	// Must be called before the first Send().
	SetEventObserver(func(event []byte))
}

// ConfigurableBootstrap is an optional interface that Host implementations
// may satisfy to accept workspace configuration during bootstrap. When a Host
// implements this, callers should prefer BootstrapWithConfig over Bootstrap so
// the host can tailor agent instructions to the available tools (e.g. graph,
// strategy).
type ConfigurableBootstrap interface {
	BootstrapWithConfig(task, baseBranch, contract string, cfg WorkspaceConfig) BootstrapResult
}

// BootstrapHost calls BootstrapWithConfig if the host supports it, otherwise
// falls back to the plain Bootstrap method. Callers should use this instead of
// calling h.Bootstrap directly when they have a WorkspaceConfig available.
func BootstrapHost(h Host, task, baseBranch, contract string, cfg WorkspaceConfig) BootstrapResult {
	if cb, ok := h.(ConfigurableBootstrap); ok {
		return cb.BootstrapWithConfig(task, baseBranch, contract, cfg)
	}
	return h.Bootstrap(task, baseBranch, contract)
}

// WorkspaceConfig holds the information needed to write host-specific
// settings into an agent workspace.
type WorkspaceConfig struct {
	AgentMCP    bool
	Servers     config.MCPServersConfig
	Backend     string
	ProjectRoot string
	ConfigPath  string // --config flag for agent-mode MCP server
	GraphAddr   string // --graph-addr flag
	StrategyDir string // --strategy-dir flag

	// CredentialOutput is the materialized credential output for this spawn
	// (nil = no auth config). Host adapters project this into host-specific
	// formats (e.g., Claude's user-settings.json) using the embedded Plan's
	// Binding and Resolver context.
	CredentialOutput *credentials.CredentialOutput

	// GatewayURL is the centralized MCP gateway URL. When set, host adapters
	// emit a single remote MCP endpoint instead of per-backend subprocess entries.
	GatewayURL string

	// RuntimeWorkDir is the agent's working directory as seen by the runtime process.
	// Local: the real worktree path. K8s: {PVCMountPath}/agents/{task}.
	// Used by OpenCode to generate workspace-aware permission rules.
	RuntimeWorkDir string

	// Objective context (spec-16a). Set when mission belongs to an objective.
	AgentTask   string // agent's task name (for agent identity)
	ObjectiveID string // objective this agent serves
	MissionID   int64  // mission this agent is executing
}
