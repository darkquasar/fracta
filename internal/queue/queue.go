package queue

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/darkquasar/fracta/internal/model"
)

// ErrNotFound is returned when a mission does not exist.
var ErrNotFound = errors.New("mission not found")

// Mission status constants.
const (
	StatusPending   = "pending"
	StatusClaimed   = "claimed"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// MissionQueue is the interface for mission queuing backends.
type MissionQueue interface {
	// Enqueue adds a mission to the queue AND persists the agent record.
	// Both backends own agent insertion:
	//   - PostgresQueue: transactional (mission + agent in one Postgres tx)
	//   - MemoryQueue: inserts agent via Store.WithLock, then pushes to channel
	// The caller never persists the agent separately.
	Enqueue(ctx context.Context, m *Mission, agent *model.AgentEntry) error

	// Dequeue blocks until a mission is available, claims it, and returns it.
	// The returned mission transitions to 'claimed'.
	// The caller must Ack or Fail the mission when done.
	// Returns ctx.Err() if the context is cancelled.
	Dequeue(ctx context.Context) (*Mission, error)

	// Ack marks a claimed mission as successfully completed.
	Ack(ctx context.Context, missionID int64) error

	// Fail marks a claimed mission as failed with a reason.
	Fail(ctx context.Context, missionID int64, reason string) error

	// Len returns the number of pending missions in the queue.
	Len(ctx context.Context) (int, error)

	// Status returns the current status of a mission.
	// Returns ("", ErrNotFound) if the mission does not exist.
	Status(ctx context.Context, missionID int64) (string, error)

	// Cancel removes a pending mission from the queue, or marks a claimed
	// mission as cancelled. Returns ErrNotFound if the mission does not exist.
	Cancel(ctx context.Context, missionID int64) error

	// Close releases queue resources.
	Close() error
}

// Mission is the payload that flows through the MissionQueue.
type Mission struct {
	ID        int64           `json:"id"`
	AgentTask string          `json:"agent_task"`
	Payload   json.RawMessage `json:"payload"`
	Status    string          `json:"status"`
	Priority  int             `json:"priority"`
	CreatedAt time.Time       `json:"created_at"`
	ClaimedBy string          `json:"claimed_by,omitempty"`
	ClaimedAt *time.Time      `json:"claimed_at,omitempty"`
	Error     string          `json:"error,omitempty"`

	// DAG fields (spec-16a). All have backward-compat defaults.
	ObjectiveID string `json:"objective_id,omitempty"`
	ParentID    *int64 `json:"parent_id,omitempty"`
	Depth       int    `json:"depth"`
	DedupeKey   string `json:"dedupe_key,omitempty"`
	ProposedBy  string `json:"proposed_by,omitempty"`
}

// MissionPayload carries everything a worker needs to execute the task.
type MissionPayload struct {
	Task         string          `json:"task"`
	Contract     string          `json:"contract"`
	BaseBranch   string          `json:"base_branch"`
	Model        string          `json:"model"`
	Mode         string          `json:"mode,omitempty"`
	RuntimeType  string          `json:"host_type"` // JSON tag kept for wire compat
	AllowedTools []string        `json:"allowed_tools"`
	MCPServers   json.RawMessage `json:"mcp_servers"`
	Backend      string          `json:"backend"`
	ConfigPath   string          `json:"config_path,omitempty"`
	GraphAddr    string          `json:"graph_addr,omitempty"`
	StrategyDir  string          `json:"strategy_dir,omitempty"`
	ConfigHash   string          `json:"config_hash,omitempty"`
	GatewayURL   string          `json:"gateway_url,omitempty"`

	// Objective context (spec-16a). Present when mission belongs to an objective.
	ObjectiveID string `json:"objective_id,omitempty"`
	MissionID   int64  `json:"mission_id,omitempty"`

	// StagedCredentialRefs maps source names to staged credential refs.
	// Workers use these to fetch pre-materialized host-edge credentials
	// via the CredentialStager, then call RehydrateSource to inject them
	// into the credential plan before execution.
	StagedCredentialRefs map[string]string `json:"staged_credential_refs,omitempty"`
}
