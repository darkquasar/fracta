package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerTools wires the full Server tool surface:
//   - Agent tools (shared with AgentServer/GatewayServer)
//   - Admin tools (Server-only: init, spawn, say, kill, merge)
//   - Enhanced list/peek handlers that use the ProcessRegistry
func (s *Server) registerTools() {
	// Shared agent tools: list, peek, send, inbox, set_intent
	if s.store != nil && s.mailbox != nil {
		registerAgentTools(s.mcp, s.store, s.mailbox)
	}

	// Override fracta_list and fracta_peek with Server-specific versions
	// that include ProcessRegistry access and extra fields.
	// AddTool replaces existing entries by name.
	s.mcp.AddTool(mcp.NewTool("fracta_list",
		mcp.WithDescription("List all agents with name, status, mode, branch, intent, and unread message count. Use to monitor progress and decide when to merge or follow up."),
	), s.handleList)

	s.mcp.AddTool(mcp.NewTool("fracta_peek",
		mcp.WithDescription("Read an agent's recent output. Returns semantic output by default. Use mode='raw' for protocol-level debugging."),
		mcp.WithString("name",
			mcp.Description("Agent task name"),
			mcp.Required(),
		),
		mcp.WithString("mode",
			mcp.Description("'raw' for raw protocol events; omit for semantic output (text, tool names)"),
		),
	), s.handlePeek)

	// Admin-only tools
	s.registerAdminTools()

	// Observability: admin-only log access
	s.mcp.AddTool(mcp.NewTool("fracta_logs",
		mcp.WithDescription("Fetch recent pod/process logs for an agent. In K8s mode fetches container logs; locally reads the agent log file. Admin tool — not available to agents."),
		mcp.WithString("task",
			mcp.Description("Agent task name"),
			mcp.Required(),
		),
		mcp.WithNumber("lines",
			mcp.Description("Number of log lines to return (default: 100, 0 = all)"),
		),
	), s.handleLogs)
}

// handleList is the Server-specific list handler that includes ObjectiveID,
// Session (ResumeToken), and uses the full orchestrator with requireRoot.
func (s *Server) handleList(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireRoot(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if s.cpClient == nil {
		return mcp.NewToolResultError("control plane client not configured"), nil
	}

	resp, err := s.cpClient.ListAgents(ctx, cpapi.ListAgentsRequest{})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("listing agents: %v", err)), nil
	}
	data, err := json.Marshal(resp.Agents)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshalling response: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// handlePeek is the Server-specific peek handler that checks the
// ProcessRegistry for streaming agent output before falling back to logs.
func (s *Server) handlePeek(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireRoot(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter: name"), nil
	}

	mode := request.GetString("mode", "")

	if s.cpClient == nil {
		return mcp.NewToolResultError("control plane client not configured"), nil
	}

	resp, err := s.cpClient.Peek(ctx, cpapi.PeekRequest{Name: name, Mode: mode})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("peek failed: %v", err)), nil
	}
	return mcp.NewToolResultText(resp.Output), nil
}

func (s *Server) handleLogs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireRoot(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	task, err := request.RequireString("task")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter: task"), nil
	}

	lines := request.GetInt("lines", 100)

	if s.cpClient == nil {
		return mcp.NewToolResultError("control plane client not configured"), nil
	}

	resp, err := s.cpClient.GetLogs(ctx, cpapi.GetLogsRequest{Task: task, Lines: lines})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("fetching logs: %v", err)), nil
	}
	if resp.Output == "" {
		return mcp.NewToolResultText("No logs available."), nil
	}
	return mcp.NewToolResultText(resp.Output), nil
}
