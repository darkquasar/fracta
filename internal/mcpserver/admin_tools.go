package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/darkquasar/fracta/internal/project"
	"github.com/darkquasar/fracta/internal/project/scaffolds"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerAdminTools registers the admin-only tools on a Server.
// These are NOT included on AgentServer or GatewayServer surfaces.
func (s *Server) registerAdminTools() {
	s.mcp.AddTool(mcp.NewTool("fracta_init",
		mcp.WithDescription("Initialize fracta in a git repository. Creates .fracta/ config and state files. Must be called before other fracta tools."),
		mcp.WithString("path",
			mcp.Description("Path to the git repository (defaults to current directory)"),
		),
	), s.handleInit)

	s.mcp.AddTool(mcp.NewTool("fracta_spawn",
		mcp.WithDescription("Spawn a new agent with a dedicated git worktree. Creates branch feature/<task>. Use fracta_list to monitor progress."),
		mcp.WithString("task",
			mcp.Description("Unique task name (alphanumeric, hyphens, underscores). Becomes branch feature/<task>."),
			mcp.Required(),
		),
		mcp.WithString("contract",
			mcp.Description("Task instructions: inline text or path to a file. Written to the agent's workspace."),
		),
		mcp.WithString("base",
			mcp.Description("Base branch for the worktree (defaults to current branch of main repo)"),
		),
		mcp.WithString("model",
			mcp.Description("Model to use (overrides config default and tier)"),
		),
		mcp.WithString("tier",
			mcp.Description("Model tier: maps to a model ID via config model_tiers. Ignored when explicit model is set."),
			mcp.Enum("heavy", "medium", "light"),
		),
		mcp.WithString("mode",
			mcp.Description("'batch' (default, runs to completion) or 'stream' (stays alive for follow-up via fracta_say)"),
		),
		mcp.WithString("dispatch",
			mcp.Description("'direct' (default) or 'queued' (submit to mission queue)"),
			mcp.Enum("direct", "queued"),
		),
		mcp.WithString("runtime",
			mcp.Description("Runtime implementation to use (e.g. 'claude'). Defaults to registry default."),
		),
		mcp.WithString("host_type",
			mcp.Description("Deprecated: use 'runtime' instead."),
		),
		mcp.WithString("objective_id",
			mcp.Description("Link this mission as the root of an objective's mission DAG (depth=0, no parent). The objective must exist and be open."),
		),
	), s.handleSpawn)

	s.mcp.AddTool(mcp.NewTool("fracta_say",
		mcp.WithDescription("Resume an agent session with a new conversation turn. Unlike fracta_send (async mailbox), this directly injects into the agent's context. Use for stream-mode agents (Idle) or to re-run batch agents."),
		mcp.WithString("name",
			mcp.Description("Agent task name"),
			mcp.Required(),
		),
		mcp.WithString("message",
			mcp.Description("Message to inject into the agent's session"),
			mcp.Required(),
		),
	), s.handleSay)

	s.mcp.AddTool(mcp.NewTool("fracta_kill",
		mcp.WithDescription("Terminate an agent: removes worktree, deletes feature branch, clears mailbox and state. Use fracta_merge first if you want to keep the agent's work. Irreversible."),
		mcp.WithString("name",
			mcp.Description("Agent task name"),
			mcp.Required(),
		),
		mcp.WithBoolean("keep_files",
			mcp.Description("Keep worktree files and branch instead of deleting them (default: false)"),
		),
	), s.handleKill)

	s.mcp.AddTool(mcp.NewTool("fracta_merge",
		mcp.WithDescription("Merge an agent's feature branch into the current branch. Non-destructive: agent stays alive. Use fracta_kill to clean up when done."),
		mcp.WithString("name",
			mcp.Description("Agent task name"),
			mcp.Required(),
		),
	), s.handleMerge)
}

// --- Admin tool handlers (Server methods) ---

func (s *Server) handleInit(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path := request.GetString("path", "")
	if path == "" {
		var err error
		path, err = os.Getwd()
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get working directory: %v", err)), nil
		}
	}

	if _, err := project.Init(path, project.InitOpts{
		Scaffold:   scaffolds.KindLocal,
		OnConflict: scaffolds.ConflictSkipExisting,
	}); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("init failed: %v", err)), nil
	}

	s.root = path
	return mcp.NewToolResultText(fmt.Sprintf("Fracta initialized at %s", path)), nil
}

func (s *Server) handleSpawn(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireRoot(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if s.cpClient == nil {
		return mcp.NewToolResultError("control plane client not configured"), nil
	}

	task, err := request.RequireString("task")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter: task"), nil
	}

	// Accept both "runtime" (preferred) and "host_type" (deprecated).
	runtimeType := request.GetString("runtime", "")
	if runtimeType == "" {
		runtimeType = request.GetString("host_type", "")
	}
	resp, err := s.cpClient.Spawn(ctx, cpapi.SpawnRequest{
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

func (s *Server) handleSay(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireRoot(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if s.cpClient == nil {
		return mcp.NewToolResultError("control plane client not configured"), nil
	}

	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter: name"), nil
	}

	message, err := request.RequireString("message")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter: message"), nil
	}

	resp, err := s.cpClient.Say(ctx, cpapi.SayRequest{Name: name, Message: message})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("say failed: %v", err)), nil
	}
	return mcp.NewToolResultText(resp.Message), nil
}

func (s *Server) handleKill(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireRoot(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if s.cpClient == nil {
		return mcp.NewToolResultError("control plane client not configured"), nil
	}

	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter: name"), nil
	}

	keepFiles := request.GetBool("keep_files", false)

	resp, err := s.cpClient.Kill(ctx, cpapi.KillRequest{Name: name, KeepFiles: keepFiles})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("kill failed: %v", err)), nil
	}
	return mcp.NewToolResultText(resp.Message), nil
}

func (s *Server) handleMerge(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.requireRoot(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if s.cpClient == nil {
		return mcp.NewToolResultError("control plane client not configured"), nil
	}

	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("missing required parameter: name"), nil
	}

	resp, err := s.cpClient.Merge(ctx, cpapi.MergeRequest{Name: name})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("merge failed: %v", err)), nil
	}
	return mcp.NewToolResultText(resp.Message), nil
}
