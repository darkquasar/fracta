package objective

import (
	"testing"
	"time"
)

func TestObjectiveStatus_Terminal(t *testing.T) {
	tests := []struct {
		status   ObjectiveStatus
		terminal bool
	}{
		{StatusOpen, false},
		{StatusFrozen, false},
		{StatusAnswered, true},
		{StatusDisproven, true},
		{StatusExhausted, true},
		{StatusBudgetExhausted, true},
		{StatusTimedOut, true},
	}
	for _, tt := range tests {
		if got := tt.status.Terminal(); got != tt.terminal {
			t.Errorf("(%q).Terminal() = %v, want %v", tt.status, got, tt.terminal)
		}
	}
}

func TestObjectiveStatus_Valid(t *testing.T) {
	if !StatusOpen.Valid() {
		t.Error("StatusOpen should be valid")
	}
	if ObjectiveStatus("bogus").Valid() {
		t.Error("bogus status should not be valid")
	}
}

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from, to ObjectiveStatus
		ok       bool
	}{
		{StatusOpen, StatusAnswered, true},
		{StatusOpen, StatusDisproven, true},
		{StatusOpen, StatusExhausted, true},
		{StatusOpen, StatusBudgetExhausted, true},
		{StatusOpen, StatusTimedOut, true},
		{StatusOpen, StatusFrozen, true},
		{StatusFrozen, StatusOpen, true},
		{StatusAnswered, StatusOpen, false},
		{StatusExhausted, StatusOpen, false},
		{StatusFrozen, StatusAnswered, false},
	}
	for _, tt := range tests {
		if got := CanTransition(tt.from, tt.to); got != tt.ok {
			t.Errorf("CanTransition(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.ok)
		}
	}
}

func TestApplyDefaults(t *testing.T) {
	o := &Objective{}
	o.ApplyDefaults()
	if o.MaxMissions != DefaultMaxMissions {
		t.Errorf("MaxMissions = %d, want %d", o.MaxMissions, DefaultMaxMissions)
	}
	if o.MaxDepth != DefaultMaxDepth {
		t.Errorf("MaxDepth = %d, want %d", o.MaxDepth, DefaultMaxDepth)
	}
	if o.MaxBranching != DefaultMaxBranching {
		t.Errorf("MaxBranching = %d, want %d", o.MaxBranching, DefaultMaxBranching)
	}
	if o.MaxRuntime != DefaultMaxRuntime {
		t.Errorf("MaxRuntime = %v, want %v", o.MaxRuntime, DefaultMaxRuntime)
	}

	// Non-zero values should not be overwritten.
	o2 := &Objective{MaxMissions: 10, MaxDepth: 3, MaxBranching: 2, MaxRuntime: time.Hour}
	o2.ApplyDefaults()
	if o2.MaxMissions != 10 {
		t.Errorf("MaxMissions should stay 10, got %d", o2.MaxMissions)
	}
}
