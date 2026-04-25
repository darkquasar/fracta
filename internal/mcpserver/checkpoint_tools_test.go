package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/schema"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestExpandTemplate(t *testing.T) {
	row := map[string]any{
		"name":   "CloudTrail",
		"tool":   "list_alerts",
		"server": "vendor_mcp",
	}

	got := expandTemplate("MCPTool '{name}' (tool: {tool}, server: {server}) has no fields", row)
	want := "MCPTool 'CloudTrail' (tool: list_alerts, server: vendor_mcp) has no fields"
	if got != want {
		t.Errorf("expandTemplate:\n got: %s\nwant: %s", got, want)
	}
}

func TestExpandTemplate_MissingColumn(t *testing.T) {
	row := map[string]any{"name": "foo"}
	got := expandTemplate("{name} references {missing}", row)
	want := "foo references {missing}"
	if got != want {
		t.Errorf("expandTemplate with missing column:\n got: %s\nwant: %s", got, want)
	}
}

// checkpointGraphClient is a mock that returns canned results for specific queries.
type checkpointGraphClient struct {
	responses map[string][]graph.Record
}

func (c *checkpointGraphClient) Query(_ context.Context, cypher string, _ map[string]any) ([]graph.Record, error) {
	if recs, ok := c.responses[cypher]; ok {
		return recs, nil
	}
	return nil, nil
}

func (c *checkpointGraphClient) Update(_ context.Context, _ string, _ map[string]any) error {
	return nil
}

func (c *checkpointGraphClient) Close() error { return nil }

func TestCheckpointHandler_YAMLRules(t *testing.T) {
	// Set up mock to return gaps for one rule, nothing for others.
	gc := &checkpointGraphClient{
		responses: map[string][]graph.Record{
			// The built-in orphaned semantics query returns nothing.
			"MATCH (n) WHERE n.semantic IS NOT NULL\n\t\t\t WITH DISTINCT n.semantic AS sem\n\t\t\t OPTIONAL MATCH (s:Semantic {name: sem})\n\t\t\t WITH sem WHERE s IS NULL\n\t\t\t RETURN sem": nil,
			// YAML rule 1: one gap
			"MATCH (m:MCPTool) WHERE NOT (m)-[:RETURNS_FIELD]->() RETURN m.name AS name\n": {
				{"name": "vendor_mcp.list_alerts"},
			},
		},
	}

	rules := []schema.CheckpointRule{
		{
			Name:            "test_bare_mcp",
			Layer:           "universal",
			Severity:        "error",
			Query:           "MATCH (m:MCPTool) WHERE NOT (m)-[:RETURNS_FIELD]->() RETURN m.name AS name\n",
			GapType:         "missing_fields",
			GapDescription:  "MCPTool '{name}' has no fields",
			SuggestedAction: "Add fields to '{name}'",
		},
	}

	handler := makeGraphCheckpointHandler(gc, rules)

	// Build a minimal CallToolRequest with no mcp_servers param.
	reqJSON := `{"method":"tools/call","params":{"name":"graph_checkpoint","arguments":{}}}`
	var req mcp.CallToolRequest
	if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	// Parse the result text.
	var cr checkpointResult
	text := result.Content[0].(mcp.TextContent).Text
	if err := json.Unmarshal([]byte(text), &cr); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if cr.AllClear {
		t.Error("expected all_clear=false")
	}
	if cr.GapCount != 1 {
		t.Errorf("gap_count = %d, want 1", cr.GapCount)
	}
	if len(cr.Gaps) != 1 {
		t.Fatalf("len(gaps) = %d, want 1", len(cr.Gaps))
	}

	gap := cr.Gaps[0]
	if gap.Layer != "universal" {
		t.Errorf("gap.Layer = %q, want 'universal'", gap.Layer)
	}
	if gap.Type != "missing_fields" {
		t.Errorf("gap.Type = %q, want 'missing_fields'", gap.Type)
	}
	if gap.Description != "MCPTool 'vendor_mcp.list_alerts' has no fields" {
		t.Errorf("gap.Description = %q", gap.Description)
	}
	if gap.SuggestedAction != "Add fields to 'vendor_mcp.list_alerts'" {
		t.Errorf("gap.SuggestedAction = %q", gap.SuggestedAction)
	}
}

// === F3: Checkpoint authority rule tests ===

// TestCheckpointHandler_InventoryWrongSource verifies that an MCPTool with
// _source='agent:hunter' triggers the inventory_wrong_source authority rule.
func TestCheckpointHandler_InventoryWrongSource(t *testing.T) {
	// The rule query finds MCPTool/MCPServer nodes with _source not in the allowed
	// automated provenance set. Canned response: one MCPTool hit.
	ruleQuery := `MATCH (n) WHERE (n:MCPTool OR n:MCPServer)
AND n._source IS NOT NULL
AND NOT n._source IN ['reconciler:auto', 'gateway:auto', 'migration:v1']
RETURN labels(n)[0] AS label, n.name AS name, n._source AS source`

	gc := &checkpointGraphClient{
		responses: map[string][]graph.Record{
			ruleQuery: {
				{"label": "MCPTool", "name": "custom.hunter_scan", "source": "agent:hunter"},
			},
		},
	}

	rules := []schema.CheckpointRule{
		{
			Name:            "inventory_wrong_source",
			Layer:           "universal",
			Severity:        "error",
			Query:           ruleQuery,
			GapType:         "authority_violation",
			GapDescription:  "{label} '{name}' has _source='{source}' which is not an authorized inventory provenance",
			SuggestedAction: "Investigate why '{name}' was written by '{source}' — only reconciler:auto, gateway:auto, and migration:v1 are valid for inventory nodes",
		},
	}

	handler := makeGraphCheckpointHandler(gc, rules)

	reqJSON := `{"method":"tools/call","params":{"name":"graph_checkpoint","arguments":{}}}`
	var req mcp.CallToolRequest
	if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	var cr checkpointResult
	text := result.Content[0].(mcp.TextContent).Text
	if err := json.Unmarshal([]byte(text), &cr); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if cr.AllClear {
		t.Error("expected all_clear=false for authority violation")
	}
	if cr.GapCount != 1 {
		t.Errorf("gap_count = %d, want 1", cr.GapCount)
	}
	if len(cr.Gaps) != 1 {
		t.Fatalf("len(gaps) = %d, want 1", len(cr.Gaps))
	}

	gap := cr.Gaps[0]
	if gap.Layer != "universal" {
		t.Errorf("gap.Layer = %q, want 'universal'", gap.Layer)
	}
	if gap.Type != "authority_violation" {
		t.Errorf("gap.Type = %q, want 'authority_violation'", gap.Type)
	}
	if gap.Description == "" {
		t.Error("gap.Description should not be empty")
	}
	// Verify template expansion includes the actual node name
	if !containsStr(gap.Description, "custom.hunter_scan") {
		t.Errorf("gap.Description should mention 'custom.hunter_scan', got: %s", gap.Description)
	}
	if !containsStr(gap.Description, "agent:hunter") {
		t.Errorf("gap.Description should mention 'agent:hunter', got: %s", gap.Description)
	}
}

// TestCheckpointHandler_ScaffoldInventorySource verifies that a DomainSource with
// _source='reconciler:auto' triggers the scaffold_inventory_source authority rule.
func TestCheckpointHandler_ScaffoldInventorySource(t *testing.T) {
	// The rule query finds DomainSource/DataStore/MCPField nodes with _source
	// in the automated set (which is wrong for scaffold nodes — they should be
	// authored by agents or users, not auto-created by reconciler/gateway).
	ruleQuery := `MATCH (n) WHERE (n:DomainSource OR n:DataStore OR n:MCPField)
AND n._source IN ['reconciler:auto', 'gateway:auto']
RETURN labels(n)[0] AS label, n.name AS name, n._source AS source`

	gc := &checkpointGraphClient{
		responses: map[string][]graph.Record{
			ruleQuery: {
				{"label": "DomainSource", "name": "AWS CloudTrail", "source": "reconciler:auto"},
			},
		},
	}

	rules := []schema.CheckpointRule{
		{
			Name:            "scaffold_inventory_source",
			Layer:           "universal",
			Severity:        "error",
			Query:           ruleQuery,
			GapType:         "authority_violation",
			GapDescription:  "{label} '{name}' has _source='{source}' — scaffold nodes should not be auto-created by reconciler/gateway",
			SuggestedAction: "DomainSource/DataStore/MCPField nodes should be authored by agents or users, not '{source}'",
		},
	}

	handler := makeGraphCheckpointHandler(gc, rules)

	reqJSON := `{"method":"tools/call","params":{"name":"graph_checkpoint","arguments":{}}}`
	var req mcp.CallToolRequest
	if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	var cr checkpointResult
	text := result.Content[0].(mcp.TextContent).Text
	if err := json.Unmarshal([]byte(text), &cr); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if cr.AllClear {
		t.Error("expected all_clear=false for scaffold authority violation")
	}
	if cr.GapCount != 1 {
		t.Errorf("gap_count = %d, want 1", cr.GapCount)
	}
	if len(cr.Gaps) != 1 {
		t.Fatalf("len(gaps) = %d, want 1", len(cr.Gaps))
	}

	gap := cr.Gaps[0]
	if gap.Layer != "universal" {
		t.Errorf("gap.Layer = %q, want 'universal'", gap.Layer)
	}
	if gap.Type != "authority_violation" {
		t.Errorf("gap.Type = %q, want 'authority_violation'", gap.Type)
	}
	if !containsStr(gap.Description, "DomainSource") {
		t.Errorf("gap.Description should mention 'DomainSource', got: %s", gap.Description)
	}
	if !containsStr(gap.Description, "AWS CloudTrail") {
		t.Errorf("gap.Description should mention 'AWS CloudTrail', got: %s", gap.Description)
	}
	if !containsStr(gap.Description, "reconciler:auto") {
		t.Errorf("gap.Description should mention 'reconciler:auto', got: %s", gap.Description)
	}
}

// containsStr is a simple substring check helper for test assertions.
func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && strings.Contains(s, substr))
}

func TestCheckpointHandler_NoRules_AllClear(t *testing.T) {
	gc := &checkpointGraphClient{responses: map[string][]graph.Record{}}
	handler := makeGraphCheckpointHandler(gc, nil)

	reqJSON := `{"method":"tools/call","params":{"name":"graph_checkpoint","arguments":{}}}`
	var req mcp.CallToolRequest
	json.Unmarshal([]byte(reqJSON), &req)

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	var cr checkpointResult
	text := result.Content[0].(mcp.TextContent).Text
	json.Unmarshal([]byte(text), &cr)

	if !cr.AllClear {
		t.Error("expected all_clear=true when no rules and no built-in gaps")
	}
	if cr.GapCount != 0 {
		t.Errorf("gap_count = %d, want 0", cr.GapCount)
	}
}
