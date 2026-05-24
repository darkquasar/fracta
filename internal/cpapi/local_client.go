package cpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/darkquasar/fracta/internal/agentpolicy"
	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/contract"
	"github.com/darkquasar/fracta/internal/controlplane"
	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/git"
	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/objective"
	"github.com/darkquasar/fracta/internal/orchestrator"
	"github.com/darkquasar/fracta/internal/queue"
	"github.com/darkquasar/fracta/internal/state"
	"github.com/google/uuid"
	"time"
)

// Compile-time interface satisfaction check.
var _ ControlPlaneClient = (*LocalControlPlaneClient)(nil)

// LocalControlPlaneClient implements ControlPlaneClient by composing
// authoritative domain components directly. It is a thin intent router —
// not a monolithic facade over ControlPlane.
//
// Operation ownership (per spec S5.4):
//   - Spawn/Say/Kill: delegate to Orchestrator
//   - ListAgents/GetAgent: delegate to Store
//   - Peek: ProcessRegistry first, then Orchestrator fallback
//   - GetLogs: delegate to Backend
//   - Objectives: delegate to ObjectiveStore
type LocalControlPlaneClient struct {
	cp           *controlplane.ControlPlane
	registry     *orchestrator.ProcessRegistry
	hostRegistry host.HostRegistry
	objStore     objective.ObjectiveStore
	root         string

	// Agent-mode wiring threaded into orchestrator for .mcp.json.
	configPath  string
	graphAddr   string
	strategyDir string

	// Graph access (spec-37). nil = graph not configured.
	graphClient graph.GraphClient

	// Observability stores (spec-35).
	snapshotStore *events.SnapshotStore
	eventStore    *events.EventStore
	eventReader   state.EventReader
}

// LocalClientOption configures a LocalControlPlaneClient.
type LocalClientOption func(*LocalControlPlaneClient)

// WithProcessRegistry sets the shared ProcessRegistry for live stream sessions.
func WithProcessRegistry(r *orchestrator.ProcessRegistry) LocalClientOption {
	return func(c *LocalControlPlaneClient) { c.registry = r }
}

// WithRuntimeRegistry sets the runtime registry for resolving host implementations.
func WithRuntimeRegistry(reg host.RuntimeRegistry) LocalClientOption {
	return func(c *LocalControlPlaneClient) { c.hostRegistry = reg }
}

// WithHostRegistry is a deprecated alias for WithRuntimeRegistry.
var WithHostRegistry = WithRuntimeRegistry

// WithObjectiveStore sets the objective store for objective operations.
func WithObjectiveStore(os objective.ObjectiveStore) LocalClientOption {
	return func(c *LocalControlPlaneClient) { c.objStore = os }
}

// WithAgentWiring sets paths threaded into the orchestrator for agent-mode.
func WithAgentWiring(configPath, graphAddr, strategyDir string) LocalClientOption {
	return func(c *LocalControlPlaneClient) {
		c.configPath = configPath
		c.graphAddr = graphAddr
		c.strategyDir = strategyDir
	}
}

// WithSnapshotStore sets the snapshot store for observability.
func WithSnapshotStore(s *events.SnapshotStore) LocalClientOption {
	return func(c *LocalControlPlaneClient) { c.snapshotStore = s }
}

// WithEventStore sets the event store (ring buffer) for observability.
func WithEventStore(s *events.EventStore) LocalClientOption {
	return func(c *LocalControlPlaneClient) { c.eventStore = s }
}

// WithGraphClient sets the graph client for graph operations.
// When nil, all graph methods return "graph not configured".
func WithGraphClient(gc graph.GraphClient) LocalClientOption {
	return func(c *LocalControlPlaneClient) { c.graphClient = gc }
}

// WithEventReader sets the DB event reader for ring buffer fallback.
func WithEventReader(r state.EventReader) LocalClientOption {
	return func(c *LocalControlPlaneClient) { c.eventReader = r }
}

// NewLocalControlPlaneClient creates a LocalControlPlaneClient from a ControlPlane and options.
func NewLocalControlPlaneClient(cp *controlplane.ControlPlane, root string, opts ...LocalClientOption) *LocalControlPlaneClient {
	c := &LocalControlPlaneClient{
		cp:   cp,
		root: root,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// newOrchestrator constructs a transient Orchestrator from the authoritative
// components. Mirrors the pattern in mcpserver.Server.newOrchestrator().
func (c *LocalControlPlaneClient) newOrchestrator() (*orchestrator.Orchestrator, error) {
	if c.hostRegistry == nil {
		return nil, fmt.Errorf("cpapi: host registry not configured")
	}
	orch := orchestrator.New(c.hostRegistry, c.cp.Workspace, c.cp.Store, c.cp.Mailbox, c.root)
	orch.Backend = c.cp.Backend
	orch.Queue = c.cp.Queue
	orch.SpawnChecker = c.cp.Reaper
	orch.RuntimeBackend = c.cp.Profile.BackendType
	orch.Config = c.cp.Config
	if c.cp.Config != nil {
		orch.MCPServers = c.cp.Config.MCPServers
	}
	orch.ConfigPath = c.configPath
	orch.GraphAddr = c.graphAddr
	orch.StrategyDir = c.strategyDir
	orch.Events = c.cp.Events
	if c.cp.Lifecycle != nil {
		orch.Lifecycle = c.cp.Lifecycle.CloneWithProgressHook(orch.SnapshotProgress)
	}
	return orch, nil
}

// Spawn creates a new agent via direct or queued dispatch.
// Uses ResolveSpawn for all parameter resolution (single resolution path).
func (c *LocalControlPlaneClient) Spawn(ctx context.Context, req SpawnRequest) (*SpawnResponse, error) {
	content, err := contract.ResolveContract(req.Contract)
	if err != nil {
		return nil, fmt.Errorf("resolving contract: %w", err)
	}

	baseBranch := req.BaseBranch
	if baseBranch == "" {
		if branch, err := git.NewRunner(c.root).CurrentBranch(); err == nil {
			baseBranch = branch
		} else if c.cp != nil && c.cp.Config != nil && c.cp.Config.Project.DefaultBaseBranch != "" {
			baseBranch = c.cp.Config.Project.DefaultBaseBranch
		}
	}

	orch, err := c.newOrchestrator()
	if err != nil {
		return nil, err
	}

	// Resolve mode: explicit > config > "batch"
	mode := req.Mode
	if mode == "" {
		if orch.Config != nil && orch.Config.Agents.DefaultMode != "" {
			mode = orch.Config.Agents.DefaultMode
		} else {
			mode = "batch"
		}
	}

	// Dispatch routing
	useQueued := false
	switch req.Dispatch {
	case "queued":
		if orch.Queue == nil {
			return nil, fmt.Errorf("queue mode not configured")
		}
		useQueued = true
	case "direct":
		useQueued = false
	default:
		if mode == "stream" {
			useQueued = false
		} else if orch.Queue != nil {
			useQueued = true
		}
	}

	if req.ObjectiveID != "" && !useQueued {
		return nil, fmt.Errorf("objective_id requires queued dispatch (set dispatch='queued' or configure a queue backend)")
	}

	// Local queued dispatch doesn't support cross-boundary credential staging.
	// The worker builds credential plans at TopologyInCluster and can't prepare
	// host-edge sources. If StagedCredentialRefs are provided (from a remote
	// staging wrapper), they'll be carried through. But if a host-edge source
	// would be needed and no refs were staged, fail clearly.
	if useQueued && len(req.StagedCredentialRefs) == 0 {
		hostType := req.RuntimeType
		if hostType == "" && orch.Config != nil {
			hostType = orch.Config.Agents.EffectiveDefaultRuntime()
		}
		if hostType != "" && orch.Config != nil {
			if credProfile, _, resolveErr := config.ResolveCredentialProfile(orch.Config, hostType); resolveErr == nil && credProfile != nil {
				for name, src := range credProfile.AuthOrigins {
					if src.Scope == "host_edge" && (src.Required == nil || *src.Required) {
						return nil, fmt.Errorf(
							"local queued spawn for host %q has required host_edge credential source %q but no staged credentials; "+
								"use direct dispatch or configure remote mode with staging",
							hostType, name,
						)
					}
				}
			}
		}
	}

	if useQueued {
		return c.spawnQueued(ctx, orch, req.Task, content, baseBranch, req.Model, req.Tier, req.RuntimeType, req.ObjectiveID, req.StagedCredentialRefs)
	}

	if mode == "stream" {
		if err := orch.SpawnStream(req.Task, content, baseBranch, req.Model, req.Tier, req.RuntimeType, c.registry); err != nil {
			return nil, fmt.Errorf("spawn failed: %w", err)
		}
		return &SpawnResponse{
			Agent:    req.Task,
			Branch:   "feature/" + req.Task,
			Worktree: fmt.Sprintf("%s/%s/%s", c.root, model.WorktreeDir, req.Task),
			Status:   "running",
			Mode:     "stream",
		}, nil
	}

	// Default: batch mode (direct dispatch)
	if err := orch.SpawnAsync(req.Task, content, baseBranch, req.Model, req.Tier, req.RuntimeType); err != nil {
		return nil, fmt.Errorf("spawn failed: %w", err)
	}
	return &SpawnResponse{
		Agent:    req.Task,
		Branch:   "feature/" + req.Task,
		Worktree: fmt.Sprintf("%s/%s/%s", c.root, model.WorktreeDir, req.Task),
		Status:   "running",
		Mode:     "batch",
	}, nil
}

// spawnQueued enqueues a mission for worker execution.
// Uses ResolveSpawn (single resolution path per spec S5.3 and lessons L5).
func (c *LocalControlPlaneClient) spawnQueued(
	ctx context.Context,
	orch *orchestrator.Orchestrator,
	task, content, baseBranch, spawnModel, spawnTier, hostType, objectiveID string,
	stagedCredentialRefs map[string]string,
) (*SpawnResponse, error) {
	if err := model.ValidateTaskName(task); err != nil {
		return nil, err
	}

	resolved, err := orch.ResolveSpawn(hostType, spawnModel, spawnTier, baseBranch, "")
	if err != nil {
		return nil, fmt.Errorf("resolving spawn: %w", err)
	}
	fractalog.Component("cpapi").Debug("spawnQueued resolved", "model", resolved.Model, "runtime", resolved.RuntimeType, "inputModel", spawnModel, "inputTier", spawnTier, "runtimesCount", len(orch.Config.EffectiveRuntimes()))

	if objectiveID != "" && c.objStore != nil {
		obj, err := c.objStore.Get(ctx, objectiveID)
		if err != nil {
			return nil, fmt.Errorf("objective %q not found: %w", objectiveID, err)
		}
		if obj.Status != objective.StatusOpen {
			return nil, fmt.Errorf("objective %q is %s (must be open)", objectiveID, obj.Status)
		}
	} else if objectiveID != "" && c.objStore == nil {
		return nil, fmt.Errorf("objective_id specified but objective store not configured")
	}

	// Build ExecutionSpec — single canonical construction replacing 15 manual assignments.
	spec := orchestrator.NewExecutionSpec(resolved, task, content, objectiveID, orch)
	if len(stagedCredentialRefs) > 0 {
		spec.Credentials = &orchestrator.SpecCredentials{
			StagedCredentialRefs: stagedCredentialRefs,
		}
	}

	payload := spec.ToMissionPayload()
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshalling payload: %w", err)
	}

	mission := &queue.Mission{
		AgentTask:   task,
		Payload:     payloadBytes,
		ObjectiveID: objectiveID,
	}
	agent := &model.AgentEntry{
		Task:        task,
		RuntimeType: resolved.RuntimeType,
		Status:      model.StatusQueued,
		Mode:        "queued",
		ObjectiveID: objectiveID,
	}

	if err := orch.Queue.Enqueue(ctx, mission, agent); err != nil {
		return nil, fmt.Errorf("enqueue failed: %w", err)
	}

	if orch.Events != nil {
		e := events.Info("orchestrator", "enqueue")
		e.Category = "queue"
		e.Task = task
		e.Resource = "task:" + task
		e.MissionID = mission.ID
		e.Attrs = map[string]string{
			"mission_id": fmt.Sprintf("%d", mission.ID),
			"runtime":    resolved.RuntimeType,
			"model":      resolved.Model,
		}
		orch.Events.Emit(ctx, e)
	}

	return &SpawnResponse{
		Agent:     task,
		Status:    "Queued",
		Mode:      "queued",
		MissionID: mission.ID,
	}, nil
}

// DryRunSpawn resolves the full spawn chain without side effects.
// It runs ResolveSpawn, builds ExecutionSpec, renders WorkspaceConfig,
// MCP topology, settings, command spec, and payload preview — but creates
// no workspaces, processes, or state entries.
func (c *LocalControlPlaneClient) DryRunSpawn(ctx context.Context, req DryRunRequest) (*DryRunResponse, error) {
	orch, err := c.newOrchestrator()
	if err != nil {
		return nil, err
	}

	task := req.Task
	if task == "" {
		task = "dry-run-probe"
	}

	resolved, err := orch.ResolveSpawn(req.RuntimeType, req.Model, req.Tier, "", "batch")
	if err != nil {
		return nil, fmt.Errorf("resolving spawn: %w", err)
	}

	spec := orchestrator.NewExecutionSpec(resolved, task, "(dry-run contract)", "", orch)

	caps := resolved.Host.Capabilities()
	capsMap := map[string]bool{
		"stream":           caps.Stream,
		"agent_mcp":        caps.AgentMCP,
		"tool_permissions": caps.ToolPermissions,
		"resume_token":     caps.ResumeToken,
	}

	// Build WorkspaceConfig without writing to disk.
	// GatewayURL comes from spec.Topology — no side arguments needed.
	wsCfg := orchestrator.WorkspaceConfigFromSpec(spec, resolved.Host, orch.MCPServers, nil)
	wsCfg.ProjectRoot = c.root

	// Render MCP topology (host-neutral, what .mcp.json is built from).
	var mcpTopology interface{}
	if caps.AgentMCP {
		mcpTopology = agentpolicy.BuildMCPTopology(wsCfg)
	}

	// Render permission tools from the resolved spec (not raw config).
	permTools := make([]string, len(spec.Resolution.AllowedTools))
	copy(permTools, spec.Resolution.AllowedTools)
	if caps.AgentMCP {
		prefix := agentpolicy.MCPPermissionPrefix(wsCfg.GatewayURL)
		serverNames := agentpolicy.ServerNames(wsCfg.Servers)
		wildcards := agentpolicy.BackendWildcards(wsCfg.GatewayURL, serverNames)
		permTools = append(permTools, agentpolicy.ExpandFractaTools(prefix, "", wildcards)...)
	}
	settings := map[string]interface{}{
		"permissions": map[string]interface{}{
			"allow": permTools,
		},
	}

	// Build command spec.
	cmdSpec := resolved.Host.BuildBatchCommand("(dry-run prompt)", resolved.Model, "")

	// Build payload preview.
	payload := spec.ToMissionPayload()

	return &DryRunResponse{
		ResolvedSpec:    spec,
		Capabilities:    capsMap,
		WorkspaceConfig: wsCfg,
		MCPServers:      mcpTopology,
		Settings:        settings,
		CommandSpec:     cmdSpec,
		PayloadPreview:  payload,
	}, nil
}

// Merge integrates an agent's feature branch into the current branch.
func (c *LocalControlPlaneClient) Merge(ctx context.Context, req MergeRequest) (*MergeResponse, error) {
	orch, err := c.newOrchestrator()
	if err != nil {
		return nil, err
	}
	if err := orch.IntegrateBranch(req.Name); err != nil {
		return nil, fmt.Errorf("merge failed: %w", err)
	}
	return &MergeResponse{
		Status:  "merged",
		Message: fmt.Sprintf("Branch feature/%s merged into current branch. Agent %q still alive.", req.Name, req.Name),
	}, nil
}

// ListAgents returns all agents with status and metadata.
// Merges durable fields from Store with ephemeral observability fields from SnapshotStore.
func (c *LocalControlPlaneClient) ListAgents(ctx context.Context, _ ListAgentsRequest) (*ListAgentsResponse, error) {
	st, err := c.cp.Store.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading state: %w", err)
	}

	agents := make([]AgentInfo, 0, len(st.Agents))
	for _, a := range st.Agents {
		mode := a.Mode
		if mode == "" {
			mode = "batch"
		}
		unread, _ := c.cp.Mailbox.UnreadCount(ctx, a.Task)
		info := AgentInfo{
			Name:           a.Task,
			Status:         a.Status,
			Mode:           mode,
			Branch:         a.BranchName,
			ResumeToken:    a.ResumeToken,
			CurrentIntent:  a.CurrentIntent,
			UnreadMessages: unread,
			ObjectiveID:    a.ObjectiveID,
			LastOutput:     a.LastOutput,
			StartTime:      a.StartTime,
		}

		// Merge snapshot observability fields when available.
		if c.snapshotStore != nil {
			if snap := c.snapshotStore.Get(a.Task); snap != nil {
				c.enrichFromSnapshot(&info, snap)
			}
		}

		agents = append(agents, info)
	}

	return &ListAgentsResponse{Agents: agents}, nil
}

// GetAgent returns a single agent's details.
func (c *LocalControlPlaneClient) GetAgent(ctx context.Context, req GetAgentRequest) (*GetAgentResponse, error) {
	agent, err := c.cp.Store.FindAgent(ctx, req.Name)
	if err != nil {
		return nil, fmt.Errorf("looking up agent: %w", err)
	}
	if agent == nil {
		return nil, fmt.Errorf("agent %q not found", req.Name)
	}

	mode := agent.Mode
	if mode == "" {
		mode = "batch"
	}
	unread, _ := c.cp.Mailbox.UnreadCount(ctx, agent.Task)

	info := AgentInfo{
		Name:           agent.Task,
		Status:         agent.Status,
		Mode:           mode,
		Branch:         agent.BranchName,
		ResumeToken:    agent.ResumeToken,
		CurrentIntent:  agent.CurrentIntent,
		UnreadMessages: unread,
		ObjectiveID:    agent.ObjectiveID,
		LastOutput:     agent.LastOutput,
		StartTime:      agent.StartTime,
	}

	if c.snapshotStore != nil {
		if snap := c.snapshotStore.Get(agent.Task); snap != nil {
			c.enrichFromSnapshot(&info, snap)
		}
	}

	return &GetAgentResponse{Agent: info}, nil
}

// enrichFromSnapshot merges ephemeral observability fields from a snapshot into AgentInfo.
func (c *LocalControlPlaneClient) enrichFromSnapshot(info *AgentInfo, snap *events.AgentSnapshot) {
	if !snap.LastHeartbeatAt.IsZero() {
		t := snap.LastHeartbeatAt
		info.LastHeartbeatAt = &t
	}
	if !snap.LastEventAt.IsZero() {
		t := snap.LastEventAt
		info.LastEventAt = &t
	}
	if snap.CurrentPhase != "" {
		info.CurrentPhase = snap.CurrentPhase
	}
	if snap.CurrentTool != "" {
		info.CurrentTool = snap.CurrentTool
	}
	if snap.Backend != "" {
		info.Backend = snap.Backend
	}
	if snap.PodName != "" {
		info.PodName = snap.PodName
	}
	if snap.LastMessageExcerpt != "" {
		info.LastMessageExcerpt = snap.LastMessageExcerpt
	}
}

// GetMission returns mission-level detail for an agent's queued work.
func (c *LocalControlPlaneClient) GetMission(ctx context.Context, req GetMissionRequest) (*GetMissionResponse, error) {
	agent, err := c.cp.Store.FindAgent(ctx, req.Name)
	if err != nil {
		return nil, fmt.Errorf("looking up agent: %w", err)
	}
	if agent == nil {
		return nil, fmt.Errorf("agent %q not found", req.Name)
	}
	if agent.MissionID == 0 {
		return nil, fmt.Errorf("agent %q has no associated mission", req.Name)
	}

	if c.cp.Queue == nil {
		return nil, fmt.Errorf("queue not configured")
	}

	missionStatus, err := c.cp.Queue.Status(ctx, agent.MissionID)
	if err != nil {
		return nil, fmt.Errorf("getting mission status: %w", err)
	}

	return &GetMissionResponse{
		Mission: MissionInfo{
			MissionID:   agent.MissionID,
			AgentTask:   agent.Task,
			Status:      missionStatus,
			ObjectiveID: agent.ObjectiveID,
		},
	}, nil
}

// Peek returns an agent's recent output.
// Modes:
//   - "" (default): SnapshotStore excerpt first, then live stream, then log file.
//   - "events": Recent events from ring buffer as JSON.
//   - "raw": Raw protocol events from orchestrator (backward compat).
func (c *LocalControlPlaneClient) Peek(ctx context.Context, req PeekRequest) (*PeekResponse, error) {
	const maxPeekResponse = 8192

	// Mode: events — return ring buffer contents.
	if req.Mode == "events" {
		if c.eventStore != nil {
			recent := c.eventStore.Recent(req.Name, 20)
			if len(recent) > 0 {
				data, err := json.Marshal(recent)
				if err != nil {
					return nil, fmt.Errorf("marshalling events: %w", err)
				}
				return &PeekResponse{Output: string(data)}, nil
			}
		}
		return &PeekResponse{Output: "[]"}, nil
	}

	// Mode: raw — unchanged behavior.
	if req.Mode == "raw" {
		orch, err := c.newOrchestrator()
		if err != nil {
			return nil, err
		}
		output, err := orch.Peek(req.Name)
		if err != nil {
			return nil, fmt.Errorf("peek failed: %w", err)
		}
		if len(output) > maxPeekResponse {
			output = output[len(output)-maxPeekResponse:]
		}
		return &PeekResponse{Output: output}, nil
	}

	// Default mode: fast path from snapshot excerpt.
	if c.snapshotStore != nil {
		if snap := c.snapshotStore.Get(req.Name); snap != nil && snap.LastMessageExcerpt != "" {
			return &PeekResponse{Output: snap.LastMessageExcerpt}, nil
		}
	}

	// Fall back to live stream handle.
	if c.registry != nil {
		handle := c.registry.Get(req.Name)
		if handle != nil {
			output := handle.RecentOutput(maxPeekResponse)
			if output != "" {
				return &PeekResponse{Output: output}, nil
			}
		}
	}

	// Fall back to log file peek.
	orch, err := c.newOrchestrator()
	if err != nil {
		return nil, err
	}
	output, err := orch.Peek(req.Name)
	if err != nil {
		return nil, fmt.Errorf("peek failed: %w", err)
	}
	if len(output) > maxPeekResponse {
		output = output[len(output)-maxPeekResponse:]
	}
	return &PeekResponse{Output: output}, nil
}

// GetLogs returns recent log output for an agent.
// Delegates to Backend (authoritative log source).
func (c *LocalControlPlaneClient) GetLogs(ctx context.Context, req GetLogsRequest) (*GetLogsResponse, error) {
	if c.cp.Backend == nil {
		return nil, fmt.Errorf("backend not configured")
	}

	lines := req.Lines
	if lines == 0 {
		lines = 100
	}

	output, err := c.cp.Backend.Logs(ctx, req.Task, lines)
	if err != nil {
		return nil, fmt.Errorf("fetching logs: %w", err)
	}

	return &GetLogsResponse{Output: output}, nil
}

// Say sends a follow-up message to an agent.
// Routes through ProcessRegistry for stream agents, Orchestrator for batch resume.
func (c *LocalControlPlaneClient) Say(ctx context.Context, req SayRequest) (*SayResponse, error) {
	orch, err := c.newOrchestrator()
	if err != nil {
		return nil, err
	}

	// Stream path: if a live handle exists, use the streaming say.
	if c.registry != nil {
		handle := c.registry.Get(req.Name)
		if handle != nil {
			if err := orch.SayStreamAsync(req.Name, req.Message, c.registry); err != nil {
				return nil, fmt.Errorf("say failed: %w", err)
			}
			return &SayResponse{
				Status:  "dispatched",
				Message: fmt.Sprintf("Message dispatched to streaming agent %q.", req.Name),
			}, nil
		}
	}

	// Batch resume path: check host capabilities.
	agent, err := orch.Store.FindAgent(ctx, req.Name)
	if err != nil {
		return nil, fmt.Errorf("looking up agent: %w", err)
	}
	if agent == nil {
		return nil, fmt.Errorf("agent %q not found", req.Name)
	}
	h, err := orch.HostRegistry.Get(agent.RuntimeType)
	if err != nil {
		return nil, fmt.Errorf("resolving host: %w", err)
	}
	if !h.Capabilities().ResumeToken {
		return nil, fmt.Errorf("host %q does not support session resumption; use fracta_send for async messaging", agent.RuntimeType)
	}

	if err := orch.SayAsync(req.Name, req.Message); err != nil {
		return nil, fmt.Errorf("say failed: %w", err)
	}
	return &SayResponse{
		Status:  "dispatched",
		Message: fmt.Sprintf("Message dispatched to %q.", req.Name),
	}, nil
}

// Kill terminates an agent and optionally preserves workspace files.
// Closes live stream handle if present, then delegates to Orchestrator.
func (c *LocalControlPlaneClient) Kill(ctx context.Context, req KillRequest) (*KillResponse, error) {
	// Close stream handle first if present.
	if c.registry != nil {
		handle := c.registry.Get(req.Name)
		if handle != nil {
			handle.Close()
			c.registry.Remove(req.Name)
		}
	}

	orch, err := c.newOrchestrator()
	if err != nil {
		return nil, err
	}

	if err := orch.Kill(req.Name, req.KeepFiles); err != nil {
		return nil, fmt.Errorf("kill failed: %w", err)
	}

	return &KillResponse{
		Status:  "killed",
		Message: fmt.Sprintf("Agent %q killed.", req.Name),
	}, nil
}

// CreateObjective creates a new objective for autonomous mission orchestration.
func (c *LocalControlPlaneClient) CreateObjective(ctx context.Context, req CreateObjectiveRequest) (*CreateObjectiveResponse, error) {
	if c.objStore == nil {
		return nil, fmt.Errorf("objective store not configured")
	}

	id := req.ID
	if id == "" {
		id = uuid.New().String()[:8]
	}
	obj := &objective.Objective{
		ID:           id,
		Description:  req.Description,
		Status:       objective.StatusOpen,
		CreatedBy:    "chessmaster",
		MaxMissions:  req.MaxMissions,
		MaxDepth:     req.MaxDepth,
		MaxBranching: req.MaxBranching,
	}
	if req.MaxRuntime != "" {
		d, err := time.ParseDuration(req.MaxRuntime)
		if err != nil {
			return nil, fmt.Errorf("invalid max_runtime %q: %w", req.MaxRuntime, err)
		}
		obj.MaxRuntime = d
	}
	obj.ApplyDefaults()

	if err := c.objStore.Create(ctx, obj); err != nil {
		return nil, fmt.Errorf("create objective failed: %w", err)
	}

	return &CreateObjectiveResponse{
		Objective: objectiveToInfo(obj),
	}, nil
}

// ListObjectives returns objectives filtered by status.
func (c *LocalControlPlaneClient) ListObjectives(ctx context.Context, req ListObjectivesRequest) (*ListObjectivesResponse, error) {
	if c.objStore == nil {
		return nil, fmt.Errorf("objective store not configured")
	}

	var objectives []*objective.Objective
	var err error

	if req.Status != "" {
		status := objective.ObjectiveStatus(req.Status)
		if !status.Valid() {
			return nil, fmt.Errorf("invalid status filter %q", req.Status)
		}
		objectives, err = c.objStore.ListByStatus(ctx, status)
	} else {
		statuses := []objective.ObjectiveStatus{
			objective.StatusOpen, objective.StatusAnswered, objective.StatusDisproven,
			objective.StatusExhausted, objective.StatusBudgetExhausted,
			objective.StatusTimedOut, objective.StatusFrozen,
		}
		for _, st := range statuses {
			objs, e := c.objStore.ListByStatus(ctx, st)
			if e != nil {
				err = e
				break
			}
			objectives = append(objectives, objs...)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("list objectives failed: %w", err)
	}

	infos := make([]ObjectiveInfo, 0, len(objectives))
	for _, o := range objectives {
		infos = append(infos, objectiveToInfo(o))
	}
	return &ListObjectivesResponse{Objectives: infos}, nil
}

// GetObjective returns a single objective's details.
func (c *LocalControlPlaneClient) GetObjective(ctx context.Context, req GetObjectiveRequest) (*GetObjectiveResponse, error) {
	if c.objStore == nil {
		return nil, fmt.Errorf("objective store not configured")
	}

	obj, err := c.objStore.Get(ctx, req.ID)
	if err != nil {
		if errors.Is(err, objective.ErrNotFound) {
			return nil, fmt.Errorf("objective %q not found", req.ID)
		}
		return nil, fmt.Errorf("get objective failed: %w", err)
	}

	return &GetObjectiveResponse{
		Objective: objectiveToInfo(obj),
	}, nil
}

// UnfreezeObjective transitions a frozen objective back to open.
func (c *LocalControlPlaneClient) UnfreezeObjective(ctx context.Context, req UnfreezeObjectiveRequest) (*UnfreezeObjectiveResponse, error) {
	if c.objStore == nil {
		return nil, fmt.Errorf("objective store not configured")
	}

	obj, err := c.objStore.Get(ctx, req.ID)
	if err != nil {
		if errors.Is(err, objective.ErrNotFound) {
			return nil, fmt.Errorf("objective %q not found", req.ID)
		}
		return nil, fmt.Errorf("get objective failed: %w", err)
	}

	if obj.Status != objective.StatusFrozen {
		return nil, fmt.Errorf("cannot unfreeze objective in status %q (must be frozen)", obj.Status)
	}

	obj.Status = objective.StatusOpen
	if err := c.objStore.Update(ctx, obj); err != nil {
		return nil, fmt.Errorf("unfreeze failed: %w", err)
	}

	return &UnfreezeObjectiveResponse{
		Objective: objectiveToInfo(obj),
	}, nil
}

// IngestEvents accepts a batch of events from a remote worker and injects
// them into the CP's event bus. This enables K8s workers to publish events
// via HTTP rather than requiring direct bus access.
func (c *LocalControlPlaneClient) IngestEvents(ctx context.Context, req IngestEventsRequest, task string) (*IngestEventsResponse, error) {
	accepted := 0
	for _, ie := range req.Events {
		eventTime := ie.Time
		if eventTime.IsZero() {
			eventTime = time.Now()
		}
		e := events.Event{
			ID:          ie.EventID,
			Time:        eventTime,
			Component:   ie.Component,
			Category:    ie.Category,
			Resource:    ie.Resource,
			Action:      ie.Action,
			Outcome:     ie.Outcome,
			Severity:    ie.Severity,
			Detail:      ie.Detail,
			Task:        task,
			MissionID:   ie.MissionID,
			ObjectiveID: ie.ObjectiveID,
			Attrs:       ie.Attrs,
		}
		if e.ID == "" {
			e.ID = uuid.NewString()
		}
		if e.Severity == "" {
			e.Severity = "info"
		}
		c.cp.Events.Emit(ctx, e)
		accepted++
	}
	return &IngestEventsResponse{Accepted: accepted, Dropped: 0}, nil
}

// QueryEvents returns recent events for an agent from the ring buffer.
// Supports ?last=N and ?since=event_id query modes.
func (c *LocalControlPlaneClient) QueryEvents(_ context.Context, req EventsQueryRequest) (*EventsQueryResponse, error) {
	if c.eventStore == nil {
		return &EventsQueryResponse{Events: []EventInfo{}}, nil
	}

	var raw []events.Event
	if req.Since != "" {
		raw = c.eventStore.Since(req.Task, req.Since)
		if raw == nil && c.eventReader != nil {
			// Event evicted from ring — fall back to DB.
			n := req.Last
			if n <= 0 {
				n = 20
			}
			dbEvents, err := c.eventReader.EventsSince(context.Background(), req.Task, req.Since, n)
			if err == nil {
				raw = dbEvents
			} else {
				raw = []events.Event{}
			}
		} else if raw == nil {
			raw = []events.Event{}
		}
	} else {
		n := req.Last
		if n <= 0 {
			n = 20
		}
		raw = c.eventStore.Recent(req.Task, n)
		if raw == nil {
			raw = []events.Event{}
		}
	}

	infos := make([]EventInfo, 0, len(raw))
	for _, e := range raw {
		infos = append(infos, EventInfo{
			ID:        e.ID,
			Time:      e.Time,
			Component: e.Component,
			Category:  e.Category,
			Action:    e.Action,
			Outcome:   e.Outcome,
			Severity:  e.Severity,
			Detail:    e.Detail,
			Attrs:     e.Attrs,
		})
	}
	return &EventsQueryResponse{Events: infos}, nil
}

// --- Graph methods (spec-37) ---

// GraphQuery executes a read-only Cypher query against the knowledge graph.
func (c *LocalControlPlaneClient) GraphQuery(ctx context.Context, req GraphQueryRequest) (*GraphQueryResponse, error) {
	if c.graphClient == nil {
		return nil, fmt.Errorf("graph not configured")
	}
	if req.Cypher == "" {
		return nil, &graph.ValidationError{Message: "missing required field: cypher"}
	}
	records, err := c.graphClient.Query(ctx, req.Cypher, req.Params)
	if err != nil {
		return nil, fmt.Errorf("graph query failed: %w", err)
	}
	return &GraphQueryResponse{Records: graph.RecordsToMaps(records)}, nil
}

// GraphUpdate executes a write Cypher query with provenance injection.
func (c *LocalControlPlaneClient) GraphUpdate(ctx context.Context, req GraphUpdateRequest) (*GraphUpdateResponse, error) {
	if c.graphClient == nil {
		return nil, fmt.Errorf("graph not configured")
	}
	if req.Cypher == "" {
		return nil, &graph.ValidationError{Message: "missing required field: cypher"}
	}
	mergedParams, err := graph.InjectProvenance(req.Params, req.Source, req.Confidence, req.CorrelationKey)
	if err != nil {
		return nil, err
	}
	if err := c.graphClient.Update(ctx, req.Cypher, mergedParams); err != nil {
		return nil, fmt.Errorf("graph update failed: %w", err)
	}
	return &GraphUpdateResponse{Status: "ok"}, nil
}

// GraphSchema returns graph schema introspection: labels, relationship types, property keys.
func (c *LocalControlPlaneClient) GraphSchema(ctx context.Context, _ GraphSchemaRequest) (*GraphSchemaResponse, error) {
	if c.graphClient == nil {
		return nil, fmt.Errorf("graph not configured")
	}
	schema, err := graph.QuerySchema(ctx, c.graphClient)
	if err != nil {
		return nil, fmt.Errorf("graph schema failed: %w", err)
	}
	return &GraphSchemaResponse{
		Labels:            schema.Labels,
		RelationshipTypes: schema.RelationshipTypes,
		PropertyKeys:      schema.PropertyKeys,
	}, nil
}

// GraphPath finds the shortest path between two nodes in the knowledge graph.
func (c *LocalControlPlaneClient) GraphPath(ctx context.Context, req GraphPathRequest) (*GraphPathResponse, error) {
	if c.graphClient == nil {
		return nil, fmt.Errorf("graph not configured")
	}
	cypher, params, err := graph.BuildPathQuery(req.FromLabel, req.FromKey, req.FromValue, req.ToLabel, req.ToKey, req.ToValue)
	if err != nil {
		return nil, err
	}
	records, err := c.graphClient.Query(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("graph path query failed: %w", err)
	}
	return &GraphPathResponse{Records: graph.RecordsToMaps(records)}, nil
}

// GraphNeighbors performs a neighborhood traversal from a given node.
func (c *LocalControlPlaneClient) GraphNeighbors(ctx context.Context, req GraphNeighborsRequest) (*GraphNeighborsResponse, error) {
	if c.graphClient == nil {
		return nil, fmt.Errorf("graph not configured")
	}
	cypher, params, err := graph.BuildNeighborsQuery(req.Label, req.Key, req.Value, req.Depth, req.EdgeTypes)
	if err != nil {
		return nil, err
	}
	records, err := c.graphClient.Query(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("graph neighbors query failed: %w", err)
	}
	return &GraphNeighborsResponse{Records: graph.RecordsToMaps(records)}, nil
}

// objectiveToInfo converts an objective domain object to the API info type.
func objectiveToInfo(o *objective.Objective) ObjectiveInfo {
	return ObjectiveInfo{
		ID:           o.ID,
		Description:  o.Description,
		Status:       o.Status,
		CreatedAt:    o.CreatedAt,
		MissionCount: o.MissionCount,
		FindingCount: o.FindingCount,
		MaxMissions:  o.MaxMissions,
		MaxDepth:     o.MaxDepth,
		MaxBranching: o.MaxBranching,
		Outcome:      o.Outcome,
	}
}
