package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// ToolInfo is normalized tool metadata exposed to consumers outside the pool.
// InputSchema is the raw JSON Schema — enough to reconstruct an mcp.Tool
// for proxy registration without leaking mcp-go types.
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// DiscoverTools returns normalized tool metadata for a backend MCP server.
// Connects lazily on first call (same as CallTool). Returns cached data
// from the ListTools call made during connection.
func (p *Pool) DiscoverTools(ctx context.Context, server string) ([]ToolInfo, error) {
	entry, err := p.getOrConnect(ctx, server)
	if err != nil {
		return nil, fmt.Errorf("connecting to %q: %w", server, err)
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	result := make([]ToolInfo, 0, len(entry.tools))
	for _, t := range entry.tools {
		schema, _ := json.Marshal(t.InputSchema)
		result = append(result, ToolInfo{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return result, nil
}

// CallToolRaw forwards a tool call to a backend server and returns the full
// MCP CallToolResult without normalization. Used by the gateway proxy —
// preserves content blocks, isError, and metadata as-is.
//
// This is the only path where mcp.CallToolResult crosses the pool boundary.
// The fetcher path (CallTool) normalizes to ToolResult instead.
func (p *Pool) CallToolRaw(ctx context.Context, server, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	entry, err := p.getOrConnect(ctx, server)
	if err != nil {
		return nil, fmt.Errorf("connecting to %q: %w", server, err)
	}

	entry.mu.Lock()
	_, ok := entry.tools[tool]
	c := entry.client
	entry.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("tool %q not found on server %q", tool, server)
	}

	callReq := mcp.CallToolRequest{}
	callReq.Params.Name = tool
	callReq.Params.Arguments = args
	result, err := c.CallTool(ctx, callReq)
	if err != nil {
		entry.mu.Lock()
		entry.state = ConnFailed
		entry.lastErr = err
		entry.mu.Unlock()
		return nil, fmt.Errorf("calling %q on %q: %w", tool, server, err)
	}

	return result, nil
}

// ServerNames returns the names of all configured MCP servers.
func (p *Pool) ServerNames() []string {
	names := make([]string, 0, len(p.config.Servers))
	for name := range p.config.Servers {
		names = append(names, name)
	}
	return names
}
