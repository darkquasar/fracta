package graph

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// recordingGraphClient records all Query and Update calls for test assertions.
// It can be primed with query responses via addQueryResponse.
type recordingGraphClient struct {
	mu      sync.Mutex
	updates []updateCall
	queries []queryCall
	// queryResponses maps a substring of the cypher query to a canned response.
	queryResponses map[string][]Record
}

type updateCall struct {
	cypher string
	params map[string]any
}

type queryCall struct {
	cypher string
	params map[string]any
}

func newRecordingClient() *recordingGraphClient {
	return &recordingGraphClient{
		queryResponses: make(map[string][]Record),
	}
}

func (c *recordingGraphClient) addQueryResponse(cypherSubstring string, records []Record) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queryResponses[cypherSubstring] = records
}

func (c *recordingGraphClient) Query(_ context.Context, cypher string, params map[string]any) ([]Record, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = append(c.queries, queryCall{cypher: cypher, params: params})
	for substr, resp := range c.queryResponses {
		if strings.Contains(cypher, substr) {
			return resp, nil
		}
	}
	return nil, nil
}

func (c *recordingGraphClient) Update(_ context.Context, cypher string, params map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updates = append(c.updates, updateCall{cypher: cypher, params: params})
	return nil
}

func (c *recordingGraphClient) Close() error { return nil }

// findUpdate returns the first update whose cypher contains the given substring.
func (c *recordingGraphClient) findUpdate(substr string) *updateCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.updates {
		if strings.Contains(c.updates[i].cypher, substr) {
			return &c.updates[i]
		}
	}
	return nil
}

// findUpdates returns all updates whose cypher contains the given substring.
func (c *recordingGraphClient) findUpdates(substr string) []updateCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []updateCall
	for _, u := range c.updates {
		if strings.Contains(u.cypher, substr) {
			result = append(result, u)
		}
	}
	return result
}

// countUpdates returns the number of updates whose cypher contains the substring.
func (c *recordingGraphClient) countUpdates(substr string) int {
	return len(c.findUpdates(substr))
}

// --- Test: Label Renames ---

func TestMigration_MCPSourceRenamedToMCPTool(t *testing.T) {
	c := newRecordingClient()
	c.addQueryResponse("MATCH (n:MCPSource) RETURN count", []Record{{"c": int64(3)}})

	err := MigrateGraph(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Step 1: add MCPTool label + provenance
	u := c.findUpdate("SET n:MCPTool")
	if u == nil {
		t.Fatal("expected MCPSource→MCPTool rename update (step 1: add label)")
	}
	if u.params["source"] != migrationSource {
		t.Errorf("expected source=%s, got %v", migrationSource, u.params["source"])
	}
	// Step 2: remove MCPSource label (separate query — FalkorDB 4.18.x workaround)
	r := c.findUpdate("REMOVE n:MCPSource")
	if r == nil {
		t.Fatal("expected MCPSource label removal (step 2: separate query)")
	}
}

func TestMigration_LogSourceRenamedToDomainSource(t *testing.T) {
	c := newRecordingClient()
	c.addQueryResponse("MATCH (n:LogSource) RETURN count", []Record{{"c": int64(2)}})

	err := MigrateGraph(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Step 1: add DomainSource label
	u := c.findUpdate("SET n:DomainSource")
	if u == nil {
		t.Fatal("expected LogSource→DomainSource rename update (step 1: add label)")
	}
	// Step 2: remove LogSource label (separate query)
	r := c.findUpdate("REMOVE n:LogSource")
	if r == nil {
		t.Fatal("expected LogSource label removal (step 2: separate query)")
	}
}

// --- Test: DataSource Split ---

func TestMigration_DataSourceSplit_WithIndexPattern(t *testing.T) {
	c := newRecordingClient()
	// Seed: one agent-created DataSource with type=elasticsearch (not a gateway server)
	c.addQueryResponse("MATCH (ds:DataSource) RETURN", []Record{
		{"config_key": "elastic", "type": "elasticsearch", "index_pattern": "audit-**"},
	})

	err := MigrateGraph(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Agent-created DataSource (type != "mcp") should NOT create MCPServer
	u := c.findUpdate("MERGE (ms:MCPServer")
	if u != nil {
		t.Error("agent-created DataSource (type=elasticsearch) should NOT create MCPServer")
	}

	// DataStore created with real URI
	ds := c.findUpdate("MERGE (d:DataStore")
	if ds == nil {
		t.Fatal("expected DataStore creation")
	}
	expectedURI := "elasticsearch://elastic/audit-**"
	if ds.params["uri"] != expectedURI {
		t.Errorf("expected uri=%s, got %v", expectedURI, ds.params["uri"])
	}
	if ds.params["type"] != "elasticsearch" {
		t.Errorf("expected type=elasticsearch, got %v", ds.params["type"])
	}

	// QUERYABLE_VIA should point to derived server key "elastic" (not "elastic" as MCPServer config_key)
	qv := c.findUpdate("QUERYABLE_VIA")
	if qv != nil {
		if qv.params["server_key"] != "elastic" {
			t.Errorf("expected server_key=elastic, got %v", qv.params["server_key"])
		}
	}
	// Should NOT have _status for real DataStore
	if _, hasStatus := ds.params["status"]; hasStatus {
		t.Error("real DataStore should not have _status parameter")
	}

	// QUERYABLE_VIA edge — agent DataSource wires to derived server key
	qv2 := c.findUpdate("QUERYABLE_VIA")
	if qv2 == nil {
		t.Fatal("expected QUERYABLE_VIA edge creation")
	}

	// DataSource deleted
	del := c.findUpdate("MATCH (ds:DataSource) DETACH DELETE")
	if del == nil {
		t.Fatal("expected DataSource deletion")
	}
}

func TestMigration_DataSourceSplit_PlaceholderDataStore(t *testing.T) {
	c := newRecordingClient()
	// Seed: DataSource with type='mcp' and no index_pattern
	c.addQueryResponse("MATCH (ds:DataSource) RETURN", []Record{
		{"config_key": "github", "type": "mcp", "index_pattern": nil},
	})

	err := MigrateGraph(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// DataStore created with placeholder URI
	ds := c.findUpdate("MERGE (d:DataStore")
	if ds == nil {
		t.Fatal("expected DataStore creation")
	}
	expectedURI := "fracta-mcp-gateway://github/"
	if ds.params["uri"] != expectedURI {
		t.Errorf("expected uri=%s, got %v", expectedURI, ds.params["uri"])
	}
	if ds.params["type"] != "fracta-mcp-gateway" {
		t.Errorf("expected type=fracta-mcp-gateway, got %v", ds.params["type"])
	}
	// Gateway DataStores have no _status (they are known access points, not speculative)
	if _, hasStatus := ds.params["status"]; hasStatus {
		t.Error("gateway DataStore should not have _status")
	}
}

func TestMigration_DataSourceSplit_NonMCPNoIndexPattern(t *testing.T) {
	c := newRecordingClient()
	// Seed: DataSource with type != 'mcp' and no index_pattern → server-level URI
	c.addQueryResponse("MATCH (ds:DataSource) RETURN", []Record{
		{"config_key": "snowflake", "type": "snowflake", "index_pattern": nil},
	})

	err := MigrateGraph(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ds := c.findUpdate("MERGE (d:DataStore")
	if ds == nil {
		t.Fatal("expected DataStore creation")
	}
	expectedURI := "snowflake://snowflake/"
	if ds.params["uri"] != expectedURI {
		t.Errorf("expected uri=%s, got %v", expectedURI, ds.params["uri"])
	}
	if ds.params["type"] != "snowflake" {
		t.Errorf("expected type=snowflake, got %v", ds.params["type"])
	}
	// Should NOT have _status for non-placeholder
	if _, hasStatus := ds.params["status"]; hasStatus {
		t.Error("non-placeholder DataStore should not have _status parameter")
	}
}

func TestMigration_DataSourceSplit_EdgeRewiring(t *testing.T) {
	c := newRecordingClient()
	c.addQueryResponse("MATCH (ds:DataSource) RETURN", []Record{
		{"config_key": "elastic", "type": "elasticsearch", "index_pattern": "logs-*"},
	})

	err := MigrateGraph(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// STORED_IN edge rewired from old QUERYABLE_VIA
	si := c.findUpdate("MERGE (src)-[:STORED_IN]->(d)")
	if si == nil {
		t.Fatal("expected STORED_IN edge creation")
	}

	// PROVIDES edge rewired from old FETCHABLE_VIA
	pv := c.findUpdate("MERGE (ms)-[:PROVIDES]->(mt)")
	if pv == nil {
		t.Fatal("expected PROVIDES edge creation")
	}
}

// --- Test: ToolRef Collapse ---

func TestMigration_ToolRefCollapsed(t *testing.T) {
	c := newRecordingClient()
	c.addQueryResponse("MATCH (n:ToolRef) RETURN count", []Record{{"c": int64(2)}})
	// No broken deps
	c.addQueryResponse("MATCH (s:Strategy)-[:USES_TOOL]->(t:ToolRef)", nil)

	err := MigrateGraph(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// USES_TOOL rewired
	rewire := c.findUpdate("MERGE (s)-[:USES_TOOL]->(mt)")
	if rewire == nil {
		t.Fatal("expected USES_TOOL rewire")
	}

	// ToolRef deleted
	del := c.findUpdate("MATCH (t:ToolRef) DETACH DELETE")
	if del == nil {
		t.Fatal("expected ToolRef deletion")
	}
}

func TestMigration_ToolRefCollapse_BrokenDeps(t *testing.T) {
	c := newRecordingClient()
	c.addQueryResponse("MATCH (n:ToolRef) RETURN count", []Record{{"c": int64(1)}})
	// Return broken dep on the "brokenQuery" path
	c.addQueryResponse("MATCH (s:Strategy)-[:USES_TOOL]->(t:ToolRef)\n\t\tRETURN", []Record{
		{"strategy": "my_strat", "tool": "old.tool"},
	})

	err := MigrateGraph(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Broken edges cleaned up
	cleanBroken := c.findUpdates("MATCH (s:Strategy)-[r:USES_TOOL]->(t:ToolRef) DELETE r")
	if len(cleanBroken) == 0 {
		t.Fatal("expected broken USES_TOOL edge cleanup")
	}
}

// --- Test: Idempotency ---

func TestMigration_Idempotent_EmptyGraph(t *testing.T) {
	c := newRecordingClient()
	// All counts return 0 / empty

	err := MigrateGraph(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still backfill null-source (runs unconditionally on label)
	backfill := c.findUpdate("WHERE n._source IS NULL")
	if backfill == nil {
		t.Fatal("expected null-source backfill even on empty graph")
	}
}

func TestMigration_Idempotent_NoOldLabels(t *testing.T) {
	c := newRecordingClient()
	// MCPSource count = 0 (already migrated)
	c.addQueryResponse("MATCH (n:MCPSource) RETURN count", []Record{{"c": int64(0)}})
	// LogSource count = 0
	c.addQueryResponse("MATCH (n:LogSource) RETURN count", []Record{{"c": int64(0)}})
	// No DataSource nodes
	c.addQueryResponse("MATCH (ds:DataSource) RETURN", nil)
	// No ToolRef nodes
	c.addQueryResponse("MATCH (n:ToolRef) RETURN count", []Record{{"c": int64(0)}})

	err := MigrateGraph(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error on idempotent re-run: %v", err)
	}

	// Should NOT have any label rename updates
	if c.findUpdate("SET n:MCPTool") != nil {
		t.Error("should not rename MCPSource when count is 0")
	}
	if c.findUpdate("SET n:DomainSource") != nil {
		t.Error("should not rename LogSource when count is 0")
	}
}

// --- Test: Provenance ---

func TestMigration_ProvenanceStamped(t *testing.T) {
	c := newRecordingClient()
	c.addQueryResponse("MATCH (n:MCPSource) RETURN count", []Record{{"c": int64(1)}})

	err := MigrateGraph(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	u := c.findUpdate("SET n:MCPTool")
	if u == nil {
		t.Fatal("expected MCPTool rename update")
	}
	if u.params["source"] != "migration:v1" {
		t.Errorf("expected _source=migration:v1, got %v", u.params["source"])
	}
	if u.params["now"] == nil || u.params["now"] == "" {
		t.Error("expected _updated_at to be set")
	}
	if !strings.Contains(u.cypher, "_migrated_from") {
		t.Error("expected _migrated_from in cypher")
	}
}

func TestMigration_NullSourceBackfill(t *testing.T) {
	c := newRecordingClient()

	err := MigrateGraph(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should backfill both MCPTool and MCPServer
	mcpToolBackfill := c.findUpdates("MCPTool")
	mcpServerBackfill := c.findUpdates("MCPServer")

	foundToolBackfill := false
	for _, u := range mcpToolBackfill {
		if strings.Contains(u.cypher, "_source IS NULL") && strings.Contains(u.cypher, "manual:legacy") {
			foundToolBackfill = true
			break
		}
	}
	if !foundToolBackfill {
		t.Error("expected MCPTool null-source backfill")
	}

	foundServerBackfill := false
	for _, u := range mcpServerBackfill {
		if strings.Contains(u.cypher, "_source IS NULL") && strings.Contains(u.cypher, "manual:legacy") {
			foundServerBackfill = true
			break
		}
	}
	if !foundServerBackfill {
		t.Error("expected MCPServer null-source backfill")
	}
}

// --- Test: Helper Functions ---

func TestDeriveDataStoreURI(t *testing.T) {
	tests := []struct {
		configKey    string
		dsType       string
		indexPattern string
		wantURI      string
		wantType     string
		wantStatus   string
	}{
		{"elastic", "elasticsearch", "audit-**", "elasticsearch://elastic/audit-**", "elasticsearch", ""},
		{"elastic", "elasticsearch", "", "elasticsearch://elastic/", "elasticsearch", ""},
		{"github", "mcp", "", "fracta-mcp-gateway://github/", "fracta-mcp-gateway", ""},
		{"snowflake", "snowflake", "db.schema.table", "snowflake://snowflake/db.schema.table", "snowflake", ""},
		{"s3-audit", "s3", "org-audit/audit-*/", "s3://s3-audit/org-audit/audit-*/", "s3", ""},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/%s/%s", tt.configKey, tt.dsType, tt.indexPattern), func(t *testing.T) {
			uri, typ, status := deriveDataStoreURI(tt.configKey, tt.dsType, tt.indexPattern)
			if uri != tt.wantURI {
				t.Errorf("uri: got %q, want %q", uri, tt.wantURI)
			}
			if typ != tt.wantType {
				t.Errorf("type: got %q, want %q", typ, tt.wantType)
			}
			if status != tt.wantStatus {
				t.Errorf("status: got %q, want %q", status, tt.wantStatus)
			}
		})
	}
}

func TestHyphenatedToDotNamespaced(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"elastic-search", "elastic.search"},
		{"my-server-tool", "my.server-tool"},
		{"no_hyphens", "no_hyphens"},
		{"dotted.already", "dotted.already"},
		{"-leading", ".leading"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := hyphenatedToDotNamespaced(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Test: Full Migration Scenario ---

func TestMigration_FullScenario(t *testing.T) {
	c := newRecordingClient()

	// Seed the graph with a complete legacy state:
	// - 2 MCPSource nodes
	// - 1 LogSource node
	// - 2 DataSource nodes (one elasticsearch with index_pattern, one mcp without)
	// - 1 ToolRef node
	c.addQueryResponse("MATCH (n:MCPSource) RETURN count", []Record{{"c": int64(2)}})
	c.addQueryResponse("MATCH (n:LogSource) RETURN count", []Record{{"c": int64(1)}})
	c.addQueryResponse("MATCH (ds:DataSource) RETURN", []Record{
		{"config_key": "elastic", "type": "elasticsearch", "index_pattern": "audit-**"},
		{"config_key": "github", "type": "mcp", "index_pattern": nil},
	})
	c.addQueryResponse("MATCH (n:ToolRef) RETURN count", []Record{{"c": int64(1)}})
	// No broken strategy deps
	c.addQueryResponse("MATCH (s:Strategy)-[:USES_TOOL]->(t:ToolRef)\n\t\tRETURN", nil)

	err := MigrateGraph(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify key operations happened:
	// 1. MCPSource renamed
	if c.findUpdate("SET n:MCPTool") == nil {
		t.Error("missing MCPSource→MCPTool rename")
	}

	// 2. LogSource renamed
	if c.findUpdate("SET n:DomainSource") == nil {
		t.Error("missing LogSource→DomainSource rename")
	}

	// 3. Only gateway DataSource (type=mcp) creates MCPServer; agent DataSource (type=elasticsearch) does not
	serverCreations := c.findUpdates("MERGE (ms:MCPServer")
	if len(serverCreations) != 1 {
		t.Errorf("expected 1 MCPServer creation (only type=mcp), got %d", len(serverCreations))
	}

	// 4. Both DataStores created
	storeCreations := c.findUpdates("MERGE (d:DataStore")
	if len(storeCreations) != 2 {
		t.Errorf("expected 2 DataStore creations, got %d", len(storeCreations))
	}

	// 5. One gateway DataStore (fracta-mcp-gateway:// URI for type=mcp DataSource)
	foundGateway := false
	for _, u := range storeCreations {
		if uri, ok := u.params["uri"].(string); ok && strings.HasPrefix(uri, "fracta-mcp-gateway://") {
			foundGateway = true
			if u.params["type"] != "fracta-mcp-gateway" {
				t.Errorf("gateway DataStore type should be 'fracta-mcp-gateway', got %v", u.params["type"])
			}
			// Gateway DataStores have no _status (not speculative — they're known access points)
			if _, hasStatus := u.params["status"]; hasStatus {
				t.Error("gateway DataStore should not have _status")
			}
		}
	}
	if !foundGateway {
		t.Error("expected one gateway DataStore with fracta-mcp-gateway:// URI")
	}

	// 6. ToolRef collapsed
	if c.findUpdate("MERGE (s)-[:USES_TOOL]->(mt)") == nil {
		t.Error("missing ToolRef→MCPTool rewire")
	}

	// 7. DataSource deleted
	if c.findUpdate("MATCH (ds:DataSource) DETACH DELETE") == nil {
		t.Error("missing DataSource deletion")
	}

	// 8. ToolRef deleted
	if c.findUpdate("MATCH (t:ToolRef) DETACH DELETE") == nil {
		t.Error("missing ToolRef deletion")
	}

	// 9. Null-source backfill
	if c.findUpdate("manual:legacy") == nil {
		t.Error("missing null-source backfill")
	}
}

// --- Test: Name Cleanup ---

func TestMigration_NameCleanup_Rename(t *testing.T) {
	c := newRecordingClient()
	// Seed: one hyphenated MCPTool with no dot
	c.addQueryResponse("WHERE mt.name CONTAINS '-'", []Record{
		{"name": "elastic-search"},
	})
	// No conflict (new name doesn't exist)
	c.addQueryResponse("MATCH (mt:MCPTool {name: $name}) RETURN mt.name", nil)

	err := MigrateGraph(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rename := c.findUpdate("SET mt.name = $new_name")
	if rename == nil {
		t.Fatal("expected name rename update")
	}
	if rename.params["old_name"] != "elastic-search" {
		t.Errorf("expected old_name=elastic-search, got %v", rename.params["old_name"])
	}
	if rename.params["new_name"] != "elastic.search" {
		t.Errorf("expected new_name=elastic.search, got %v", rename.params["new_name"])
	}
}
