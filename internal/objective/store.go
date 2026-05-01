package objective

import (
	"context"
	"errors"
)

// ErrNotFound is returned when an objective does not exist.
var ErrNotFound = errors.New("objective not found")

// ObjectiveStore persists and retrieves objectives.
type ObjectiveStore interface {
	// Create inserts a new objective. The objective must have a non-empty ID.
	Create(ctx context.Context, o *Objective) error

	// Get returns an objective by ID. Returns ErrNotFound if it doesn't exist.
	Get(ctx context.Context, id string) (*Objective, error)

	// Update persists changes to an existing objective (status, outcome, counters).
	// Returns ErrNotFound if the objective doesn't exist.
	Update(ctx context.Context, o *Objective) error

	// IncrementMissionCount atomically increments mission_count by 1.
	IncrementMissionCount(ctx context.Context, id string) error

	// IncrementFindingCount atomically increments finding_count by 1.
	IncrementFindingCount(ctx context.Context, id string) error

	// ListByStatus returns all objectives with the given status.
	ListByStatus(ctx context.Context, status ObjectiveStatus) ([]*Objective, error)
}
