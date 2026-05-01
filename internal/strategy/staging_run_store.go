package strategy

import (
	"context"
	"encoding/json"
	"time"
)

// StagingRunStore persists StagingRun state for restart-resilient async staging.
// Both SQLite and Postgres implementations share this interface.
type StagingRunStore interface {
	// Create persists a new StagingRun. The run must have a unique ID.
	Create(ctx context.Context, run *StagingRun) error

	// Get retrieves a StagingRun by ID. Returns nil, nil if not found.
	Get(ctx context.Context, id string) (*StagingRun, error)

	// UpdateStatus atomically transitions the run to a new status.
	// Returns an error if the transition is invalid.
	UpdateStatus(ctx context.Context, id string, status RunStatus) error

	// UpdateTable updates a single table's state within a run.
	UpdateTable(ctx context.Context, runID, table string, state *TableState) error

	// CASExecute atomically transitions status from "staged" → "executing".
	// Returns true if the caller won the race, false if another caller already claimed it.
	// This is the exactly-once execution guard.
	CASExecute(ctx context.Context, id string) (bool, error)

	// SetResult stores the execution result and trace, transitioning to the given terminal status.
	SetResult(ctx context.Context, id string, status RunStatus, result, trace json.RawMessage) error

	// FailRun atomically sets status to "failed" and persists the structured error.
	FailRun(ctx context.Context, id string, err *StructuredError) error

	// UpdateRecovery persists recovery diagnostic fields (resume_count, recovered_at).
	UpdateRecovery(ctx context.Context, id string, resumeCount int, recoveredAt time.Time) error

	// ListActive returns all runs in non-terminal states (for startup recovery).
	ListActive(ctx context.Context) ([]*StagingRun, error)

	// Reap deletes runs in terminal states older than terminalTTL, and runs
	// stuck in non-terminal states older than staleTTL. Returns count of reaped runs.
	Reap(ctx context.Context, terminalTTL, staleTTL time.Duration) (int, error)
}
