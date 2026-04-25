package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerObjectiveTools adds chessmaster-facing objective management tools.
// Called when cpClient is set on the Server (thin-client or local mode).
func (s *Server) registerObjectiveTools() {
	s.mcp.AddTool(mcp.NewTool("fracta_create_objective",
		mcp.WithDescription(
			"Create a new objective for autonomous agent swarms. "+
				"An objective defines a goal that spawns a tree of missions. "+
				"Use fracta_spawn with objective_id to launch the root mission.",
		),
		mcp.WithString("description", mcp.Description("Goal description for the objective"), mcp.Required()),
		mcp.WithString("id", mcp.Description("Optional objective ID (auto-generated if empty)")),
		mcp.WithNumber("max_missions", mcp.Description("Maximum number of missions (default 100)")),
		mcp.WithNumber("max_depth", mcp.Description("Maximum mission tree depth (default 5)")),
		mcp.WithNumber("max_branching", mcp.Description("Maximum active children per mission (default 5)")),
		mcp.WithString("max_runtime", mcp.Description("Maximum wall-clock runtime as duration string (default '4h')")),
	), s.handleCreateObjective)

	s.mcp.AddTool(mcp.NewTool("fracta_list_objectives",
		mcp.WithDescription("List objectives with status, budget usage, and mission/finding counts."),
		mcp.WithString("status", mcp.Description("Filter by status (open, answered, disproven, exhausted, budget_exhausted, timed_out, frozen). Omit for all.")),
	), s.handleListObjectives)

	s.mcp.AddTool(mcp.NewTool("fracta_unfreeze_objective",
		mcp.WithDescription(
			"Unfreeze a frozen objective (frozen → open). "+
				"Use after a circuit breaker trip to resume the objective.",
		),
		mcp.WithString("id", mcp.Description("Objective ID to unfreeze"), mcp.Required()),
	), s.handleUnfreezeObjective)
}

func (s *Server) handleCreateObjective(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireRoot(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if s.cpClient == nil {
		return mcp.NewToolResultError("control plane client not configured"), nil
	}

	description, err := request.RequireString("description")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter: description"), nil
	}

	resp, err := s.cpClient.CreateObjective(ctx, cpapi.CreateObjectiveRequest{
		ID:           request.GetString("id", ""),
		Description:  description,
		MaxMissions:  request.GetInt("max_missions", 0),
		MaxDepth:     request.GetInt("max_depth", 0),
		MaxBranching: request.GetInt("max_branching", 0),
		MaxRuntime:   request.GetString("max_runtime", ""),
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create objective failed: %v", err)), nil
	}

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func (s *Server) handleListObjectives(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireRoot(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if s.cpClient == nil {
		return mcp.NewToolResultError("control plane client not configured"), nil
	}

	resp, err := s.cpClient.ListObjectives(ctx, cpapi.ListObjectivesRequest{
		Status: request.GetString("status", ""),
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list objectives failed: %v", err)), nil
	}

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func (s *Server) handleUnfreezeObjective(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireRoot(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if s.cpClient == nil {
		return mcp.NewToolResultError("control plane client not configured"), nil
	}

	id, err := request.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter: id"), nil
	}

	resp, err := s.cpClient.UnfreezeObjective(ctx, cpapi.UnfreezeObjectiveRequest{ID: id})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("unfreeze failed: %v", err)), nil
	}

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}
