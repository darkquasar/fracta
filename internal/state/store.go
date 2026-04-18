package state

import (
	"context"
	"errors"
	"time"

	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/mailbox"
	"github.com/darkquasar/fracta/internal/model"
)

// ErrAgentNotClaimable is returned by ClaimAgent when the agent does not exist
// or is not in Queued status. Callers can distinguish this from real DB errors.
var ErrAgentNotClaimable = errors.New("agent not found or not in Queued status")

// EventReader provides read access to persisted agent events.
// Both PostgresStore and SQLiteStore implement this interface.
// Used by the event read API and SSE catch-up path when the in-memory
// ring buffer has evicted the requested events.
type EventReader interface {
	// RecentEvents returns the most recent events for a task, ordered newest-first.
	RecentEvents(ctx context.Context, task string, limit int) ([]events.Event, error)

	// EventsSince returns events for a task after the event with the given
	// UUID event_id, ordered oldest-first (ascending), up to limit rows.
	// The implementation resolves the UUID to the DB row and returns rows after it.
	EventsSince(ctx context.Context, task string, sinceEventID string, limit int) ([]events.Event, error)
}

// Deprecated: EventEmitter is replaced by events.Bus + events.StoreSink.
// New code should emit via events.Bus; StoreSink calls EventInserter.InsertEvent
// for persistence. This interface is retained only for backward compatibility
// with existing tests and will be removed in a future spec.
type EventEmitter interface {
	EmitEvent(ctx context.Context, task, event, detail string) error
}

// Store is the interface for persisting agent and chessmaster state.
//
// Two mutation paths exist:
//   - WithLock: transactional bulk access. Used for spawn admission (TOCTOU guard
//     with concurrency check) and operations that read+modify the full agent list.
//   - RemoveAgent / UpdateAgentStatus / UpdateAgentResult / UpdateAgentIntent:
//     targeted single-row mutations for post-admission hot paths.
//
// Never mix both paths for the same operation. WithLock is for admission;
// targeted methods are for everything after admission.
type Store interface {
	// Load returns the full state snapshot (all agents + chessmaster status).
	Load(ctx context.Context) (model.State, error)

	// WithLock acquires an exclusive lock, loads state, calls fn to mutate it,
	// then persists atomically. Use for spawn admission and bulk operations.
	WithLock(ctx context.Context, fn func(*model.State) error) error

	// FindAgent returns a single agent by task name, or nil if not found.
	FindAgent(ctx context.Context, task string) (*model.AgentEntry, error)

	// RemoveAgent deletes a single agent by task name.
	RemoveAgent(ctx context.Context, task string) error

	// UpdateAgentStatus updates status and last_output for a single agent.
	UpdateAgentStatus(ctx context.Context, task string, status model.AgentStatus, lastOutput string) error

	// UpdateAgentResult updates status, last_output, and resume_token for a single agent.
	// Narrow UPDATE — avoids row-clobber races from whole-row upserts.
	UpdateAgentResult(ctx context.Context, task string, status model.AgentStatus, lastOutput, resumeToken string) error

	// UpdateAgentIntent updates only the current_intent field for a single agent.
	UpdateAgentIntent(ctx context.Context, task, intent string) error

	// ClaimAgent atomically transitions an agent from Queued to Running
	// and resets StartTime to now. Used by workers when they claim a mission.
	// Returns an error if the agent does not exist or is not in Queued status.
	ClaimAgent(ctx context.Context, task string) error

	// UpdateAgentStatusIf conditionally updates status and last_output for a single agent.
	// The update is applied only if the agent's current status is in the expected set.
	// Returns (true, nil) if updated, (false, nil) if status didn't match, or (false, err) on failure.
	UpdateAgentStatusIf(ctx context.Context, task string, expected []model.AgentStatus, newStatus model.AgentStatus, lastOutput string) (bool, error)

	// UpdateAgentResultIf conditionally updates status, last_output, and resume_token.
	// The update is applied only if the agent's current status is in the expected set.
	// Returns (true, nil) if updated, (false, nil) if status didn't match, or (false, err) on failure.
	UpdateAgentResultIf(ctx context.Context, task string, expected []model.AgentStatus, status model.AgentStatus, lastOutput, resumeToken string) (bool, error)

	// UpdateChessmaster updates the chessmaster singleton status without
	// touching the agents table.
	UpdateChessmaster(ctx context.Context, status, lastAction string, updatedAt time.Time) error

	// Mailbox returns the mailbox implementation associated with this store.
	Mailbox() mailbox.Mailbox

	// Close releases store resources (e.g., database connections).
	Close() error
}
