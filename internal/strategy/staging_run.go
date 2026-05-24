package strategy

import (
	"encoding/json"
	"fmt"
	"time"
)

// RunStatus represents the lifecycle state of a StagingRun.
type RunStatus string

const (
	RunStatusCreated   RunStatus = "created"
	RunStatusStaging   RunStatus = "staging"
	RunStatusPending   RunStatus = "pending"
	RunStatusStaged    RunStatus = "staged"
	RunStatusExecuting RunStatus = "executing"
	RunStatusComplete  RunStatus = "complete"
	RunStatusFailed    RunStatus = "failed"
)

// validRunTransitions defines the allowed state transitions for a StagingRun.
var validRunTransitions = map[RunStatus][]RunStatus{
	RunStatusCreated:   {RunStatusStaging, RunStatusPending, RunStatusStaged, RunStatusFailed},
	RunStatusStaging:   {RunStatusPending, RunStatusStaged, RunStatusFailed},
	RunStatusPending:   {RunStatusStaged, RunStatusFailed},
	RunStatusStaged:    {RunStatusExecuting, RunStatusFailed},
	RunStatusExecuting: {RunStatusComplete, RunStatusFailed},
	// Terminal states — no transitions out.
	RunStatusComplete: {},
	RunStatusFailed:   {},
}

// ValidateRunTransition returns an error if the transition from → to is invalid.
func ValidateRunTransition(from, to RunStatus) error {
	allowed, ok := validRunTransitions[from]
	if !ok {
		return fmt.Errorf("unknown run status %q", from)
	}
	for _, a := range allowed {
		if a == to {
			return nil
		}
	}
	return fmt.Errorf("invalid run transition: %s → %s", from, to)
}

// DeriveRunStatus examines all table states in a run and returns the appropriate
// run-level status. Returns "" if the run is still actively staging (no transition).
//
// Rules (highest priority first):
//  1. Any required table failed → RunStatusFailed
//  2. Any table still fetching/pending → "" (still in progress)
//  3. Only awaiting_agent tables remain → RunStatusPending (mixed-mode)
//  4. All tables in terminal states → RunStatusStaged
func DeriveRunStatus(run *StagingRun) RunStatus {
	hasInProgress := false
	hasAwaitingAgent := false
	hasRequiredFailed := false

	for _, ts := range run.Tables {
		if ts.Required && ts.Status == TableStatusFailed {
			hasRequiredFailed = true
		}
		switch ts.Status {
		case TableStatusPending, TableStatusFetching:
			hasInProgress = true
		case TableStatusAwaitingAgent:
			hasAwaitingAgent = true
		}
	}

	if hasRequiredFailed {
		return RunStatusFailed
	}
	if hasInProgress {
		return "" // still actively staging
	}
	if hasAwaitingAgent {
		return RunStatusPending
	}
	return RunStatusStaged
}

// TableStatus represents the lifecycle state of a single table within a StagingRun.
type TableStatus string

const (
	TableStatusPending       TableStatus = "pending"
	TableStatusFetching      TableStatus = "fetching"
	TableStatusStaged        TableStatus = "staged"
	TableStatusFailed        TableStatus = "failed"
	TableStatusSkipped       TableStatus = "skipped"
	TableStatusAwaitingAgent TableStatus = "awaiting_agent"
)

// validTableTransitions defines the allowed state transitions for a table.
var validTableTransitions = map[TableStatus][]TableStatus{
	TableStatusPending:       {TableStatusFetching, TableStatusStaged, TableStatusFailed, TableStatusSkipped, TableStatusAwaitingAgent},
	TableStatusFetching:      {TableStatusStaged, TableStatusFailed},
	TableStatusAwaitingAgent: {TableStatusStaged, TableStatusFailed, TableStatusSkipped},
	// Terminal states.
	TableStatusStaged:  {},
	TableStatusFailed:  {},
	TableStatusSkipped: {},
}

// ValidateTableTransition returns an error if the transition from → to is invalid.
func ValidateTableTransition(from, to TableStatus) error {
	allowed, ok := validTableTransitions[from]
	if !ok {
		return fmt.Errorf("unknown table status %q", from)
	}
	for _, a := range allowed {
		if a == to {
			return nil
		}
	}
	return fmt.Errorf("invalid table transition: %s → %s", from, to)
}

// StagingRun represents the full state of a strategy staging+execution lifecycle.
type StagingRun struct {
	ID                string                 `json:"id"`
	StrategyName      string                 `json:"strategy_name"`
	Params            map[string]any         `json:"params"`
	ParamsFingerprint string                 `json:"params_fingerprint"`
	Status            RunStatus              `json:"status"`
	Tables            map[string]*TableState `json:"tables"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
	Error             *StructuredError       `json:"error,omitempty"`
	Result            json.RawMessage        `json:"result,omitempty"`
	Trace             json.RawMessage        `json:"trace,omitempty"`

	// Diagnostic fields (S10.C)
	ResumeCount        int        `json:"resume_count"`
	RecoveredAt        *time.Time `json:"recovered_at,omitempty"`
	ExecutionClaimedAt *time.Time `json:"execution_claimed_at,omitempty"`
}

// TableState tracks the staging progress of a single table within a run.
type TableState struct {
	Name           string           `json:"name"`
	FetchMode      string           `json:"fetch_mode"` // fracta_mcp_gateway, mcp, native
	Required       bool             `json:"required"`
	Status         TableStatus      `json:"status"`
	Partial        bool             `json:"partial"`      // true if staged with incomplete data
	ParquetPath    string           `json:"parquet_path"` // single file or glob pattern for chunks
	RowCount       int64            `json:"row_count"`
	BytesStaged    int64            `json:"bytes_staged"`
	PagesCompleted int              `json:"pages_completed"`
	TotalEstimate  int              `json:"total_estimate"` // estimated total rows (from max_rows)
	Error          *StructuredError `json:"error,omitempty"`
	FetchPlan      json.RawMessage  `json:"fetch_plan,omitempty"` // serialized for resume after restart
	RetryCount     int              `json:"retry_count"`
	LastOffset     int              `json:"last_offset"` // resume point for offset pagination
	LastCursor     string           `json:"last_cursor"` // resume point for cursor pagination
	StartedAt      *time.Time       `json:"started_at,omitempty"`
	CompletedAt    *time.Time       `json:"completed_at,omitempty"`

	// Diagnostic fields (S10.C)
	LastErrorAt        *time.Time `json:"last_error_at,omitempty"`
	ResumedFromRestart bool       `json:"resumed_from_restart"`
}

// StructuredError provides categorized error information for strategy failures.
type StructuredError struct {
	Message        string `json:"message"`
	Category       string `json:"category"` // transient, permanent, partial
	Retryable      bool   `json:"retryable"`
	RetryAfterSecs int    `json:"retry_after_seconds,omitempty"`
	Phase          string `json:"phase"` // resolution, staging, execution
	Detail         any    `json:"detail,omitempty"`
}

// SerializedFetchPlan captures the resolved fetch options for a table,
// enabling resume from checkpoint after a pod restart.
type SerializedFetchPlan struct {
	Server          string            `json:"server"`
	Tool            string            `json:"tool"`
	Args            map[string]any    `json:"args"`
	Fields          []FetchField      `json:"fields"`
	ItemsPath       string            `json:"items_path,omitempty"`
	SingleItem      bool              `json:"single_item,omitempty"`
	MaxRows         int               `json:"max_rows,omitempty"`
	TimeoutSecs     int               `json:"timeout_secs,omitempty"`
	ResponseFormat  string            `json:"response_format,omitempty"`
	ResponseAdapter string            `json:"response_adapter,omitempty"`
	Pagination      *PaginationConfig `json:"pagination,omitempty"`
}

// FetchField is a minimal field mapping for serialization in FetchPlan.
type FetchField struct {
	Source string `json:"source"`
	Column string `json:"column"`
	Type   string `json:"type"`
}

// PaginationConfig mirrors contract.PaginationConfig for serialization in FetchPlan.
type PaginationConfig struct {
	Mode           string `json:"mode"`
	PageSize       int    `json:"page_size"`
	OffsetParam    string `json:"offset_param,omitempty"`
	LimitParam     string `json:"limit_param,omitempty"`
	CursorParam    string `json:"cursor_param,omitempty"`
	NextCursorPath string `json:"next_cursor_path,omitempty"`
	TotalPath      string `json:"total_path,omitempty"`
}

// IsTerminal returns true if the run is in a terminal state.
func (r *StagingRun) IsTerminal() bool {
	return r.Status == RunStatusComplete || r.Status == RunStatusFailed
}

// TransitionTo validates and applies a status transition on the run.
func (r *StagingRun) TransitionTo(status RunStatus) error {
	if err := ValidateRunTransition(r.Status, status); err != nil {
		return err
	}
	r.Status = status
	r.UpdatedAt = time.Now()
	return nil
}

// TransitionTableTo validates and applies a status transition on a table.
func (ts *TableState) TransitionTo(status TableStatus) error {
	if err := ValidateTableTransition(ts.Status, status); err != nil {
		return err
	}
	ts.Status = status
	return nil
}
