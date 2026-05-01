package proposal

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a proposal does not exist.
var ErrNotFound = errors.New("proposal not found")

// ProposalStore persists and retrieves mission proposals.
type ProposalStore interface {
	// Submit inserts a new proposal with status "pending".
	// The proposal ID is set on return.
	Submit(ctx context.Context, p *MissionProposal) error

	// PendingProposals returns all proposals with status "pending",
	// ordered by priority DESC, created_at ASC.
	PendingProposals(ctx context.Context) ([]*MissionProposal, error)

	// Approve transitions a proposal to "approved" and sets reviewed_at.
	Approve(ctx context.Context, id int64) error

	// Reject transitions a proposal to "rejected" with a note and sets reviewed_at.
	Reject(ctx context.Context, id int64, note string) error

	// UpdateStatus sets an arbitrary status (e.g., "dedupe_hit").
	UpdateStatus(ctx context.Context, id int64, status string) error

	// PendingForObjective returns all pending proposals for a specific objective,
	// ordered by priority DESC, created_at ASC.
	PendingForObjective(ctx context.Context, objectiveID string) ([]*MissionProposal, error)

	// RejectAllPending rejects all pending proposals for a specific objective
	// with the given note. Returns the number of proposals rejected.
	RejectAllPending(ctx context.Context, objectiveID string, note string) (int, error)
}
