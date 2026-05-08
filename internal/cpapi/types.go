package cpapi

import (
	"time"

	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/objective"
)

// SpawnRequest describes a lifecycle intent to create a new agent.
// Fields are intent-oriented — no queue/reaper/admission internals.
type SpawnRequest struct {
	// Task is the unique agent identifier (alphanumeric, hyphens, underscores).
	Task string `json:"task"`

	// Contract is the task instructions (inline text or file contents).
	Contract string `json:"contract,omitempty"`

	// BaseBranch is the git branch to create the workspace from.
	// Empty = config default or current branch.
	BaseBranch string `json:"base_branch,omitempty"`

	// Model overrides the default model ID for this spawn.
	Model string `json:"model,omitempty"`

	// Tier selects a model via config model_tiers. Ignored when Model is set.
	Tier string `json:"tier,omitempty"`

	// RuntimeType selects the runtime implementation (e.g. "claude").
	// Empty = registry default.
	RuntimeType string `json:"host_type,omitempty"` // JSON tag kept for wire compat

	// Mode is the execution mode: "batch" (default) or "stream".
	Mode string `json:"mode,omitempty"`

	// Dispatch is the dispatch intent: "direct" (default) or "queued".
	Dispatch string `json:"dispatch,omitempty"`

	// ObjectiveID links this spawn as a root mission under an objective.
	// Requires queued dispatch.
	ObjectiveID string `json:"objective_id,omitempty"`

	// StagedCredentialRefs maps source names to staged credential refs.
	// Populated by StagingSpawnClient when host-edge sources are pre-materialized
	// and need to cross the host→worker boundary via the staging API.
	StagedCredentialRefs map[string]string `json:"staged_credential_refs,omitempty"`
}

// SpawnResponse is returned after a successful spawn.
type SpawnResponse struct {
	Agent     string `json:"agent"`
	Branch    string `json:"branch,omitempty"`
	Worktree  string `json:"worktree,omitempty"`
	Status    string `json:"status"`
	Mode      string `json:"mode"`
	MissionID int64  `json:"mission_id,omitempty"`
}

// ListAgentsRequest controls agent listing.
type ListAgentsRequest struct {
	// No filters in v1. Reserved for future status/mode filtering.
}

// AgentInfo is the per-agent detail returned in list and get responses.
type AgentInfo struct {
	Name           string            `json:"name"`
	Status         model.AgentStatus `json:"status"`
	Mode           string            `json:"mode"`
	Branch         string            `json:"branch,omitempty"`
	ResumeToken    string            `json:"resume_token,omitempty"`
	CurrentIntent  string            `json:"current_intent,omitempty"`
	UnreadMessages int               `json:"unread_messages"`
	ObjectiveID    string            `json:"objective_id,omitempty"`
	LastOutput     string            `json:"last_output,omitempty"`
	StartTime      time.Time         `json:"start_time,omitempty"`

	// Observability fields (populated from SnapshotStore when available).
	LastHeartbeatAt    *time.Time `json:"last_heartbeat_at,omitempty"`
	LastEventAt        *time.Time `json:"last_event_at,omitempty"`
	CurrentPhase       string     `json:"current_phase,omitempty"`
	CurrentTool        string     `json:"current_tool,omitempty"`
	Backend            string     `json:"backend,omitempty"`
	PodName            string     `json:"pod_name,omitempty"`
	LastMessageExcerpt string     `json:"last_message_excerpt,omitempty"`
}

// ListAgentsResponse contains the list of agents.
type ListAgentsResponse struct {
	Agents []AgentInfo `json:"agents"`
}

// GetAgentRequest identifies a single agent.
type GetAgentRequest struct {
	Name string `json:"name"`
}

// GetAgentResponse contains a single agent's details.
type GetAgentResponse struct {
	Agent AgentInfo `json:"agent"`
}

// GetMissionRequest identifies a mission by agent task name.
type GetMissionRequest struct {
	Name string `json:"name"`
}

// MissionInfo contains mission/queue-level detail without exposing internals.
type MissionInfo struct {
	MissionID   int64  `json:"mission_id"`
	AgentTask   string `json:"agent_task"`
	Status      string `json:"status"`
	ObjectiveID string `json:"objective_id,omitempty"`
}

// GetMissionResponse contains mission details.
type GetMissionResponse struct {
	Mission MissionInfo `json:"mission"`
}

// PeekRequest describes a request to read an agent's recent output.
type PeekRequest struct {
	Name string `json:"name"`
	// Mode controls output format: empty for semantic output, "raw" for protocol events.
	Mode string `json:"mode,omitempty"`
}

// PeekResponse contains the agent's recent output.
type PeekResponse struct {
	Output string `json:"output"`
}

// GetLogsRequest describes a request for agent logs.
type GetLogsRequest struct {
	Task  string `json:"task"`
	Lines int    `json:"lines,omitempty"` // 0 = default (100)
}

// GetLogsResponse contains log output.
type GetLogsResponse struct {
	Output string `json:"output"`
}

// SayRequest describes a follow-up message to an agent.
type SayRequest struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

// SayResponse is returned after dispatching a say.
type SayResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// KillRequest describes a request to terminate an agent.
type KillRequest struct {
	Name      string `json:"name"`
	KeepFiles bool   `json:"keep_files,omitempty"`
}

// KillResponse is returned after a kill.
type KillResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// MergeRequest describes a request to merge an agent's branch.
type MergeRequest struct {
	Name string `json:"name"`
}

// MergeResponse is returned after a merge.
type MergeResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// CreateObjectiveRequest describes a new objective.
type CreateObjectiveRequest struct {
	// ID is an optional caller-chosen objective ID. Auto-generated if empty.
	ID           string `json:"id,omitempty"`
	Description  string `json:"description"`
	MaxMissions  int    `json:"max_missions,omitempty"`
	MaxDepth     int    `json:"max_depth,omitempty"`
	MaxBranching int    `json:"max_branching,omitempty"`
	// MaxRuntime is a duration string (e.g. "4h"). Parsed server-side.
	MaxRuntime   string `json:"max_runtime,omitempty"`
}

// CreateObjectiveResponse is returned after creating an objective.
type CreateObjectiveResponse struct {
	Objective ObjectiveInfo `json:"objective"`
}

// ListObjectivesRequest controls objective listing.
type ListObjectivesRequest struct {
	// Status filters by objective status. Empty = list all open.
	Status string `json:"status,omitempty"`
}

// ObjectiveInfo is the per-objective detail returned in responses.
type ObjectiveInfo struct {
	ID           string                   `json:"id"`
	Description  string                   `json:"description"`
	Status       objective.ObjectiveStatus `json:"status"`
	CreatedAt    time.Time                `json:"created_at"`
	MissionCount int                      `json:"mission_count"`
	FindingCount int                      `json:"finding_count"`
	MaxMissions  int                      `json:"max_missions"`
	MaxDepth     int                      `json:"max_depth"`
	MaxBranching int                      `json:"max_branching"`
	Outcome      string                   `json:"outcome,omitempty"`
}

// ListObjectivesResponse contains the list of objectives.
type ListObjectivesResponse struct {
	Objectives []ObjectiveInfo `json:"objectives"`
}

// GetObjectiveRequest identifies a single objective.
type GetObjectiveRequest struct {
	ID string `json:"id"`
}

// GetObjectiveResponse contains a single objective's details.
type GetObjectiveResponse struct {
	Objective ObjectiveInfo `json:"objective"`
}

// UnfreezeObjectiveRequest identifies an objective to unfreeze.
type UnfreezeObjectiveRequest struct {
	ID string `json:"id"`
}

// UnfreezeObjectiveResponse is returned after unfreezing an objective.
type UnfreezeObjectiveResponse struct {
	Objective ObjectiveInfo `json:"objective"`
}

// DryRunRequest describes a dry-run spawn that resolves the full execution
// chain without creating any agents, workspaces, or processes.
type DryRunRequest struct {
	// Task is a hypothetical task name for rendering (default: "dry-run-probe").
	Task string `json:"task,omitempty"`

	// RuntimeType selects the runtime implementation (e.g. "claude").
	RuntimeType string `json:"host_type,omitempty"` // JSON tag kept for wire compat

	// Model overrides the default model ID.
	Model string `json:"model,omitempty"`

	// Tier selects a model via config model_tiers. Ignored when Model is set.
	Tier string `json:"tier,omitempty"`

	// Format controls the output format: "yaml" (default) or "json".
	Format string `json:"format,omitempty"`
}

// DryRunResponse contains the fully-resolved spawn chain for inspection.
type DryRunResponse struct {
	// ResolvedSpec is the canonical ExecutionSpec that would be constructed.
	ResolvedSpec interface{} `json:"resolved_spec"`

	// Capabilities describes the resolved host's capability flags.
	Capabilities interface{} `json:"capabilities"`

	// WorkspaceConfig is the rendered host.WorkspaceConfig.
	WorkspaceConfig interface{} `json:"workspace_config"`

	// MCPServers is the rendered .mcp.json content.
	MCPServers interface{} `json:"mcp_servers"`

	// Settings is the rendered settings.json content (permissions).
	Settings interface{} `json:"settings"`

	// CommandSpec is the binary + args that would execute.
	CommandSpec interface{} `json:"command_spec"`

	// PayloadPreview is the MissionPayload that would be enqueued (queued dispatch).
	PayloadPreview interface{} `json:"payload_preview"`
}

// StageCredentialRequest is the body for POST /api/v1/credentials/stage.
type StageCredentialRequest struct {
	SourceName string `json:"source_name"` // declared source identity
	Data       string `json:"data"`        // base64-encoded single credential blob
	MountPath  string `json:"mount_path"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"` // default: 300
}

// StageCredentialResponse is returned after staging a credential.
type StageCredentialResponse struct {
	Ref string `json:"ref"`
}

// IngestEventsRequest is the body for POST /api/v1/agents/{task}/events.
// Workers batch events and POST them to the CP for fan-out to sinks.
type IngestEventsRequest struct {
	Events []IngestEvent `json:"events"`
}

// IngestEvent is a single event in an ingest batch. It mirrors the fields
// of events.Event that are relevant for wire transport.
type IngestEvent struct {
	EventID     string            `json:"event_id,omitempty"`
	Time        time.Time         `json:"time,omitempty"`
	Component   string            `json:"component"`
	Category    string            `json:"category"`
	Resource    string            `json:"resource,omitempty"`
	Action      string            `json:"action"`
	Outcome     string            `json:"outcome,omitempty"`
	Severity    string            `json:"severity,omitempty"`
	Detail      string            `json:"detail,omitempty"`
	Task        string            `json:"task,omitempty"`
	MissionID   int64             `json:"mission_id,omitempty"`
	ObjectiveID string            `json:"objective_id,omitempty"`
	Attrs       map[string]string `json:"attrs,omitempty"`
}

// IngestEventsResponse is returned after ingesting events.
type IngestEventsResponse struct {
	Accepted int `json:"accepted"`
	Dropped  int `json:"dropped"`
}

// EventsQueryRequest is the query params for GET /api/v1/agents/{task}/events.
type EventsQueryRequest struct {
	Task  string `json:"task"`
	Last  int    `json:"last,omitempty"`  // default 20
	Since string `json:"since,omitempty"` // event_id cursor
}

// EventsQueryResponse returns recent events for an agent.
type EventsQueryResponse struct {
	Events []EventInfo `json:"events"`
}

// EventInfo is a single event in query responses.
type EventInfo struct {
	ID        string            `json:"id"`
	Time      time.Time         `json:"time"`
	Component string            `json:"component"`
	Category  string            `json:"category"`
	Action    string            `json:"action"`
	Outcome   string            `json:"outcome,omitempty"`
	Severity  string            `json:"severity,omitempty"`
	Detail    string            `json:"detail,omitempty"`
	Attrs     map[string]string `json:"attrs,omitempty"`
}

// --- Graph types (spec-37) ---

// GraphQueryRequest is the body for POST /api/v1/graph/query.
type GraphQueryRequest struct {
	Cypher string         `json:"cypher"`
	Params map[string]any `json:"params,omitempty"`
}

// GraphQueryResponse contains the query result records.
type GraphQueryResponse struct {
	Records []map[string]any `json:"records"`
}

// GraphUpdateRequest is the body for POST /api/v1/graph/update.
type GraphUpdateRequest struct {
	Cypher         string         `json:"cypher"`
	Params         map[string]any `json:"params,omitempty"`
	Source         string         `json:"source,omitempty"`
	Confidence     string         `json:"confidence,omitempty"`
	CorrelationKey string         `json:"correlation_key,omitempty"`
}

// GraphUpdateResponse is returned after a graph update.
type GraphUpdateResponse struct {
	Status string `json:"status"` // "ok"
}

// GraphSchemaRequest is the body for POST /api/v1/graph/schema.
// Empty for interface consistency — every ControlPlaneClient method takes a request struct.
type GraphSchemaRequest struct{}

// GraphSchemaResponse contains graph schema introspection results.
type GraphSchemaResponse struct {
	Labels            []string `json:"labels"`
	RelationshipTypes []string `json:"relationship_types"`
	PropertyKeys      []string `json:"property_keys"`
}

// GraphPathRequest is the body for POST /api/v1/graph/path.
type GraphPathRequest struct {
	FromLabel string `json:"from_label"`
	FromKey   string `json:"from_key"`
	FromValue string `json:"from_value"`
	ToLabel   string `json:"to_label"`
	ToKey     string `json:"to_key"`
	ToValue   string `json:"to_value"`
}

// GraphPathResponse contains shortest-path result records.
type GraphPathResponse struct {
	Records []map[string]any `json:"records"`
}

// GraphNeighborsRequest is the body for POST /api/v1/graph/neighbors.
type GraphNeighborsRequest struct {
	Label     string   `json:"label"`
	Key       string   `json:"key"`
	Value     string   `json:"value"`
	Depth     int      `json:"depth,omitempty"`
	EdgeTypes []string `json:"edge_types,omitempty"`
}

// GraphNeighborsResponse contains neighborhood traversal result records.
type GraphNeighborsResponse struct {
	Records []map[string]any `json:"records"`
}
