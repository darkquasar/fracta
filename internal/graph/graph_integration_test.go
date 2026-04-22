//go:build integration

package graph

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const testAddr = "localhost:6379"
const testGraph = "fracta_test"

func newTestClient(t *testing.T) *FalkorDBClient {
	t.Helper()
	c := NewFalkorDBClient(testAddr, WithGraphName(testGraph))
	ctx := context.Background()
	if err := c.Ping(ctx); err != nil {
		t.Skipf("FalkorDB not available at %s: %v", testAddr, err)
	}
	// Clean slate for each test.
	_ = c.DeleteGraph(ctx)
	t.Cleanup(func() {
		_ = c.DeleteGraph(ctx)
		c.Close()
	})
	return c
}

func TestQueryAndUpdate(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// Create a node.
	err := c.Update(ctx, `CREATE (:LogSource {name: "TestLog", description: "test"})`, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Query it back.
	recs, err := c.Query(ctx, `MATCH (l:LogSource {name: "TestLog"}) RETURN l.name AS name, l.description AS desc`, nil)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if recs[0]["name"] != "TestLog" {
		t.Errorf("expected name=TestLog, got %v", recs[0]["name"])
	}
	if recs[0]["desc"] != "test" {
		t.Errorf("expected desc=test, got %v", recs[0]["desc"])
	}
}

func TestQueryWithParams(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// Create nodes with direct Cypher.
	err := c.Update(ctx, `CREATE (:LogSource {name: "CloudTrail", region: "us-east-1"})`, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	err = c.Update(ctx, `CREATE (:LogSource {name: "VPCFlowLogs", region: "eu-west-1"})`, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Query using parameterized $name — this exercises buildCypherPrefix.
	recs, err := c.Query(ctx,
		`MATCH (l:LogSource {name: $name}) RETURN l.name AS name, l.region AS region`,
		map[string]any{"name": "CloudTrail"},
	)
	if err != nil {
		t.Fatalf("Query with params: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if recs[0]["name"] != "CloudTrail" {
		t.Errorf("name = %v, want CloudTrail", recs[0]["name"])
	}
	if recs[0]["region"] != "us-east-1" {
		t.Errorf("region = %v, want us-east-1", recs[0]["region"])
	}

	// Query with a param that matches a different node.
	recs, err = c.Query(ctx,
		`MATCH (l:LogSource {name: $name}) RETURN l.region AS region`,
		map[string]any{"name": "VPCFlowLogs"},
	)
	if err != nil {
		t.Fatalf("Query with params (2): %v", err)
	}
	if len(recs) != 1 || recs[0]["region"] != "eu-west-1" {
		t.Errorf("expected eu-west-1, got %v", recs)
	}

	// Update using params.
	err = c.Update(ctx,
		`MATCH (l:LogSource {name: $name}) SET l.region = $region`,
		map[string]any{"name": "CloudTrail", "region": "ap-southeast-2"},
	)
	if err != nil {
		t.Fatalf("Update with params: %v", err)
	}

	// Verify the update took effect.
	recs, err = c.Query(ctx,
		`MATCH (l:LogSource {name: $name}) RETURN l.region AS region`,
		map[string]any{"name": "CloudTrail"},
	)
	if err != nil {
		t.Fatalf("Query after update: %v", err)
	}
	if len(recs) != 1 || recs[0]["region"] != "ap-southeast-2" {
		t.Errorf("expected ap-southeast-2 after update, got %v", recs)
	}

	// Query with int param.
	err = c.Update(ctx, `MATCH (l:LogSource {name: "CloudTrail"}) SET l.priority = 1`, nil)
	if err != nil {
		t.Fatalf("set priority: %v", err)
	}
	recs, err = c.Query(ctx,
		`MATCH (l:LogSource) WHERE l.priority = $p RETURN l.name AS name`,
		map[string]any{"p": 1},
	)
	if err != nil {
		t.Fatalf("Query with int param: %v", err)
	}
	if len(recs) != 1 || recs[0]["name"] != "CloudTrail" {
		t.Errorf("expected CloudTrail with priority=1, got %v", recs)
	}

	// Param with single quote in string value — tests escaping.
	err = c.Update(ctx,
		`CREATE (:LogSource {name: $name})`,
		map[string]any{"name": "O'Brien Logs"},
	)
	if err != nil {
		t.Fatalf("Update with quote in param: %v", err)
	}
	recs, err = c.Query(ctx,
		`MATCH (l:LogSource {name: $name}) RETURN l.name AS name`,
		map[string]any{"name": "O'Brien Logs"},
	)
	if err != nil {
		t.Fatalf("Query with quote in param: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 record for quoted param, got %d", len(recs))
	}
	if recs[0]["name"] != "O'Brien Logs" {
		t.Errorf("name = %v, want O'Brien Logs", recs[0]["name"])
	}
}

func TestSeedFromDir(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	seedDir := seedsDir(t)

	n, err := SeedFromDir(ctx, c, seedDir)
	if err != nil {
		t.Fatalf("SeedFromDir: %v", err)
	}
	if n == 0 {
		t.Fatal("expected >0 statements executed")
	}
	t.Logf("seeded %d statements", n)

	// Seeds now contain only Semantic vocabulary nodes (spec-02 decision:
	// DomainSources are agent-discovered, not pre-seeded).
	recs, err := c.Query(ctx, `MATCH (s:Semantic) RETURN s.name AS name ORDER BY s.name`, nil)
	if err != nil {
		t.Fatalf("query semantics: %v", err)
	}
	if len(recs) < 10 {
		t.Fatalf("expected >= 10 semantic nodes, got %d", len(recs))
	}
	t.Logf("found %d semantic nodes", len(recs))
}

func TestGraphRAGContext(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// Create test data: two domain sources with IP fields that join.
	setup := []string{
		`CREATE (:DomainSource {name: "CloudTrail"})-[:HAS_FIELD]->(:FieldType {name: "sourceIPAddress", semantic: "ip_address"})`,
		`CREATE (:DomainSource {name: "VPCFlowLogs"})-[:HAS_FIELD]->(:FieldType {name: "srcaddr", semantic: "ip_address"})`,
		`MATCH (f1:FieldType {name: "sourceIPAddress"}), (f2:FieldType {name: "srcaddr"}) CREATE (f1)-[:JOINS_WITH {confidence: 0.99, method: "exact"}]->(f2)`,
		`CREATE (:Strategy {name: "correlate-ip", description: "IP correlation"})`,
		`MATCH (s:Strategy {name: "correlate-ip"}), (d:DomainSource {name: "CloudTrail"}) CREATE (s)-[:USES_SOURCE]->(d)`,
	}
	for _, q := range setup {
		if err := c.Update(ctx, q, nil); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	// Query RAG context for IP-related semantics.
	ragCtx, err := GraphRAGContext(ctx, c, []string{"ip_address"})
	if err != nil {
		t.Fatalf("GraphRAGContext: %v", err)
	}

	if len(ragCtx.DomainSources) == 0 {
		t.Error("expected domain source join paths for ip_address semantic")
	}
	if len(ragCtx.Strategies) == 0 {
		t.Error("expected strategies for ip_address semantic")
	}

	s := ragCtx.String()
	if len(s) < 50 {
		t.Errorf("RAG context string too short: %d chars", len(s))
	}
	t.Log(s)
}

// TestMigrateGraph_EndToEnd seeds a legacy 3-tier graph and verifies that
// MigrateGraph() produces a correct 4-tier ontology.
func TestMigrateGraph_EndToEnd(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// Seed legacy graph: LogSource → DataSource → MCPSource → MCPField
	// with edges: QUERYABLE_VIA, FETCHABLE_VIA, RETURNS_FIELD
	// plus a ToolRef and Strategy for the collapse step.
	legacySetup := []string{
		// Core 3-tier chain
		`CREATE (:LogSource {name: "CloudTrail", description: "AWS CloudTrail logs"})`,
		`CREATE (:DataSource {config_key: "elastic", type: "elasticsearch", index_pattern: "audit-**"})`,
		`CREATE (:MCPSource {name: "elastic-search", tool: "search", mcp_server: "elastic", description: "Search tool"})`,
		`CREATE (:MCPField {name: "sourceIPAddress", semantic: "ip_address"})`,

		// Wire edges: LogSource → DataSource (QUERYABLE_VIA)
		`MATCH (ls:LogSource {name: "CloudTrail"}), (ds:DataSource {config_key: "elastic"})
		 CREATE (ls)-[:QUERYABLE_VIA]->(ds)`,
		// DataSource → MCPSource (FETCHABLE_VIA)
		`MATCH (ds:DataSource {config_key: "elastic"}), (ms:MCPSource {name: "elastic-search"})
		 CREATE (ds)-[:FETCHABLE_VIA]->(ms)`,
		// MCPSource → MCPField (RETURNS_FIELD)
		`MATCH (ms:MCPSource {name: "elastic-search"}), (f:MCPField {name: "sourceIPAddress"})
		 CREATE (ms)-[:RETURNS_FIELD]->(f)`,

		// ToolRef for collapse step
		`CREATE (:ToolRef {name: "elastic-search", mcp_server: "elastic"})`,
		`CREATE (:Strategy {name: "ip-enrichment", description: "IP enrichment"})`,
		`MATCH (s:Strategy {name: "ip-enrichment"}), (tr:ToolRef {name: "elastic-search"})
		 CREATE (s)-[:USES_TOOL]->(tr)`,

		// Second MCP source with MCP-type DataSource (no index_pattern → placeholder DataStore)
		`CREATE (:DataSource {config_key: "vendor", type: "mcp"})`,
		`CREATE (:MCPSource {name: "vendor-get_alerts", tool: "get_alerts", mcp_server: "vendor", description: "Get alerts"})`,
		`MATCH (ds:DataSource {config_key: "vendor"}), (ms:MCPSource {name: "vendor-get_alerts"})
		 CREATE (ds)-[:FETCHABLE_VIA]->(ms)`,
	}
	for _, q := range legacySetup {
		if err := c.Update(ctx, q, nil); err != nil {
			t.Fatalf("legacy setup: %v", err)
		}
	}

	// Verify legacy state: MCPSource exists, MCPTool does not
	assertNodeCount(t, c, ctx, "MCPSource", 2)
	assertNodeCount(t, c, ctx, "LogSource", 1)
	assertNodeCount(t, c, ctx, "DataSource", 2)
	assertNodeCount(t, c, ctx, "ToolRef", 1)
	assertNodeCount(t, c, ctx, "MCPTool", 0)
	assertNodeCount(t, c, ctx, "DomainSource", 0)
	assertNodeCount(t, c, ctx, "MCPServer", 0)
	assertNodeCount(t, c, ctx, "DataStore", 0)

	// Run migration
	if err := MigrateGraph(ctx, c); err != nil {
		t.Fatalf("MigrateGraph: %v", err)
	}

	// Verify: old labels gone
	assertNodeCount(t, c, ctx, "MCPSource", 0)
	assertNodeCount(t, c, ctx, "LogSource", 0)
	assertNodeCount(t, c, ctx, "DataSource", 0)
	assertNodeCount(t, c, ctx, "ToolRef", 0)

	// Verify: new labels present
	assertNodeCount(t, c, ctx, "MCPTool", 2)
	assertNodeCount(t, c, ctx, "DomainSource", 1)
	assertNodeCount(t, c, ctx, "MCPServer", 2)
	assertNodeCount(t, c, ctx, "DataStore", 2)

	// Verify: 4-tier chain is traversable
	// DomainSource → STORED_IN → DataStore → QUERYABLE_VIA → MCPServer → PROVIDES → MCPTool → RETURNS_FIELD → MCPField
	chainQuery := `MATCH (ds:DomainSource {name: "CloudTrail"})
		-[:STORED_IN]->(store:DataStore)
		-[:QUERYABLE_VIA]->(srv:MCPServer)
		-[:PROVIDES]->(mt:MCPTool)
		-[:RETURNS_FIELD]->(f:MCPField)
		RETURN ds.name AS domain_source, store.uri AS store_uri,
		       srv.config_key AS server, mt.name AS tool, f.name AS field`
	recs, err := c.Query(ctx, chainQuery, nil)
	if err != nil {
		t.Fatalf("4-tier chain query: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 complete 4-tier chain, got %d", len(recs))
	}
	rec := recs[0]
	if rec["domain_source"] != "CloudTrail" {
		t.Errorf("domain_source = %v, want CloudTrail", rec["domain_source"])
	}
	if rec["server"] != "elastic" {
		t.Errorf("server = %v, want elastic", rec["server"])
	}
	if rec["field"] != "sourceIPAddress" {
		t.Errorf("field = %v, want sourceIPAddress", rec["field"])
	}

	// Verify: dot-namespaced tool name (hyphenated → dot)
	toolNameRecs, err := c.Query(ctx,
		`MATCH (mt:MCPTool) WHERE mt.name CONTAINS 'elastic' RETURN mt.name AS name`, nil)
	if err != nil {
		t.Fatalf("tool name query: %v", err)
	}
	if len(toolNameRecs) != 1 {
		t.Fatalf("expected 1 elastic MCPTool, got %d", len(toolNameRecs))
	}
	toolName := toolNameRecs[0]["name"].(string)
	if toolName != "elastic.search" {
		t.Errorf("MCPTool name = %q, want 'elastic.search' (dot-namespaced)", toolName)
	}

	// Verify: provenance stamps on migrated nodes
	provRecs, err := c.Query(ctx,
		`MATCH (mt:MCPTool {name: "elastic.search"})
		 RETURN mt._source AS source, mt._migrated_from AS migrated_from`, nil)
	if err != nil {
		t.Fatalf("provenance query: %v", err)
	}
	if len(provRecs) != 1 {
		t.Fatalf("expected 1 provenance record, got %d", len(provRecs))
	}
	if provRecs[0]["source"] != "migration:v1" {
		t.Errorf("_source = %v, want 'migration:v1'", provRecs[0]["source"])
	}

	// Verify: placeholder DataStore has _status='speculative'
	specRecs, err := c.Query(ctx,
		`MATCH (ds:DataStore) WHERE ds.uri STARTS WITH 'pending://'
		 RETURN ds.uri AS uri, ds._status AS status`, nil)
	if err != nil {
		t.Fatalf("speculative DataStore query: %v", err)
	}
	if len(specRecs) != 1 {
		t.Fatalf("expected 1 speculative DataStore, got %d", len(specRecs))
	}
	if specRecs[0]["status"] != "speculative" {
		t.Errorf("speculative DataStore _status = %v, want 'speculative'", specRecs[0]["status"])
	}

	// Verify: Strategy → USES_TOOL now points to MCPTool (not ToolRef)
	stratRecs, err := c.Query(ctx,
		`MATCH (s:Strategy {name: "ip-enrichment"})-[:USES_TOOL]->(mt:MCPTool)
		 RETURN mt.name AS tool`, nil)
	if err != nil {
		t.Fatalf("strategy USES_TOOL query: %v", err)
	}
	if len(stratRecs) != 1 {
		t.Fatalf("expected 1 strategy→tool edge, got %d", len(stratRecs))
	}
	if stratRecs[0]["tool"] != "elastic.search" {
		t.Errorf("strategy tool = %v, want 'elastic.search'", stratRecs[0]["tool"])
	}

	// Verify: idempotency — running migration again should not error
	if err := MigrateGraph(ctx, c); err != nil {
		t.Fatalf("MigrateGraph (idempotent re-run): %v", err)
	}

	// Counts should be unchanged after re-run
	assertNodeCount(t, c, ctx, "MCPTool", 2)
	assertNodeCount(t, c, ctx, "DomainSource", 1)
	assertNodeCount(t, c, ctx, "MCPServer", 2)
	assertNodeCount(t, c, ctx, "DataStore", 2)
}

// assertNodeCount is a helper for migration tests.
func assertNodeCount(t *testing.T, c *FalkorDBClient, ctx context.Context, label string, want int) {
	t.Helper()
	got, err := countNodes(ctx, c, label)
	if err != nil {
		t.Fatalf("countNodes(%s): %v", label, err)
	}
	if got != want {
		t.Errorf("%s count = %d, want %d", label, got, want)
	}
}

func seedsDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file path")
	}
	// Navigate from internal/graph/ up to repo root, then into strategies/seeds/
	repoRoot := filepath.Join(filepath.Dir(filename), "..", "..")
	dir := filepath.Join(repoRoot, "strategies", "seeds")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("seeds dir not found at %s (not yet created): %v", dir, err)
	}
	return dir
}
