package mcpserver

import (
	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/mailbox"
	"github.com/darkquasar/fracta/internal/objective"
	"github.com/darkquasar/fracta/internal/proposal"
	"github.com/darkquasar/fracta/internal/state"
	"github.com/darkquasar/fracta/internal/strategy"
	"github.com/mark3labs/mcp-go/server"
)

// agentObjectiveCtx holds objective-scoped identity for an agent.
// When set, the agent server registers objective-aware tools (propose, finding, resolve).
type agentObjectiveCtx struct {
	agentTask   string // this agent's task name
	objectiveID string // the objective this agent serves
	missionID   int64  // the mission this agent is executing
}

// AgentServer is a restricted MCP server for use by spawned agents.
// It exposes only inter-agent communication tools (no spawn/kill/merge).
type AgentServer struct {
	root          string
	store         state.Store
	mailbox       mailbox.Mailbox
	mcp           *server.MCPServer
	graph         graph.GraphClient
	strategy      strategy.Runner
	objStore      objective.ObjectiveStore
	proposalStore proposal.ProposalStore
	objCtx        *agentObjectiveCtx // nil when agent has no objective context
}

// AgentServerOption configures an AgentServer.
type AgentServerOption func(*AgentServer)

// WithAgentStore sets the state store for the agent server.
func WithAgentStore(s state.Store) AgentServerOption {
	return func(as *AgentServer) {
		as.store = s
	}
}

// WithAgentMailbox sets the mailbox backend for the agent server.
func WithAgentMailbox(m mailbox.Mailbox) AgentServerOption {
	return func(as *AgentServer) {
		as.mailbox = m
	}
}

// WithAgentGraphClient attaches a graph database client to the agent server.
func WithAgentGraphClient(gc graph.GraphClient) AgentServerOption {
	return func(s *AgentServer) {
		s.graph = gc
	}
}

// WithAgentStrategyRunner attaches a strategy runner to the agent server.
func WithAgentStrategyRunner(r strategy.Runner) AgentServerOption {
	return func(s *AgentServer) {
		s.strategy = r
	}
}

// WithAgentObjectiveStore sets the objective store for objective-aware tools.
func WithAgentObjectiveStore(os objective.ObjectiveStore) AgentServerOption {
	return func(s *AgentServer) {
		s.objStore = os
	}
}

// WithAgentProposalStore sets the proposal store for objective-aware tools.
func WithAgentProposalStore(ps proposal.ProposalStore) AgentServerOption {
	return func(s *AgentServer) {
		s.proposalStore = ps
	}
}

// WithAgentObjectiveContext sets the agent's objective identity.
// When set, the server registers fracta_propose_mission, fracta_report_finding,
// and fracta_resolve_objective. The objective_id and mission_id are derived from
// the control plane — the agent cannot spoof them.
func WithAgentObjectiveContext(agentTask, objectiveID string, missionID int64) AgentServerOption {
	return func(s *AgentServer) {
		s.objCtx = &agentObjectiveCtx{
			agentTask:   agentTask,
			objectiveID: objectiveID,
			missionID:   missionID,
		}
	}
}

// NewAgentServer creates an agent-mode MCP server with restricted tools.
func NewAgentServer(root string, opts ...AgentServerOption) *AgentServer {
	s := &AgentServer{
		root: root,
		mcp: server.NewMCPServer(
			"fracta-agent",
			"0.1.0",
			server.WithToolCapabilities(false),
		),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.registerTools()
	if s.objCtx != nil && s.objStore != nil && s.proposalStore != nil {
		resolver := &StaticResolver{
			AgentTask:   s.objCtx.agentTask,
			ObjectiveID: s.objCtx.objectiveID,
			MissionID:   s.objCtx.missionID,
		}
		registerObjectiveTools(s.mcp, resolver, s.objStore, s.proposalStore)
	}
	if s.graph != nil {
		registerGraphTools(s.mcp, s.graph)
		registerCheckpointTool(s.mcp, s.graph, nil)
	}
	if s.strategy != nil {
		registerStrategyTools(s.mcp, s.strategy, s.graph, false, nil, nil, nil, nil, nil, nil)
	}
	return s
}

// Serve starts the agent MCP server on stdio.
func (s *AgentServer) Serve() error {
	if s.graph != nil {
		defer s.graph.Close()
	}
	if s.strategy != nil {
		defer s.strategy.Close()
	}
	return server.ServeStdio(s.mcp)
}

func (s *AgentServer) registerTools() {
	registerAgentTools(s.mcp, s.store, s.mailbox)
}

// Objective tools are registered via the standalone registerObjectiveTools()
// in agent_tools.go, using ObjectiveContextResolver for per-request context.
