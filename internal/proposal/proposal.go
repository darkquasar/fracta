package proposal

import (
	"encoding/json"
	"time"
)

// Proposal status constants.
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRejected = "rejected"
	StatusDedupeHit = "dedupe_hit"
)

// MissionProposal represents an agent's request to spawn a child mission.
type MissionProposal struct {
	ID            int64           `json:"id"`
	ObjectiveID   string          `json:"objective_id"`
	ParentMission int64           `json:"parent_mission"`
	ProposedBy    string          `json:"proposed_by"`
	Task          string          `json:"task"`
	Contract      string          `json:"contract"`
	Priority      int             `json:"priority"`
	DedupeKey     string          `json:"dedupe_key"`
	Rationale     string          `json:"rationale"`
	Evidence      json.RawMessage `json:"evidence,omitempty"`
	Status        string          `json:"status"`
	CreatedAt     time.Time       `json:"created_at"`
	ReviewedAt    *time.Time      `json:"reviewed_at,omitempty"`
	RejectionNote string          `json:"rejection_note,omitempty"`
}
