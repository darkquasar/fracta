package mcpclient

import "context"

// ToolResult is the normalized output of a tool call.
// The Pool is responsible for extracting text content and detecting errors
// from the raw CallToolResult before returning this.
type ToolResult struct {
	Text    string // first JSON-bearing TextContent block from the response
	IsError bool   // true if the MCP tool reported an error
}

// ToolCaller is the consumption boundary for MCP tool calls.
type ToolCaller interface {
	// CallTool calls a named tool on a named server. The pool handles
	// connection lifecycle, content extraction, and error detection.
	// Returns ToolResult with extracted text, or error for transport/connection failures.
	CallTool(ctx context.Context, server, tool string, args map[string]any) (*ToolResult, error)
}
