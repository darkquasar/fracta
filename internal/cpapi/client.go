// Package cpapi defines the ControlPlaneClient abstraction for control plane
// capabilities including agent lifecycle, objectives, and graph access. Both
// local (in-process) and remote (HTTP) implementations share this interface,
// ensuring the CLI and admin surfaces work identically regardless of
// deployment topology.
//
// The interface is intent-oriented: it exposes lifecycle verbs (spawn, say,
// kill, merge) and graph operations without leaking queue, reaper, or
// admission internals.
package cpapi

import "context"

// ControlPlaneClient is the shared client boundary for local in-process
// and remote K8s access to lifecycle operations.
//
// Implementations:
//   - LocalControlPlaneClient: composes authoritative domain components directly
//   - RemoteControlPlaneClient: calls the in-cluster control-plane HTTP API
type ControlPlaneClient interface {
	// Spawn creates a new agent via direct or queued dispatch.
	Spawn(ctx context.Context, req SpawnRequest) (*SpawnResponse, error)

	// ListAgents returns all agents with status and metadata.
	ListAgents(ctx context.Context, req ListAgentsRequest) (*ListAgentsResponse, error)

	// GetAgent returns a single agent's details.
	GetAgent(ctx context.Context, req GetAgentRequest) (*GetAgentResponse, error)

	// GetMission returns mission-level detail for an agent's queued work.
	GetMission(ctx context.Context, req GetMissionRequest) (*GetMissionResponse, error)

	// Peek returns an agent's recent output (semantic or raw).
	Peek(ctx context.Context, req PeekRequest) (*PeekResponse, error)

	// GetLogs returns recent log output for an agent.
	GetLogs(ctx context.Context, req GetLogsRequest) (*GetLogsResponse, error)

	// Say sends a follow-up message to an agent (stream or batch resume).
	Say(ctx context.Context, req SayRequest) (*SayResponse, error)

	// Kill terminates an agent and optionally preserves workspace files.
	Kill(ctx context.Context, req KillRequest) (*KillResponse, error)

	// Merge integrates an agent's feature branch into the current branch.
	Merge(ctx context.Context, req MergeRequest) (*MergeResponse, error)

	// DryRunSpawn resolves the full spawn chain without side effects.
	// Returns the resolved spec, rendered workspace config, MCP servers,
	// settings, command spec, and payload preview.
	DryRunSpawn(ctx context.Context, req DryRunRequest) (*DryRunResponse, error)

	// CreateObjective creates a new objective for autonomous mission orchestration.
	CreateObjective(ctx context.Context, req CreateObjectiveRequest) (*CreateObjectiveResponse, error)

	// ListObjectives returns objectives filtered by status.
	ListObjectives(ctx context.Context, req ListObjectivesRequest) (*ListObjectivesResponse, error)

	// GetObjective returns a single objective's details.
	GetObjective(ctx context.Context, req GetObjectiveRequest) (*GetObjectiveResponse, error)

	// UnfreezeObjective transitions a frozen objective back to open.
	UnfreezeObjective(ctx context.Context, req UnfreezeObjectiveRequest) (*UnfreezeObjectiveResponse, error)

	// IngestEvents accepts a batch of events from a worker (K8s remote mode).
	IngestEvents(ctx context.Context, req IngestEventsRequest, task string) (*IngestEventsResponse, error)

	// QueryEvents returns recent events for an agent from the ring buffer.
	QueryEvents(ctx context.Context, req EventsQueryRequest) (*EventsQueryResponse, error)

	// GraphQuery executes a read-only Cypher query against the knowledge graph.
	GraphQuery(ctx context.Context, req GraphQueryRequest) (*GraphQueryResponse, error)

	// GraphUpdate executes a write Cypher query with provenance injection.
	GraphUpdate(ctx context.Context, req GraphUpdateRequest) (*GraphUpdateResponse, error)

	// GraphSchema returns graph schema introspection: labels, relationship types, property keys.
	GraphSchema(ctx context.Context, req GraphSchemaRequest) (*GraphSchemaResponse, error)

	// GraphPath finds the shortest path between two nodes in the knowledge graph.
	GraphPath(ctx context.Context, req GraphPathRequest) (*GraphPathResponse, error)

	// GraphNeighbors performs a neighborhood traversal from a given node.
	GraphNeighbors(ctx context.Context, req GraphNeighborsRequest) (*GraphNeighborsResponse, error)
}
