package claude

import "encoding/json"

// Response is the JSON output from the Claude CLI in batch mode.
// This type is internal to the claude host adapter — the orchestrator
// uses host.Result instead.
type Response struct {
	SessionID string `json:"session_id"`
	Result    string `json:"result"`
	IsError   bool   `json:"is_error"`
}

// StreamEvent represents a single event from Claude's stream-json protocol.
// Internal to the claude host adapter — parsed by the stream session.
type StreamEvent struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	SessionID string          `json:"session_id"`
	Result    string          `json:"result"`
	NumTurns  int             `json:"num_turns"`
	IsError   bool            `json:"is_error"`
	Message   json.RawMessage `json:"message"`
}
