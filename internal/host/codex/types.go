// Package codex implements the host.Host interface for OpenAI's Codex CLI.
// Stage 1.5: Batch+Resume+MCP+K8s host (Stream=false, AgentMCP=true, ResumeToken=true, StructuredEvents=true).
package codex

// Event represents a single JSONL line from `codex exec --json`.
type Event struct {
	Type     string      `json:"type"`
	ThreadID string      `json:"thread_id,omitempty"` // present on thread.started
	Item     *Item       `json:"item,omitempty"`      // present on item.completed/item.started
	Usage    *Usage      `json:"usage,omitempty"`      // present on turn.completed
	Error    *EventError `json:"error,omitempty"`      // present on error events
}

// EventError represents an error event from Codex.
type EventError struct {
	Message   string `json:"message"`
	WillRetry bool   `json:"willRetry,omitempty"`
}

// Item represents an item within a Codex execution event.
type Item struct {
	ID               string       `json:"id"`
	Type             string       `json:"type"` // "agent_message", "file_change", "command_execution"
	Text             string       `json:"text,omitempty"`
	Command          string       `json:"command,omitempty"`
	AggregatedOutput string       `json:"aggregated_output,omitempty"`
	ExitCode         *int         `json:"exit_code,omitempty"`
	Status           string       `json:"status,omitempty"`
	Changes          []FileChange `json:"changes,omitempty"`
}

// FileChange represents a file operation from a file_change item.
type FileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"` // "add", "modify", "delete"
}

// Usage contains token usage statistics from turn.completed.
type Usage struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
}
