package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/mailbox"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/objective"
	"github.com/darkquasar/fracta/internal/orchestrator"
	"github.com/darkquasar/fracta/internal/proposal"
	"github.com/darkquasar/fracta/internal/state"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// agentTaskKey is the context key for enforcing agent identity in gateway mode.
// When present, handlers override caller-supplied from/name parameters.
type agentTaskKey struct{}

// agentTaskFromContext returns the agent task name injected by the HTTP
// transport from the URL path (/agents/{task}/mcp). Returns "" in stdio
// mode, preserving current behavior.
func agentTaskFromContext(ctx context.Context) string {
	if task, ok := ctx.Value(agentTaskKey{}).(string); ok {
		return task
	}
	return ""
}

// newAgentOrchestrator creates a lightweight orchestrator with only
// store + mailbox wired — sufficient for agent-facing tool handlers.
func newAgentOrchestrator(store state.Store, mb mailbox.Mailbox) *orchestrator.Orchestrator {
	reg := host.NewMapRegistry("noop")
	reg.Register("noop", host.NoopHost{})
	return orchestrator.New(reg, nil, store, mb, "")
}

// registerAgentTools registers the inter-agent communication tools on an MCPServer.
// These tools are shared across Server, AgentServer, and GatewayServer surfaces.
// agentToolsCfg holds optional observability stores for agent tools.
type agentToolsCfg struct {
	snapshotStore *events.SnapshotStore
}

type agentToolsOption func(*agentToolsCfg)

// WithAgentToolsSnapshotStore sets the snapshot store for enriched agent list/peek.
func WithAgentToolsSnapshotStore(s *events.SnapshotStore) agentToolsOption {
	return func(c *agentToolsCfg) { c.snapshotStore = s }
}

func registerAgentTools(m *server.MCPServer, store state.Store, mb mailbox.Mailbox, opts ...agentToolsOption) {
	var cfg agentToolsCfg
	for _, o := range opts {
		o(&cfg)
	}

	m.AddTool(mcp.NewTool("fracta_list",
		mcp.WithDescription("List all agents with name, status, mode, branch, intent, and unread messages. Use to discover peer agents and check if work you depend on is done."),
	), makeAgentListHandler(store, mb, cfg.snapshotStore))

	m.AddTool(mcp.NewTool("fracta_peek",
		mcp.WithDescription("Read another agent's recent log output. Useful for checking what a peer has done without sending a message."),
		mcp.WithString("name",
			mcp.Description("Agent task name to peek at"),
			mcp.Required(),
		),
	), makeAgentPeekHandler(store, mb, cfg.snapshotStore))

	m.AddTool(mcp.NewTool("fracta_send",
		mcp.WithDescription("Send a message to another agent's or the chessmaster's mailbox. Use to='chessmaster' to notify the orchestrator when your work is ready to merge."),
		mcp.WithString("from",
			mcp.Description("Your agent task name (same as your branch suffix: branch feature/foo -> name 'foo')"),
			mcp.Required(),
		),
		mcp.WithString("to",
			mcp.Description("Recipient: another agent's task name, or 'chessmaster' to notify the orchestrator"),
			mcp.Required(),
		),
		mcp.WithString("message",
			mcp.Description("Message content"),
			mcp.Required(),
		),
	), makeAgentSendHandler(store, mb))

	m.AddTool(mcp.NewTool("fracta_inbox",
		mcp.WithDescription("Read unread messages from your mailbox. Returns messages from the chessmaster or peer agents, then advances the cursor. Check periodically."),
		mcp.WithString("name",
			mcp.Description("Your agent task name (same as your branch suffix: branch feature/foo -> name 'foo')"),
			mcp.Required(),
		),
	), makeAgentInboxHandler(store, mb))

	m.AddTool(mcp.NewTool("fracta_set_intent",
		mcp.WithDescription("Set your current intent so the chessmaster and peer agents can see what you're working on. Update when starting new sub-tasks."),
		mcp.WithString("name",
			mcp.Description("Your agent task name (same as your branch suffix: branch feature/foo -> name 'foo')"),
			mcp.Required(),
		),
		mcp.WithString("intent",
			mcp.Description("Short description of what you're currently working on"),
			mcp.Required(),
		),
	), makeAgentSetIntentHandler(store, mb))
}

func makeAgentListHandler(store state.Store, mb mailbox.Mailbox, snapStore *events.SnapshotStore) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		st, err := store.Load(ctx)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("loading state: %v", err)), nil
		}

		orch := newAgentOrchestrator(store, mb)

		type agentInfo struct {
			Name           string            `json:"name"`
			Status         model.AgentStatus `json:"status"`
			Mode           string            `json:"mode"`
			Branch         string            `json:"branch"`
			CurrentIntent  string            `json:"current_intent,omitempty"`
			UnreadMessages int               `json:"unread_messages"`
			CurrentPhase   string            `json:"current_phase,omitempty"`
			CurrentTool    string            `json:"current_tool,omitempty"`
		}

		agents := make([]agentInfo, 0, len(st.Agents))
		for _, a := range st.Agents {
			info := agentInfo{
				Name:           a.Task,
				Status:         a.Status,
				Mode:           a.Mode,
				Branch:         a.BranchName,
				CurrentIntent:  a.CurrentIntent,
				UnreadMessages: orch.UnreadCount(a.Task),
			}
			// Enrich from snapshot store when available.
			if snapStore != nil {
				if snap := snapStore.Get(a.Task); snap != nil {
					info.CurrentPhase = snap.CurrentPhase
					info.CurrentTool = snap.CurrentTool
				}
			}
			agents = append(agents, info)
		}

		data, err := json.Marshal(agents)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshalling response: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

func makeAgentPeekHandler(store state.Store, mb mailbox.Mailbox, snapStore *events.SnapshotStore) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, err := request.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: name"), nil
		}

		// Try snapshot excerpt first (fast, works across all agent types).
		if snapStore != nil {
			if snap := snapStore.Get(name); snap != nil && snap.LastMessageExcerpt != "" {
				return mcp.NewToolResultText(snap.LastMessageExcerpt), nil
			}
		}

		// Fallback to existing log/store path.
		orch := newAgentOrchestrator(store, mb)
		output, err := orch.Peek(name)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("peek failed: %v", err)), nil
		}

		return mcp.NewToolResultText(output), nil
	}
}

func makeAgentSendHandler(store state.Store, mb mailbox.Mailbox) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		from, err := request.RequireString("from")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: from"), nil
		}

		to, err := request.RequireString("to")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: to"), nil
		}

		message, err := request.RequireString("message")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: message"), nil
		}

		// Agent identity enforcement: in gateway mode, override caller-supplied from.
		if identity := agentTaskFromContext(ctx); identity != "" {
			from = identity
		}

		orch := newAgentOrchestrator(store, mb)
		if err := orch.SendMessage(from, to, message); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("send failed: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Message sent to %q.", to)), nil
	}
}

func makeAgentInboxHandler(store state.Store, mb mailbox.Mailbox) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, err := request.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: name"), nil
		}

		// Agent identity enforcement: in gateway mode, override caller-supplied name.
		if identity := agentTaskFromContext(ctx); identity != "" {
			name = identity
		}

		orch := newAgentOrchestrator(store, mb)
		messages, err := orch.ReadInbox(name)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("inbox failed: %v", err)), nil
		}

		if len(messages) == 0 {
			return mcp.NewToolResultText(
				"No unread messages.\n\n" +
					"Polling tip: Focus on your own task progress. " +
					"Check inbox again after completing your next work unit or after 30+ seconds.",
			), nil
		}

		data, err := json.Marshal(messages)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshalling response: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

func makeAgentSetIntentHandler(store state.Store, mb mailbox.Mailbox) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, err := request.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: name"), nil
		}

		intent, err := request.RequireString("intent")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: intent"), nil
		}

		// Agent identity enforcement: in gateway mode, override caller-supplied name.
		if identity := agentTaskFromContext(ctx); identity != "" {
			name = identity
		}

		if err := store.UpdateAgentIntent(ctx, name, intent); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("set intent failed: %v", err)), nil
		}

		newAgentOrchestrator(store, mb).SnapshotProgress()

		return mcp.NewToolResultText(fmt.Sprintf("Intent updated for %q.", name)), nil
	}
}

// registerObjectiveTools adds objective-aware tools (propose, finding, resolve)
// to the MCP server. The resolver provides per-request objective context —
// StaticResolver for stdio mode, StoreResolver for HTTP gateway mode.
func registerObjectiveTools(
	mcpSrv *server.MCPServer,
	resolver ObjectiveContextResolver,
	objStore objective.ObjectiveStore,
	proposalStore proposal.ProposalStore,
) {
	mcpSrv.AddTool(mcp.NewTool("fracta_propose_mission",
		mcp.WithDescription(
			"Propose a child mission for the current objective. "+
				"The proposal goes to the admission controller — it is NOT immediately executed. "+
				"Include a dedupe_key to prevent duplicate work. "+
				"Your objective_id and mission_id are derived automatically from your agent context.",
		),
		mcp.WithString("task", mcp.Description("Task description for the proposed child mission"), mcp.Required()),
		mcp.WithString("contract", mcp.Description("Detailed contract/instructions for the child agent")),
		mcp.WithString("dedupe_key", mcp.Description("Stable fingerprint to prevent duplicate work (e.g. investigate:host=srv-01)"), mcp.Required()),
		mcp.WithString("rationale", mcp.Description("Why this child mission is needed"), mcp.Required()),
		mcp.WithString("evidence", mcp.Description("Supporting evidence (required after 3+ missions in the objective)")),
		mcp.WithNumber("priority", mcp.Description("Priority (higher = processed first, default 0)")),
	), makeProposeMissionHandler(resolver, proposalStore))

	mcpSrv.AddTool(mcp.NewTool("fracta_report_finding",
		mcp.WithDescription(
			"Report a finding for the current objective. "+
				"Increments the finding counter (used by the circuit breaker). "+
				"Your objective_id is derived automatically from your agent context.",
		),
		mcp.WithString("summary", mcp.Description("Summary of the finding"), mcp.Required()),
		mcp.WithString("graph_node_id", mcp.Description("Optional graph node ID associated with this finding")),
	), makeReportFindingHandler(resolver, objStore))

	mcpSrv.AddTool(mcp.NewTool("fracta_resolve_objective",
		mcp.WithDescription(
			"Declare the objective as answered or disproven. "+
				"Only use when you have sufficient evidence to conclude. "+
				"Your objective_id is derived automatically from your agent context.",
		),
		mcp.WithString("status", mcp.Description("Outcome status"), mcp.Required(), mcp.Enum("answered", "disproven")),
		mcp.WithString("outcome", mcp.Description("Summary of the outcome"), mcp.Required()),
	), makeResolveObjectiveHandler(resolver, objStore))
}

func makeProposeMissionHandler(resolver ObjectiveContextResolver, proposalStore proposal.ProposalStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctxTask := agentTaskFromContext(ctx)
		agentTask, objectiveID, missionID, err := resolver.Resolve(ctx, ctxTask)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("resolving objective context: %v", err)), nil
		}

		taskName, err := request.RequireString("task")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: task"), nil
		}
		dedupeKey, err := request.RequireString("dedupe_key")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: dedupe_key"), nil
		}
		rationale, err := request.RequireString("rationale")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: rationale"), nil
		}

		contractStr := request.GetString("contract", "")
		evidence := request.GetString("evidence", "")
		priority := request.GetInt("priority", 0)

		var evidenceJSON json.RawMessage
		if evidence != "" {
			evidenceJSON, _ = json.Marshal(evidence)
		}

		p := &proposal.MissionProposal{
			ObjectiveID:   objectiveID,
			ParentMission: missionID,
			ProposedBy:    agentTask,
			Task:          taskName,
			Contract:      contractStr,
			Priority:      priority,
			DedupeKey:     dedupeKey,
			Rationale:     rationale,
			Evidence:      evidenceJSON,
			Status:        proposal.StatusPending,
		}

		if err := proposalStore.Submit(ctx, p); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("submit proposal failed: %v", err)), nil
		}

		result := map[string]interface{}{
			"proposal_id":  p.ID,
			"objective_id": objectiveID,
			"status":       "pending",
			"message":      "Proposal submitted. The admission controller will evaluate it asynchronously.",
		}
		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil
	}
}

func makeReportFindingHandler(resolver ObjectiveContextResolver, objStore objective.ObjectiveStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctxTask := agentTaskFromContext(ctx)
		_, objectiveID, _, err := resolver.Resolve(ctx, ctxTask)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("resolving objective context: %v", err)), nil
		}

		summary, err := request.RequireString("summary")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: summary"), nil
		}
		graphNodeID := request.GetString("graph_node_id", "")

		if err := objStore.IncrementFindingCount(ctx, objectiveID); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("report finding failed: %v", err)), nil
		}

		result := map[string]interface{}{
			"objective_id":  objectiveID,
			"summary":       summary,
			"graph_node_id": graphNodeID,
			"message":       "Finding recorded. Objective finding count incremented.",
		}
		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil
	}
}

func makeResolveObjectiveHandler(resolver ObjectiveContextResolver, objStore objective.ObjectiveStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctxTask := agentTaskFromContext(ctx)
		_, objectiveID, _, err := resolver.Resolve(ctx, ctxTask)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("resolving objective context: %v", err)), nil
		}

		statusStr, err := request.RequireString("status")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: status"), nil
		}
		outcome, err := request.RequireString("outcome")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: outcome"), nil
		}

		var targetStatus objective.ObjectiveStatus
		switch statusStr {
		case "answered":
			targetStatus = objective.StatusAnswered
		case "disproven":
			targetStatus = objective.StatusDisproven
		default:
			return mcp.NewToolResultError(fmt.Sprintf("invalid status %q: must be 'answered' or 'disproven'", statusStr)), nil
		}

		obj, err := objStore.Get(ctx, objectiveID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("get objective failed: %v", err)), nil
		}

		if !objective.CanTransition(obj.Status, targetStatus) {
			return mcp.NewToolResultError(fmt.Sprintf(
				"cannot transition objective from %s to %s", obj.Status, targetStatus)), nil
		}

		obj.Status = targetStatus
		obj.Outcome = outcome
		if err := objStore.Update(ctx, obj); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("update objective failed: %v", err)), nil
		}

		result := map[string]interface{}{
			"objective_id": objectiveID,
			"status":       string(targetStatus),
			"outcome":      outcome,
			"message":      fmt.Sprintf("Objective resolved as %s.", targetStatus),
		}
		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil
	}
}
