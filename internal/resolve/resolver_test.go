package resolve

import (
	"context"
	"testing"

	"github.com/darkquasar/fracta/internal/contract"
)

type mockGraphQuerier struct{}

func (m *mockGraphQuerier) Query(ctx context.Context, cypher string, params map[string]any) ([]map[string]interface{}, error) {
	return nil, nil
}

func TestResolveFromBinding_TabularTextWarning(t *testing.T) {
	r := NewResolver(&mockGraphQuerier{})

	sb := contract.SourceBinding{
		Backend:   "mcp",
		FetchMode: "mcp_client",
		MCPTool:   "vendor.run_tabular_text",
		MCPServer: "vendor",
	}
	tableSpec := contract.TableSpec{
		Columns: map[string]contract.ColumnSpec{
			"id": {Type: "VARCHAR"},
		},
	}

	tp, warnings, err := r.resolveFromBinding("events", tableSpec, sb, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tp == nil {
		t.Fatal("expected non-nil table plan")
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !contains(warnings[0], "tabular_text") {
		t.Errorf("expected warning mentioning tabular_text, got: %s", warnings[0])
	}
}

func TestResolveFromBinding_NoWarningWithAdapter(t *testing.T) {
	r := NewResolver(&mockGraphQuerier{})

	sb := contract.SourceBinding{
		Backend:         "mcp",
		FetchMode:       "mcp_client",
		MCPTool:         "vendor.run_tabular_text",
		MCPServer:       "vendor",
		ResponseAdapter: "tabular_text",
	}
	tableSpec := contract.TableSpec{
		Columns: map[string]contract.ColumnSpec{
			"id": {Type: "VARCHAR"},
		},
	}

	_, warnings, err := r.resolveFromBinding("events", tableSpec, sb, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings when adapter is set, got: %v", warnings)
	}
}

func TestResolveFromBinding_NoWarningNonTabularText(t *testing.T) {
	r := NewResolver(&mockGraphQuerier{})

	sb := contract.SourceBinding{
		Backend:   "mcp",
		FetchMode: "mcp_client",
		MCPTool:   "elastic.search",
		MCPServer: "elastic",
	}
	tableSpec := contract.TableSpec{
		Columns: map[string]contract.ColumnSpec{
			"id": {Type: "VARCHAR"},
		},
	}

	_, warnings, err := r.resolveFromBinding("events", tableSpec, sb, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for non-tabular_text tool, got: %v", warnings)
	}
}

func TestResolveFromBinding_PropagatesResponseFields(t *testing.T) {
	r := NewResolver(&mockGraphQuerier{})

	sb := contract.SourceBinding{
		Backend:        "mcp",
		FetchMode:      "mcp_client",
		MCPTool:        "some_tool",
		ResponseFormat: "csv",
	}
	tableSpec := contract.TableSpec{
		Columns: map[string]contract.ColumnSpec{
			"id": {Type: "VARCHAR"},
		},
	}

	tp, _, err := r.resolveFromBinding("events", tableSpec, sb, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tp.ResponseFormat != "csv" {
		t.Errorf("expected ResponseFormat=csv, got %q", tp.ResponseFormat)
	}
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
