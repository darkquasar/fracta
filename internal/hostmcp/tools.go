package hostmcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerTools wires all host-facing lifecycle tools.
func (s *Server) registerTools() {
	s.mcp.AddTool(mcp.NewTool("fracta_spawn",
		mcp.WithDescription("Spawn a new agent. Creates a workspace and starts execution."),
		mcp.WithString("task",
			mcp.Description("Unique task name (alphanumeric, hyphens, underscores)."),
			mcp.Required(),
		),
		mcp.WithString("contract",
			mcp.Description("Task instructions: inline text or path to a file."),
		),
		mcp.WithString("base",
			mcp.Description("Base branch for the workspace (defaults to config default)."),
		),
		mcp.WithString("model",
			mcp.Description("Model to use (overrides config default and tier)."),
		),
		mcp.WithString("tier",
			mcp.Description("Model tier: maps to a model ID via config model_tiers."),
			mcp.Enum("heavy", "medium", "light"),
		),
		mcp.WithString("mode",
			mcp.Description("'batch' (default, runs to completion) or 'stream' (stays alive)."),
		),
		mcp.WithString("dispatch",
			mcp.Description("'direct' (default) or 'queued' (submit to mission queue)."),
			mcp.Enum("direct", "queued"),
		),
		mcp.WithString("runtime",
			mcp.Description("Runtime implementation to use (e.g. 'claude')."),
		),
		mcp.WithString("host_type",
			mcp.Description("Deprecated: use 'runtime' instead."),
		),
		mcp.WithString("objective_id",
			mcp.Description("Link this spawn as the root mission of an objective."),
		),
	), s.handleSpawn)

	s.mcp.AddTool(mcp.NewTool("fracta_list",
		mcp.WithDescription("List all agents with status, mode, branch, intent, and unread message count."),
	), s.handleList)

	s.mcp.AddTool(mcp.NewTool("fracta_peek",
		mcp.WithDescription("Read an agent's recent output."),
		mcp.WithString("name",
			mcp.Description("Agent task name"),
			mcp.Required(),
		),
		mcp.WithString("mode",
			mcp.Description("'raw' for protocol events; omit for semantic output."),
		),
	), s.handlePeek)

	s.mcp.AddTool(mcp.NewTool("fracta_say",
		mcp.WithDescription("Send a follow-up message to an agent session."),
		mcp.WithString("name",
			mcp.Description("Agent task name"),
			mcp.Required(),
		),
		mcp.WithString("message",
			mcp.Description("Message to inject into the agent's session."),
			mcp.Required(),
		),
	), s.handleSay)

	s.mcp.AddTool(mcp.NewTool("fracta_kill",
		mcp.WithDescription("Terminate an agent: removes workspace, clears state. Irreversible."),
		mcp.WithString("name",
			mcp.Description("Agent task name"),
			mcp.Required(),
		),
		mcp.WithBoolean("keep_files",
			mcp.Description("Keep workspace files instead of deleting them (default: false)."),
		),
	), s.handleKill)

	s.mcp.AddTool(mcp.NewTool("fracta_logs",
		mcp.WithDescription("Fetch recent logs for an agent."),
		mcp.WithString("task",
			mcp.Description("Agent task name"),
			mcp.Required(),
		),
		mcp.WithNumber("lines",
			mcp.Description("Number of log lines to return (default: 100, 0 = all)."),
		),
	), s.handleLogs)

	s.mcp.AddTool(mcp.NewTool("fracta_get_agent",
		mcp.WithDescription("Get detailed status for a specific agent."),
		mcp.WithString("name",
			mcp.Description("Agent task name"),
			mcp.Required(),
		),
	), s.handleGetAgent)

	s.mcp.AddTool(mcp.NewTool("fracta_get_mission",
		mcp.WithDescription("Get mission details for an agent (queue status, objective link)."),
		mcp.WithString("name",
			mcp.Description("Agent task name"),
			mcp.Required(),
		),
	), s.handleGetMission)

	s.mcp.AddTool(mcp.NewTool("fracta_create_objective",
		mcp.WithDescription("Create a new objective for autonomous mission orchestration."),
		mcp.WithString("description",
			mcp.Description("Objective description"),
			mcp.Required(),
		),
	), s.handleCreateObjective)

	s.mcp.AddTool(mcp.NewTool("fracta_list_objectives",
		mcp.WithDescription("List objectives, optionally filtered by status."),
		mcp.WithString("status",
			mcp.Description("Filter by status: 'open', 'answered', 'disproven' (default: all)"),
		),
	), s.handleListObjectives)

	s.mcp.AddTool(mcp.NewTool("fracta_get_objective",
		mcp.WithDescription("Get details for a specific objective."),
		mcp.WithString("id",
			mcp.Description("Objective ID"),
			mcp.Required(),
		),
	), s.handleGetObjective)
}

func (s *Server) handleSpawn(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	task, err := request.RequireString("task")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter: task"), nil
	}

	// Accept both "runtime" (preferred) and "host_type" (deprecated).
	runtimeType := request.GetString("runtime", "")
	if runtimeType == "" {
		runtimeType = request.GetString("host_type", "")
	}
	resp, err := s.client.Spawn(ctx, cpapi.SpawnRequest{
		Task:        task,
		Contract:    request.GetString("contract", ""),
		BaseBranch:  request.GetString("base", ""),
		Model:       request.GetString("model", ""),
		Tier:        request.GetString("tier", ""),
		RuntimeType: runtimeType,
		Mode:        request.GetString("mode", ""),
		Dispatch:    request.GetString("dispatch", ""),
		ObjectiveID: request.GetString("objective_id", ""),
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("spawn failed: %v", err)), nil
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshalling response: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (s *Server) handleList(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resp, err := s.client.ListAgents(ctx, cpapi.ListAgentsRequest{})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list failed: %v", err)), nil
	}

	data, err := json.Marshal(resp.Agents)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshalling response: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (s *Server) handlePeek(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter: name"), nil
	}

	resp, err := s.client.Peek(ctx, cpapi.PeekRequest{
		Name: name,
		Mode: request.GetString("mode", ""),
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("peek failed: %v", err)), nil
	}

	return mcp.NewToolResultText(resp.Output), nil
}

func (s *Server) handleSay(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter: name"), nil
	}

	message, err := request.RequireString("message")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter: message"), nil
	}

	resp, err := s.client.Say(ctx, cpapi.SayRequest{
		Name:    name,
		Message: message,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("say failed: %v", err)), nil
	}

	return mcp.NewToolResultText(resp.Message), nil
}

func (s *Server) handleKill(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter: name"), nil
	}

	resp, err := s.client.Kill(ctx, cpapi.KillRequest{
		Name:      name,
		KeepFiles: request.GetBool("keep_files", false),
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("kill failed: %v", err)), nil
	}

	return mcp.NewToolResultText(resp.Message), nil
}

func (s *Server) handleGetAgent(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter: name"), nil
	}

	resp, err := s.client.GetAgent(ctx, cpapi.GetAgentRequest{Name: name})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("get agent failed: %v", err)), nil
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshalling response: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (s *Server) handleGetMission(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter: name"), nil
	}

	resp, err := s.client.GetMission(ctx, cpapi.GetMissionRequest{Name: name})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("get mission failed: %v", err)), nil
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshalling response: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (s *Server) handleCreateObjective(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	description, err := request.RequireString("description")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter: description"), nil
	}

	resp, err := s.client.CreateObjective(ctx, cpapi.CreateObjectiveRequest{
		Description: description,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create objective failed: %v", err)), nil
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshalling response: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (s *Server) handleListObjectives(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resp, err := s.client.ListObjectives(ctx, cpapi.ListObjectivesRequest{
		Status: request.GetString("status", ""),
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list objectives failed: %v", err)), nil
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshalling response: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (s *Server) handleGetObjective(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := request.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter: id"), nil
	}

	resp, err := s.client.GetObjective(ctx, cpapi.GetObjectiveRequest{ID: id})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("get objective failed: %v", err)), nil
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshalling response: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (s *Server) handleLogs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	task, err := request.RequireString("task")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter: task"), nil
	}

	lines := request.GetInt("lines", 100)

	resp, err := s.client.GetLogs(ctx, cpapi.GetLogsRequest{
		Task:  task,
		Lines: lines,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("logs failed: %v", err)), nil
	}

	if resp.Output == "" {
		return mcp.NewToolResultText("No logs available."), nil
	}

	return mcp.NewToolResultText(resp.Output), nil
}
