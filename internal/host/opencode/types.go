// Package opencode implements the host.Host interface for the OpenCode CLI.
// Phase 1: Batch mode (Stream=false, AgentMCP=true, ToolPermissions=true, ResumeToken=true).
package opencode

import "encoding/json"

// ndEvent represents a single nd-JSON line from `opencode run --format json`.
type ndEvent struct {
	Type      string          `json:"type"`
	Timestamp int64           `json:"timestamp,omitempty"`
	SessionID string          `json:"sessionID,omitempty"`
	Part      json.RawMessage `json:"part,omitempty"`
	Error     *ndError        `json:"error,omitempty"`
}

// ndError is the error payload within an nd-JSON event.
type ndError struct {
	Name string       `json:"name"`
	Data ndErrorData  `json:"data"`
}

// ndErrorData contains the error message details.
type ndErrorData struct {
	Message string `json:"message"`
}

// ndPart represents a part payload within an nd-JSON event.
// Used for text and tool_use events.
type ndPart struct {
	Type string `json:"type"` // "text", "tool", "step-start", "step-finish", "reasoning"
	Text string `json:"text,omitempty"`
}

// openCodeConfig is the structure written to opencode.json.
type openCodeConfig struct {
	MCP        map[string]mcpEntry    `json:"mcp,omitempty"`
	Permission map[string]interface{} `json:"permission"`
}

// mcpEntry is a single MCP server entry in opencode.json.
type mcpEntry struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}
