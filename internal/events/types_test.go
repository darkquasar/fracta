package events

import (
	"testing"
	"time"
)

func TestInfo_PreFillsFields(t *testing.T) {
	e := Info("orchestrator", "create")
	if e.ID == "" {
		t.Error("ID should be set")
	}
	if e.Time.IsZero() {
		t.Error("Time should be set")
	}
	if e.Severity != "info" {
		t.Errorf("Severity = %q, want info", e.Severity)
	}
	if e.Component != "orchestrator" {
		t.Errorf("Component = %q, want orchestrator", e.Component)
	}
	if e.Action != "create" {
		t.Errorf("Action = %q, want create", e.Action)
	}
	if time.Since(e.Time) > time.Second {
		t.Error("Time should be recent")
	}
}

func TestWarn_IncludesDetail(t *testing.T) {
	e := Warn("gateway", "status_change", "degraded mode")
	if e.Severity != "warn" {
		t.Errorf("Severity = %q, want warn", e.Severity)
	}
	if e.Detail != "degraded mode" {
		t.Errorf("Detail = %q, want 'degraded mode'", e.Detail)
	}
	if e.ID == "" {
		t.Error("ID should be set")
	}
}

func TestError_IncludesDetail(t *testing.T) {
	e := Error("mcpclient", "connect_attempt", "connection refused")
	if e.Severity != "error" {
		t.Errorf("Severity = %q, want error", e.Severity)
	}
	if e.Detail != "connection refused" {
		t.Errorf("Detail = %q, want 'connection refused'", e.Detail)
	}
}

func TestHelpers_UniqueIDs(t *testing.T) {
	e1 := Info("a", "b")
	e2 := Info("a", "b")
	if e1.ID == e2.ID {
		t.Error("two events should have different IDs")
	}
}
