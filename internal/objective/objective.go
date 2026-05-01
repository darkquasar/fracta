package objective

import (
	"encoding/json"
	"fmt"
	"time"
)

// ObjectiveStatus represents the lifecycle state of an objective.
type ObjectiveStatus string

const (
	StatusOpen            ObjectiveStatus = "open"
	StatusAnswered        ObjectiveStatus = "answered"
	StatusDisproven       ObjectiveStatus = "disproven"
	StatusExhausted       ObjectiveStatus = "exhausted"
	StatusBudgetExhausted ObjectiveStatus = "budget_exhausted"
	StatusTimedOut        ObjectiveStatus = "timed_out"
	StatusFrozen          ObjectiveStatus = "frozen"
)

// Terminal returns true if the status is a final state (no further transitions).
func (s ObjectiveStatus) Terminal() bool {
	switch s {
	case StatusAnswered, StatusDisproven, StatusExhausted, StatusBudgetExhausted, StatusTimedOut:
		return true
	default:
		return false
	}
}

// Valid returns true if s is a recognized ObjectiveStatus.
func (s ObjectiveStatus) Valid() bool {
	switch s {
	case StatusOpen, StatusAnswered, StatusDisproven, StatusExhausted,
		StatusBudgetExhausted, StatusTimedOut, StatusFrozen:
		return true
	default:
		return false
	}
}

// Default budget caps.
const (
	DefaultMaxMissions  = 100
	DefaultMaxDepth     = 5
	DefaultMaxBranching = 5
	DefaultMaxRuntime   = 4 * time.Hour
)

// Objective is the top-level goal that spawns a tree of missions.
type Objective struct {
	ID          string          `json:"id"`
	Description string          `json:"description"`
	Status      ObjectiveStatus `json:"status"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	CreatedBy   string          `json:"created_by"`

	// Budget caps (enforceable in v1).
	MaxMissions  int           `json:"max_missions"`
	MaxDepth     int           `json:"max_depth"`
	MaxRuntime   time.Duration `json:"max_runtime"`
	MaxBranching int           `json:"max_branching"`

	// Counters.
	MissionCount int `json:"mission_count"`
	FindingCount int `json:"finding_count"`

	// Outcome (set on answered/disproven).
	Outcome     string          `json:"outcome,omitempty"`
	OutcomeData json.RawMessage `json:"outcome_data,omitempty"`
}

// AllowedTransitions defines valid status transitions from each state.
var AllowedTransitions = map[ObjectiveStatus][]ObjectiveStatus{
	StatusOpen:   {StatusAnswered, StatusDisproven, StatusExhausted, StatusBudgetExhausted, StatusTimedOut, StatusFrozen},
	StatusFrozen: {StatusOpen},
}

// CanTransition returns true if moving from current to target is allowed.
func CanTransition(from, to ObjectiveStatus) bool {
	allowed, ok := AllowedTransitions[from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

// ErrInvalidTransition is returned when a status transition is not allowed.
type ErrInvalidTransition struct {
	From ObjectiveStatus
	To   ObjectiveStatus
}

func (e *ErrInvalidTransition) Error() string {
	return fmt.Sprintf("invalid objective transition: %s -> %s", e.From, e.To)
}

// ApplyDefaults fills in zero-value budget caps with defaults.
func (o *Objective) ApplyDefaults() {
	if o.MaxMissions == 0 {
		o.MaxMissions = DefaultMaxMissions
	}
	if o.MaxDepth == 0 {
		o.MaxDepth = DefaultMaxDepth
	}
	if o.MaxBranching == 0 {
		o.MaxBranching = DefaultMaxBranching
	}
	if o.MaxRuntime == 0 {
		o.MaxRuntime = DefaultMaxRuntime
	}
}
