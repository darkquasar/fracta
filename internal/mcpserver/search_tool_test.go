package mcpserver

import (
	"testing"

	"github.com/darkquasar/fracta/internal/gateway"
	"github.com/darkquasar/fracta/internal/graph"
)

func TestBuildCatalogNameSet(t *testing.T) {
	catalog := []gateway.CatalogEntry{
		{ServerName: "elastic", OriginalName: "search"},
		{ServerName: "elastic", OriginalName: "esql"},
		{ServerName: "vendor", OriginalName: "get_alerts"},
	}

	set := buildCatalogNameSet(catalog)

	if !set["elastic.search"] {
		t.Error("expected elastic.search in catalog set")
	}
	if !set["elastic.esql"] {
		t.Error("expected elastic.esql in catalog set")
	}
	if !set["vendor.get_alerts"] {
		t.Error("expected vendor.get_alerts in catalog set")
	}
	if set["elastic.missing"] {
		t.Error("unexpected elastic.missing in catalog set")
	}
}

func TestFilterCallable_RemovesNonCallable(t *testing.T) {
	catalogSet := map[string]bool{
		"elastic.search": true,
		"vendor.alerts":  true,
	}

	tools := []searchToolEntry{
		{Name: "elastic.search", Server: "elastic", MatchType: "graph", Grounded: true},
		{Name: "elastic.stale_tool", Server: "elastic", MatchType: "graph", Grounded: true},
		{Name: "vendor.alerts", Server: "vendor", MatchType: "graph", Grounded: true},
	}

	result := filterCallable(tools, catalogSet)

	if len(result) != 2 {
		t.Fatalf("expected 2 callable tools, got %d", len(result))
	}
	if result[0].Name != "elastic.search" {
		t.Errorf("expected elastic.search, got %s", result[0].Name)
	}
	if result[1].Name != "vendor.alerts" {
		t.Errorf("expected vendor.alerts, got %s", result[1].Name)
	}
}

func TestFilterCallable_EmptyWhenNoneCallable(t *testing.T) {
	catalogSet := map[string]bool{} // empty catalog

	tools := []searchToolEntry{
		{Name: "elastic.search", Server: "elastic"},
		{Name: "vendor.alerts", Server: "vendor"},
	}

	result := filterCallable(tools, catalogSet)

	if len(result) != 0 {
		t.Fatalf("expected 0 callable tools (empty catalog), got %d", len(result))
	}
}

func TestFilterCallable_EmptyInputReturnsEmpty(t *testing.T) {
	catalogSet := map[string]bool{"elastic.search": true}

	result := filterCallable(nil, catalogSet)
	if result != nil {
		t.Fatalf("expected nil for nil input, got %v", result)
	}
}

func TestRecordsToEntriesWithFields_GroupsByTool(t *testing.T) {
	records := []graph.Record{
		{"tool": "elastic.search", "server": "elastic", "description": "Search", "field_name": "src_ip", "field_semantic": "ip_address"},
		{"tool": "elastic.search", "server": "elastic", "description": "Search", "field_name": "timestamp", "field_semantic": "event_time"},
		{"tool": "vendor.alerts", "server": "vendor", "description": "Alerts", "field_name": "severity", "field_semantic": "alert_severity"},
	}

	entries := recordsToEntriesWithFields(records)

	if len(entries) != 2 {
		t.Fatalf("expected 2 tool entries, got %d", len(entries))
	}

	// First tool should have 2 fields
	if entries[0].Name != "elastic.search" {
		t.Errorf("expected elastic.search first, got %s", entries[0].Name)
	}
	if len(entries[0].Fields) != 2 {
		t.Fatalf("expected 2 fields on elastic.search, got %d", len(entries[0].Fields))
	}
	if entries[0].Fields[0].Name != "src_ip" || entries[0].Fields[0].Semantic != "ip_address" {
		t.Errorf("unexpected first field: %+v", entries[0].Fields[0])
	}
	if !entries[0].Grounded {
		t.Error("semantic mode entries should be grounded")
	}
	if entries[0].MatchType != "graph" {
		t.Errorf("expected match_type 'graph', got %s", entries[0].MatchType)
	}

	// Second tool should have 1 field
	if entries[1].Name != "vendor.alerts" {
		t.Errorf("expected vendor.alerts second, got %s", entries[1].Name)
	}
	if len(entries[1].Fields) != 1 {
		t.Fatalf("expected 1 field on vendor.alerts, got %d", len(entries[1].Fields))
	}
}

func TestRecordsToEntriesWithFields_SkipsNilFields(t *testing.T) {
	records := []graph.Record{
		{"tool": "elastic.search", "server": "elastic", "description": "Search", "field_name": nil, "field_semantic": nil},
	}

	entries := recordsToEntriesWithFields(records)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if len(entries[0].Fields) != 0 {
		t.Errorf("expected 0 fields when field_name is nil, got %d", len(entries[0].Fields))
	}
}

func TestRecordsToEntries(t *testing.T) {
	records := []graph.Record{
		{"tool": "elastic.search", "server": "elastic", "description": "Search"},
		{"tool": "vendor.alerts", "server": "vendor", "description": "Alerts"},
	}

	entries := recordsToEntries(records, "keyword", false)

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].MatchType != "keyword" {
		t.Errorf("expected match_type 'keyword', got %s", entries[0].MatchType)
	}
	if entries[0].Grounded {
		t.Error("keyword entries should not be grounded")
	}
}
