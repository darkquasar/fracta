package mcpserver

import (
	"context"
	"testing"

	"github.com/darkquasar/fracta/internal/graph"
)

// queryableGraphClient extends mockGraphClient with configurable Query responses.
type queryableGraphClient struct {
	mockGraphClient
	queryRows []graph.Record
	queryErr  error
}

func (m *queryableGraphClient) Query(_ context.Context, _ string, _ map[string]any) ([]graph.Record, error) {
	return m.queryRows, m.queryErr
}

func TestResolveEffectiveStatus_NoRows(t *testing.T) {
	gc := &queryableGraphClient{queryRows: nil}
	status, err := resolveEffectiveStatus(context.Background(), gc, "test", "1", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != StatusExploratory {
		t.Errorf("status = %q, want %q", status, StatusExploratory)
	}
}

func TestResolveEffectiveStatus_ExploratoryStays(t *testing.T) {
	gc := &queryableGraphClient{
		queryRows: []graph.Record{{
			"status":          "exploratory",
			"total_runs":      int64(2),
			"reliability":     0.5,
			"composite_score": 0.3,
			"last_run":        "",
		}},
	}
	status, err := resolveEffectiveStatus(context.Background(), gc, "test", "1", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != StatusExploratory {
		t.Errorf("status = %q, want %q", status, StatusExploratory)
	}
	// No update should have been written.
	if len(gc.updates) != 0 {
		t.Errorf("expected 0 updates, got %d", len(gc.updates))
	}
}

func TestResolveEffectiveStatus_ExploratoryToValidated(t *testing.T) {
	gc := &queryableGraphClient{
		queryRows: []graph.Record{{
			"status":          "exploratory",
			"total_runs":      int64(5),
			"reliability":     0.85,
			"composite_score": 0.5,
			"last_run":        "",
		}},
	}
	status, err := resolveEffectiveStatus(context.Background(), gc, "test", "1", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != StatusValidated {
		t.Errorf("status = %q, want %q", status, StatusValidated)
	}
	// Should have written an update.
	if len(gc.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(gc.updates))
	}
	if gc.updates[0].params["status"] != StatusValidated {
		t.Errorf("update status = %v", gc.updates[0].params["status"])
	}
}

func TestResolveEffectiveStatus_ValidatedStaysWithoutAutoPromote(t *testing.T) {
	gc := &queryableGraphClient{
		queryRows: []graph.Record{{
			"status":          "validated",
			"total_runs":      int64(25),
			"reliability":     0.98,
			"composite_score": 0.8,
			"last_run":        "",
		}},
	}
	status, err := resolveEffectiveStatus(context.Background(), gc, "test", "1", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != StatusValidated {
		t.Errorf("status = %q, want %q (auto-promote disabled)", status, StatusValidated)
	}
	if len(gc.updates) != 0 {
		t.Errorf("expected 0 updates, got %d", len(gc.updates))
	}
}

func TestResolveEffectiveStatus_ValidatedToPromotedAutoPromote(t *testing.T) {
	gc := &queryableGraphClient{
		queryRows: []graph.Record{{
			"status":          "validated",
			"total_runs":      int64(25),
			"reliability":     0.96,
			"composite_score": 0.75,
			"last_run":        "",
		}},
	}
	status, err := resolveEffectiveStatus(context.Background(), gc, "test", "1", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != StatusPromoted {
		t.Errorf("status = %q, want %q", status, StatusPromoted)
	}
	if len(gc.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(gc.updates))
	}
}

func TestResolveEffectiveStatus_ValidatedNotPromotedBelowThreshold(t *testing.T) {
	gc := &queryableGraphClient{
		queryRows: []graph.Record{{
			"status":          "validated",
			"total_runs":      int64(15), // below 20
			"reliability":     0.96,
			"composite_score": 0.75,
			"last_run":        "",
		}},
	}
	status, err := resolveEffectiveStatus(context.Background(), gc, "test", "1", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != StatusValidated {
		t.Errorf("status = %q, want %q (below run_count threshold)", status, StatusValidated)
	}
}

func TestResolveEffectiveStatus_PromotedDemoted(t *testing.T) {
	gc := &queryableGraphClient{
		queryRows: []graph.Record{{
			"status":          "promoted",
			"total_runs":      int64(30),
			"reliability":     0.65, // below 0.7 threshold
			"composite_score": 0.4,
			"last_run":        "",
		}},
	}
	status, err := resolveEffectiveStatus(context.Background(), gc, "test", "1", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != StatusValidated {
		t.Errorf("status = %q, want %q (auto-demote)", status, StatusValidated)
	}
	if len(gc.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(gc.updates))
	}
}

func TestResolveEffectiveStatus_PromotedStaysReliable(t *testing.T) {
	gc := &queryableGraphClient{
		queryRows: []graph.Record{{
			"status":          "promoted",
			"total_runs":      int64(30),
			"reliability":     0.9,
			"composite_score": 0.7,
			"last_run":        "",
		}},
	}
	status, err := resolveEffectiveStatus(context.Background(), gc, "test", "1", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != StatusPromoted {
		t.Errorf("status = %q, want %q", status, StatusPromoted)
	}
	if len(gc.updates) != 0 {
		t.Errorf("expected 0 updates, got %d", len(gc.updates))
	}
}

func TestResolveEffectiveStatus_DeprecatedToRetired(t *testing.T) {
	gc := &queryableGraphClient{
		queryRows: []graph.Record{{
			"status":          "deprecated",
			"total_runs":      int64(10),
			"reliability":     0.5,
			"composite_score": 0.3,
			"last_run":        "2026-01-01T00:00:00Z", // >30 days ago
		}},
	}
	status, err := resolveEffectiveStatus(context.Background(), gc, "test", "1", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != StatusRetired {
		t.Errorf("status = %q, want %q", status, StatusRetired)
	}
}

func TestResolveEffectiveStatus_DeprecatedStaysRecent(t *testing.T) {
	gc := &queryableGraphClient{
		queryRows: []graph.Record{{
			"status":          "deprecated",
			"total_runs":      int64(10),
			"reliability":     0.5,
			"composite_score": 0.3,
			"last_run":        "2099-01-01T00:00:00Z", // far future = recent
		}},
	}
	status, err := resolveEffectiveStatus(context.Background(), gc, "test", "1", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != StatusDeprecated {
		t.Errorf("status = %q, want %q (recent run)", status, StatusDeprecated)
	}
}

func TestResolveEffectiveStatus_DefaultVersion(t *testing.T) {
	gc := &queryableGraphClient{queryRows: nil}
	// Empty version should default to "1".
	status, err := resolveEffectiveStatus(context.Background(), gc, "test", "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != StatusExploratory {
		t.Errorf("status = %q, want %q", status, StatusExploratory)
	}
}

// --- Helper function tests ---

func TestStringVal(t *testing.T) {
	row := graph.Record{"key": "value", "nil_key": nil}
	if got := stringVal(row, "key", ""); got != "value" {
		t.Errorf("stringVal = %q, want %q", got, "value")
	}
	if got := stringVal(row, "missing", "default"); got != "default" {
		t.Errorf("stringVal missing = %q, want %q", got, "default")
	}
	if got := stringVal(row, "nil_key", "default"); got != "default" {
		t.Errorf("stringVal nil = %q, want %q", got, "default")
	}
	// Non-string value should be formatted.
	row2 := graph.Record{"num": 42}
	if got := stringVal(row2, "num", ""); got != "42" {
		t.Errorf("stringVal int = %q, want %q", got, "42")
	}
}

func TestIntVal(t *testing.T) {
	tests := []struct {
		name     string
		val      interface{}
		expected int
	}{
		{"int", 5, 5},
		{"int64", int64(10), 10},
		{"float64", float64(7), 7},
		{"nil", nil, 99},
		{"string", "bad", 99},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := graph.Record{"k": tt.val}
			if got := intVal(row, "k", 99); got != tt.expected {
				t.Errorf("intVal(%v) = %d, want %d", tt.val, got, tt.expected)
			}
		})
	}
	// Missing key.
	if got := intVal(graph.Record{}, "missing", 42); got != 42 {
		t.Errorf("intVal missing = %d, want 42", got)
	}
}

func TestFloatVal(t *testing.T) {
	tests := []struct {
		name     string
		val      interface{}
		expected float64
	}{
		{"float64", 0.85, 0.85},
		{"int", 3, 3.0},
		{"int64", int64(7), 7.0},
		{"nil", nil, -1.0},
		{"string", "bad", -1.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := graph.Record{"k": tt.val}
			if got := floatVal(row, "k", -1.0); got != tt.expected {
				t.Errorf("floatVal(%v) = %f, want %f", tt.val, got, tt.expected)
			}
		})
	}
}
