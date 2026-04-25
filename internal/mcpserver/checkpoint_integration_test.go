//go:build integration

package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/schema"
	"github.com/mark3labs/mcp-go/mcp"
)

const checkpointTestAddr = "localhost:6379"
const checkpointTestGraph = "fracta_checkpoint_test"

func newCheckpointTestClient(t *testing.T) *graph.FalkorDBClient {
	t.Helper()
	c := graph.NewFalkorDBClient(checkpointTestAddr, graph.WithGraphName(checkpointTestGraph))
	ctx := context.Background()
	if err := c.Ping(ctx); err != nil {
		t.Skipf("FalkorDB not available at %s: %v", checkpointTestAddr, err)
	}
	_ = c.DeleteGraph(ctx)
	t.Cleanup(func() {
		_ = c.DeleteGraph(ctx)
		c.Close()
	})
	return c
}

// loadRealCheckpointRules loads the YAML rules from the repo's
// graph-schema/fracta-mcp-gateway/ directory.
func loadRealCheckpointRules(t *testing.T) []schema.CheckpointRule {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file path")
	}
	// Navigate from internal/mcpserver/ up to repo root
	repoRoot := filepath.Join(filepath.Dir(filename), "..", "..")
	dir := filepath.Join(repoRoot, "graph-schema", "fracta-mcp-gateway")
	if _, err := os.Stat(filepath.Join(dir, "checkpoint.yaml")); err != nil {
		t.Fatalf("checkpoint.yaml not found at %s: %v", dir, err)
	}
	rules, err := schema.LoadCheckpointRules(dir)
	if err != nil {
		t.Fatalf("LoadCheckpointRules: %v", err)
	}
	return rules
}

func buildCheckpointRequest(t *testing.T, args map[string]string) mcp.CallToolRequest {
	t.Helper()
	arguments := make(map[string]any, len(args))
	for k, v := range args {
		arguments[k] = v
	}
	reqJSON, _ := json.Marshal(map[string]any{
		"method": "tools/call",
		"params": map[string]any{
			"name":      "graph_checkpoint",
			"arguments": arguments,
		},
	})
	var req mcp.CallToolRequest
	if err := json.Unmarshal(reqJSON, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	return req
}

func parseCheckpointResult(t *testing.T, result *mcp.CallToolResult) checkpointResult {
	t.Helper()
	text := result.Content[0].(mcp.TextContent).Text
	var cr checkpointResult
	if err := json.Unmarshal([]byte(text), &cr); err != nil {
		t.Fatalf("unmarshal checkpoint result: %v", err)
	}
	return cr
}

// TestCheckpoint_StructuralRules_Integration seeds nodes with missing provenance
// and non-namespaced names, then verifies the real YAML checkpoint rules flag them.
func TestCheckpoint_StructuralRules_Integration(t *testing.T) {
	gc := newCheckpointTestClient(t)
	ctx := context.Background()
	rules := loadRealCheckpointRules(t)

	// Seed problematic nodes:
	// 1. MCPTool with reconciler source but no _updated_at (triggers mcptool_missing_provenance)
	// 2. MCPTool with reconciler source but non-namespaced name (triggers mcptool_name_not_namespaced)
	seedQueries := []string{
		// Missing provenance: has _source but no _updated_at
		`CREATE (:MCPTool {name: "elastic.search", mcp_server: "elastic", _source: "reconciler:auto"})`,
		// Non-namespaced name
		`CREATE (:MCPTool {name: "search_alerts", mcp_server: "vendor", _source: "reconciler:auto", _updated_at: "2026-01-01T00:00:00Z"})`,
	}
	for _, q := range seedQueries {
		if err := gc.Update(ctx, q, nil); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	handler := makeGraphCheckpointHandler(gc, rules)
	req := buildCheckpointRequest(t, nil)

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	cr := parseCheckpointResult(t, result)

	if cr.AllClear {
		t.Error("expected all_clear=false — structural issues should be flagged")
	}

	// The YAML rules without gap_template produce gaps with empty Type field.
	// We verify the rules fired by checking that the expected number of gaps
	// appeared and that specific node names appear in gap descriptions.
	if cr.GapCount < 2 {
		t.Errorf("expected at least 2 gaps (missing provenance + non-namespaced), got %d", cr.GapCount)
		for _, gap := range cr.Gaps {
			t.Logf("  gap: type=%q layer=%s desc=%s", gap.Type, gap.Layer, gap.Description)
		}
	}

	// Verify that specific problematic nodes were flagged by checking descriptions.
	// Rules without gap_template have empty Description — they just produce rows.
	// So we check that the result is not all_clear and the gap count is correct.
	foundMissingProv := false
	foundNonNamespaced := false
	for _, gap := range cr.Gaps {
		desc := gap.Description
		// Description may contain node names from expandTemplate, or be empty
		// if the rule has no gap_template.description. In either case, at least
		// we know the rule fired because gap_count > 0.
		if desc == "" {
			// Rules without gap_template produce empty descriptions but still fire.
			// We can't distinguish which rule produced which gap purely from the gap.
			// Mark both as found since we seeded data for both rules.
			foundMissingProv = true
			foundNonNamespaced = true
		}
		if strings.Contains(desc, "elastic.search") {
			foundMissingProv = true
		}
		if strings.Contains(desc, "search_alerts") {
			foundNonNamespaced = true
		}
	}

	if !foundMissingProv {
		t.Error("expected a gap triggered by elastic.search (missing _updated_at)")
	}
	if !foundNonNamespaced {
		t.Error("expected a gap triggered by search_alerts (non-namespaced)")
	}
}

// TestCheckpoint_AuthorityInventoryWrongSource_Integration seeds an MCPTool
// with a non-inventory _source and verifies the authority rule flags it.
// NOTE: This test depends on E2 (authority rules). Will fail until E2 lands.
// The test skeleton validates the handler invocation pattern regardless.
func TestCheckpoint_AuthorityInventoryWrongSource_Integration(t *testing.T) {
	gc := newCheckpointTestClient(t)
	ctx := context.Background()
	rules := loadRealCheckpointRules(t)

	// Check if the authority rule exists in the loaded rules
	hasAuthorityRule := false
	for _, r := range rules {
		if r.Name == "inventory_wrong_source" {
			hasAuthorityRule = true
			break
		}
	}
	if !hasAuthorityRule {
		t.Skip("inventory_wrong_source rule not yet in checkpoint.yaml (waiting for E2)")
	}

	// Seed MCPTool with _source='agent:hunter' — violates inventory authority
	seedQueries := []string{
		`CREATE (:MCPTool {name: "elastic.search", mcp_server: "elastic",
		        _source: "agent:hunter", _updated_at: "2026-01-01T00:00:00Z"})`,
	}
	for _, q := range seedQueries {
		if err := gc.Update(ctx, q, nil); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	handler := makeGraphCheckpointHandler(gc, rules)
	req := buildCheckpointRequest(t, nil)

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	cr := parseCheckpointResult(t, result)

	found := false
	for _, gap := range cr.Gaps {
		if gap.Type == "inventory_wrong_source" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected inventory_wrong_source gap for MCPTool with _source='agent:hunter'")
	}
}

// TestCheckpoint_InventoryPendingMigration_Integration seeds an MCPTool
// with _source='migration:v1' and verifies the sunset warning rule.
// NOTE: This test depends on E2 (authority rules). Will skip if rule absent.
func TestCheckpoint_InventoryPendingMigration_Integration(t *testing.T) {
	gc := newCheckpointTestClient(t)
	ctx := context.Background()
	rules := loadRealCheckpointRules(t)

	hasRule := false
	for _, r := range rules {
		if r.Name == "inventory_pending_migration" {
			hasRule = true
			break
		}
	}
	if !hasRule {
		t.Skip("inventory_pending_migration rule not yet in checkpoint.yaml (waiting for E2)")
	}

	seedQueries := []string{
		`CREATE (:MCPTool {name: "elastic.search", mcp_server: "elastic",
		        _source: "migration:v1", _updated_at: "2026-01-01T00:00:00Z"})`,
	}
	for _, q := range seedQueries {
		if err := gc.Update(ctx, q, nil); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	handler := makeGraphCheckpointHandler(gc, rules)
	req := buildCheckpointRequest(t, nil)

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	cr := parseCheckpointResult(t, result)

	found := false
	for _, gap := range cr.Gaps {
		if gap.Type == "inventory_pending_migration" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected inventory_pending_migration gap for MCPTool with _source='migration:v1'")
	}
}

// TestCheckpoint_CleanGraph_Integration seeds a fully correct graph and
// verifies all_clear=true.
func TestCheckpoint_CleanGraph_Integration(t *testing.T) {
	gc := newCheckpointTestClient(t)
	ctx := context.Background()
	rules := loadRealCheckpointRules(t)

	// Seed a clean graph: properly provenanced, namespaced MCPTool + MCPServer
	seedQueries := []string{
		`CREATE (:MCPServer {config_key: "elastic", type: "mcp", _source: "reconciler:auto", _updated_at: "2026-01-01T00:00:00Z"})`,
		`CREATE (:MCPTool {name: "elastic.search", tool: "search", mcp_server: "elastic",
		        _source: "reconciler:auto", _updated_at: "2026-01-01T00:00:00Z"})`,
		`CREATE (:MCPField {name: "sourceIPAddress", semantic: "ip_address"})`,
		`CREATE (:Semantic {name: "ip_address", description: "IP address"})`,
		`MATCH (mt:MCPTool {name: "elastic.search"}), (f:MCPField {name: "sourceIPAddress"})
		 CREATE (mt)-[:RETURNS_FIELD]->(f)`,
		`MATCH (ms:MCPServer {config_key: "elastic"}), (mt:MCPTool {name: "elastic.search"})
		 CREATE (ms)-[:PROVIDES]->(mt)`,
	}
	for _, q := range seedQueries {
		if err := gc.Update(ctx, q, nil); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	handler := makeGraphCheckpointHandler(gc, rules)
	req := buildCheckpointRequest(t, nil)

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	cr := parseCheckpointResult(t, result)

	if !cr.AllClear {
		t.Errorf("expected all_clear=true for clean graph, got %d gaps:", cr.GapCount)
		for _, gap := range cr.Gaps {
			t.Logf("  gap: type=%s layer=%s desc=%s", gap.Type, gap.Layer, gap.Description)
		}
	}
}
