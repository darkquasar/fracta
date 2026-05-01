package strategy

import (
	"testing"
)

func TestValidateRunTransition_Valid(t *testing.T) {
	tests := []struct {
		from, to RunStatus
	}{
		{RunStatusCreated, RunStatusStaging},
		{RunStatusCreated, RunStatusPending},
		{RunStatusCreated, RunStatusStaged},
		{RunStatusCreated, RunStatusFailed},
		{RunStatusStaging, RunStatusPending},
		{RunStatusStaging, RunStatusStaged},
		{RunStatusStaging, RunStatusFailed},
		{RunStatusPending, RunStatusStaged},
		{RunStatusPending, RunStatusFailed},
		{RunStatusStaged, RunStatusExecuting},
		{RunStatusStaged, RunStatusFailed},
		{RunStatusExecuting, RunStatusComplete},
		{RunStatusExecuting, RunStatusFailed},
	}
	for _, tt := range tests {
		if err := ValidateRunTransition(tt.from, tt.to); err != nil {
			t.Errorf("expected valid transition %s → %s, got error: %v", tt.from, tt.to, err)
		}
	}
}

func TestValidateRunTransition_Invalid(t *testing.T) {
	tests := []struct {
		from, to RunStatus
	}{
		{RunStatusComplete, RunStatusFailed},
		{RunStatusComplete, RunStatusExecuting},
		{RunStatusFailed, RunStatusComplete},
		{RunStatusFailed, RunStatusStaging},
		{RunStatusExecuting, RunStatusStaging},
		{RunStatusStaged, RunStatusStaging},
		{RunStatusPending, RunStatusStaging},
		{RunStatusStaging, RunStatusExecuting},
		{RunStatusCreated, RunStatusExecuting},
		{RunStatusCreated, RunStatusComplete},
	}
	for _, tt := range tests {
		if err := ValidateRunTransition(tt.from, tt.to); err == nil {
			t.Errorf("expected invalid transition %s → %s, got nil", tt.from, tt.to)
		}
	}
}

func TestValidateTableTransition_Valid(t *testing.T) {
	tests := []struct {
		from, to TableStatus
	}{
		{TableStatusPending, TableStatusFetching},
		{TableStatusPending, TableStatusStaged},
		{TableStatusPending, TableStatusFailed},
		{TableStatusPending, TableStatusSkipped},
		{TableStatusPending, TableStatusAwaitingAgent},
		{TableStatusFetching, TableStatusStaged},
		{TableStatusFetching, TableStatusFailed},
		{TableStatusAwaitingAgent, TableStatusStaged},
		{TableStatusAwaitingAgent, TableStatusFailed},
		{TableStatusAwaitingAgent, TableStatusSkipped},
	}
	for _, tt := range tests {
		if err := ValidateTableTransition(tt.from, tt.to); err != nil {
			t.Errorf("expected valid transition %s → %s, got error: %v", tt.from, tt.to, err)
		}
	}
}

func TestValidateTableTransition_Invalid(t *testing.T) {
	tests := []struct {
		from, to TableStatus
	}{
		{TableStatusStaged, TableStatusFetching},
		{TableStatusStaged, TableStatusFailed},
		{TableStatusFailed, TableStatusStaged},
		{TableStatusFailed, TableStatusFetching},
		{TableStatusSkipped, TableStatusStaged},
		{TableStatusFetching, TableStatusPending},
		{TableStatusFetching, TableStatusAwaitingAgent},
	}
	for _, tt := range tests {
		if err := ValidateTableTransition(tt.from, tt.to); err == nil {
			t.Errorf("expected invalid transition %s → %s, got nil", tt.from, tt.to)
		}
	}
}

func TestDeriveRunStatus(t *testing.T) {
	tests := []struct {
		name   string
		tables map[string]*TableState
		want   RunStatus
	}{
		{
			name: "all staged → staged",
			tables: map[string]*TableState{
				"t1": {Status: TableStatusStaged, Required: true},
				"t2": {Status: TableStatusStaged, Required: true},
			},
			want: RunStatusStaged,
		},
		{
			name: "one still fetching → empty (in progress)",
			tables: map[string]*TableState{
				"t1": {Status: TableStatusStaged, Required: true},
				"t2": {Status: TableStatusFetching, Required: true},
			},
			want: "",
		},
		{
			name: "one pending → empty (in progress)",
			tables: map[string]*TableState{
				"t1": {Status: TableStatusStaged, Required: true},
				"t2": {Status: TableStatusPending, Required: true},
			},
			want: "",
		},
		{
			name: "required table failed → failed",
			tables: map[string]*TableState{
				"t1": {Status: TableStatusStaged, Required: true},
				"t2": {Status: TableStatusFailed, Required: true},
			},
			want: RunStatusFailed,
		},
		{
			name: "required failed beats in-progress → failed",
			tables: map[string]*TableState{
				"t1": {Status: TableStatusFetching, Required: true},
				"t2": {Status: TableStatusFailed, Required: true},
			},
			want: RunStatusFailed,
		},
		{
			name: "optional failed does not fail run → staged",
			tables: map[string]*TableState{
				"t1": {Status: TableStatusStaged, Required: true},
				"t2": {Status: TableStatusFailed, Required: false},
			},
			want: RunStatusStaged,
		},
		{
			name: "mixed-mode: staged + awaiting_agent → pending",
			tables: map[string]*TableState{
				"auto":   {Status: TableStatusStaged, Required: true, FetchMode: "fracta_mcp_gateway"},
				"manual": {Status: TableStatusAwaitingAgent, Required: true, FetchMode: "mcp"},
			},
			want: RunStatusPending,
		},
		{
			name: "mixed-mode: fetching + awaiting_agent → empty (still in progress)",
			tables: map[string]*TableState{
				"auto":   {Status: TableStatusFetching, Required: true, FetchMode: "fracta_mcp_gateway"},
				"manual": {Status: TableStatusAwaitingAgent, Required: true, FetchMode: "mcp"},
			},
			want: "",
		},
		{
			name: "all awaiting_agent → pending",
			tables: map[string]*TableState{
				"t1": {Status: TableStatusAwaitingAgent, Required: true},
			},
			want: RunStatusPending,
		},
		{
			name: "staged + skipped → staged",
			tables: map[string]*TableState{
				"t1": {Status: TableStatusStaged, Required: true},
				"t2": {Status: TableStatusSkipped, Required: false},
			},
			want: RunStatusStaged,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := &StagingRun{
				Status: RunStatusStaging,
				Tables: tt.tables,
			}
			got := DeriveRunStatus(run)
			if got != tt.want {
				t.Errorf("DeriveRunStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStagingRun_TransitionTo(t *testing.T) {
	run := &StagingRun{Status: RunStatusCreated}

	if err := run.TransitionTo(RunStatusStaging); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != RunStatusStaging {
		t.Fatalf("expected status %s, got %s", RunStatusStaging, run.Status)
	}

	// Invalid transition
	if err := run.TransitionTo(RunStatusComplete); err == nil {
		t.Fatal("expected error for staging → complete")
	}
	// Status should not change on invalid transition
	if run.Status != RunStatusStaging {
		t.Fatalf("status changed on invalid transition: got %s", run.Status)
	}
}

func TestTableState_TransitionTo(t *testing.T) {
	ts := &TableState{Status: TableStatusPending}

	if err := ts.TransitionTo(TableStatusFetching); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts.Status != TableStatusFetching {
		t.Fatalf("expected status %s, got %s", TableStatusFetching, ts.Status)
	}

	// Invalid transition
	if err := ts.TransitionTo(TableStatusPending); err == nil {
		t.Fatal("expected error for fetching → pending")
	}
	if ts.Status != TableStatusFetching {
		t.Fatalf("status changed on invalid transition: got %s", ts.Status)
	}
}

func TestStagingRun_IsTerminal(t *testing.T) {
	tests := []struct {
		status   RunStatus
		terminal bool
	}{
		{RunStatusCreated, false},
		{RunStatusStaging, false},
		{RunStatusPending, false},
		{RunStatusStaged, false},
		{RunStatusExecuting, false},
		{RunStatusComplete, true},
		{RunStatusFailed, true},
	}
	for _, tt := range tests {
		run := &StagingRun{Status: tt.status}
		if got := run.IsTerminal(); got != tt.terminal {
			t.Errorf("IsTerminal(%s) = %v, want %v", tt.status, got, tt.terminal)
		}
	}
}
