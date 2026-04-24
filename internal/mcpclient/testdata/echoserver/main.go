// echoserver is a minimal MCP server for integration testing.
// It exposes one tool "echo" that returns its input as JSON.
// Launched as a stdio subprocess by pool_integration_test.go.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	s := server.NewMCPServer("echo-test-server", "0.1.0",
		server.WithToolCapabilities(false),
	)

	s.AddTool(mcp.NewTool("echo",
		mcp.WithDescription("Returns the input as JSON"),
		mcp.WithString("message", mcp.Description("Message to echo"), mcp.Required()),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		msg := req.GetString("message", "")
		// Return a JSON array of objects (mimics real MCP tool responses)
		items := []map[string]any{
			{"id": "1", "text": msg},
			{"id": "2", "text": msg + " (copy)"},
		}
		data, _ := json.Marshal(items)
		return mcp.NewToolResultText(string(data)), nil
	})

	s.AddTool(mcp.NewTool("fail",
		mcp.WithDescription("Always returns an error"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, fmt.Errorf("intentional failure")
	})

	if err := server.ServeStdio(s); err != nil {
		panic(err)
	}
}
