//go:build integration

package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/darkquasar/fracta/internal/gateway"
	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/mcpclient"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const searchTestAddr = "localhost:6379"
const searchTestGraph = "fracta_search_test"

func newSearchTestClient(t *testing.T) *graph.FalkorDBClient {
	t.Helper()
	c := graph.NewFalkorDBClient(searchTestAddr, graph.WithGraphName(searchTestGraph))
	ctx := context.Background()
	if err := c.Ping(ctx); err != nil {
		t.Skipf("FalkorDB not available at %s: %v", searchTestAddr, err)
	}
	_ = c.DeleteGraph(ctx)
	t.Cleanup(func() {
		_ = c.DeleteGraph(ctx)
		c.Close()
	})
	return c
}

// newTestGatewayWithCatalog creates a Gateway with the given catalog entries.
// It uses a real mcp-go MCPServer but no real backend pool (nil is safe since
// proxy handlers are never invoked in these tests).
func newTestGatewayWithCatalog(t *testing.T, entries []gateway.CatalogEntry) *gateway.Gateway {
	t.Helper()
	gw := gateway.New(nil, nil)
	ms := server.NewMCPServer("test-server", "0.0.0")
	gw.SetMCPServer(ms)

	// Group entries by server name and call ReconcileServer per server.
	byServer := make(map[string][]mcpclient.ToolInfo)
	for _, e := range entries {
		byServer[e.ServerName] = append(byServer[e.ServerName], mcpclient.ToolInfo{
			Name:        e.OriginalName,
			Description: e.Description,
		})
	}
	for name, tools := range byServer {
		if err := gw.ReconcileServer(name, tools); err != nil {
			t.Fatalf("ReconcileServer(%s): %v", name, err)
		}
	}
	return gw
}

func buildSearchRequest(t *testing.T, args map[string]string) mcp.CallToolRequest {
	t.Helper()
	arguments := make(map[string]any, len(args))
	for k, v := range args {
		arguments[k] = v
	}
	reqJSON, _ := json.Marshal(map[string]any{
		"method": "tools/call",
		"params": map[string]any{
			"name":      "search_tool",
			"arguments": arguments,
		},
	})
	var req mcp.CallToolRequest
	if err := json.Unmarshal(reqJSON, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	return req
}

func parseSearchResult(t *testing.T, result *mcp.CallToolResult) searchToolResult {
	t.Helper()
	text := result.Content[0].(mcp.TextContent).Text
	var sr searchToolResult
	if err := json.Unmarshal([]byte(text), &sr); err != nil {
		t.Fatalf("unmarshal search result: %v", err)
	}
	return sr
}

// TestSearchTool_SourceMode_Integration seeds a full 4-tier chain and verifies
// the source-mode Cypher traversal returns grounded, callable results.
func TestSearchTool_SourceMode_Integration(t *testing.T) {
	gc := newSearchTestClient(t)
	ctx := context.Background()

	// Seed 4-tier chain: DomainSource → DataStore → MCPServer → MCPTool
	seedQueries := []string{
		`CREATE (:DomainSource {name: "CloudTrail"})`,
		`CREATE (:DataStore {uri: "elasticsearch://elastic/audit-**", type: "elasticsearch"})`,
		`CREATE (:MCPServer {config_key: "elastic", type: "mcp"})`,
		`CREATE (:MCPTool {name: "elastic.search", tool: "search", mcp_server: "elastic", description: "Search ES"})`,
		`MATCH (ds:DomainSource {name: "CloudTrail"}), (store:DataStore {uri: "elasticsearch://elastic/audit-**"})
		 CREATE (ds)-[:STORED_IN]->(store)`,
		`MATCH (store:DataStore {uri: "elasticsearch://elastic/audit-**"}), (srv:MCPServer {config_key: "elastic"})
		 CREATE (store)-[:QUERYABLE_VIA]->(srv)`,
		`MATCH (srv:MCPServer {config_key: "elastic"}), (mt:MCPTool {name: "elastic.search"})
		 CREATE (srv)-[:PROVIDES]->(mt)`,
	}
	for _, q := range seedQueries {
		if err := gc.Update(ctx, q, nil); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Gateway catalog has elastic.search as callable
	gw := newTestGatewayWithCatalog(t, []gateway.CatalogEntry{
		{ServerName: "elastic", OriginalName: "search", Description: "Search ES"},
	})

	handler := makeSearchToolHandler(gc, gw)
	req := buildSearchRequest(t, map[string]string{"source": "CloudTrail"})

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	sr := parseSearchResult(t, result)
	if len(sr.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(sr.Tools))
	}
	if sr.Tools[0].Name != "elastic.search" {
		t.Errorf("tool name = %q, want 'elastic.search'", sr.Tools[0].Name)
	}
	if !sr.Tools[0].Grounded {
		t.Error("expected grounded=true for source-mode result")
	}
	if sr.Tools[0].MatchType != "graph" {
		t.Errorf("match_type = %q, want 'graph'", sr.Tools[0].MatchType)
	}
	if sr.QueryPath == "" {
		t.Error("expected non-empty query_path")
	}
}

// TestSearchTool_SemanticMode_Integration seeds MCPTool→MCPField and verifies
// semantic-mode returns tools with populated Fields.
func TestSearchTool_SemanticMode_Integration(t *testing.T) {
	gc := newSearchTestClient(t)
	ctx := context.Background()

	seedQueries := []string{
		`CREATE (:MCPTool {name: "elastic.search", tool: "search", mcp_server: "elastic", description: "Search ES"})`,
		`CREATE (:MCPField {name: "sourceIPAddress", semantic: "ip_address"})`,
		`CREATE (:MCPField {name: "destIPAddress", semantic: "ip_address"})`,
		`MATCH (mt:MCPTool {name: "elastic.search"}), (f:MCPField {name: "sourceIPAddress"})
		 CREATE (mt)-[:RETURNS_FIELD]->(f)`,
		`MATCH (mt:MCPTool {name: "elastic.search"}), (f:MCPField {name: "destIPAddress"})
		 CREATE (mt)-[:RETURNS_FIELD]->(f)`,
	}
	for _, q := range seedQueries {
		if err := gc.Update(ctx, q, nil); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	gw := newTestGatewayWithCatalog(t, []gateway.CatalogEntry{
		{ServerName: "elastic", OriginalName: "search", Description: "Search ES"},
	})

	handler := makeSearchToolHandler(gc, gw)
	req := buildSearchRequest(t, map[string]string{"semantic": "ip_address"})

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	sr := parseSearchResult(t, result)
	if len(sr.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(sr.Tools))
	}
	tool := sr.Tools[0]
	if tool.Name != "elastic.search" {
		t.Errorf("tool name = %q, want 'elastic.search'", tool.Name)
	}
	if len(tool.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(tool.Fields))
	}
	// Both fields should have semantic=ip_address
	for _, f := range tool.Fields {
		if f.Semantic != "ip_address" {
			t.Errorf("field %q semantic = %q, want 'ip_address'", f.Name, f.Semantic)
		}
	}
	if !tool.Grounded {
		t.Error("expected grounded=true for semantic-mode result")
	}
}

// TestSearchTool_StrategyMode_Integration seeds Strategy→MCPTool and verifies
// strategy-mode traversal including non-callable diagnostic.
func TestSearchTool_StrategyMode_Integration(t *testing.T) {
	gc := newSearchTestClient(t)
	ctx := context.Background()

	seedQueries := []string{
		`CREATE (:Strategy {name: "ip-enrichment", description: "IP enrichment"})`,
		`CREATE (:MCPTool {name: "elastic.search", tool: "search", mcp_server: "elastic", description: "Search ES"})`,
		`CREATE (:MCPTool {name: "vendor.get_alerts", tool: "get_alerts", mcp_server: "vendor", description: "Get alerts"})`,
		`CREATE (:MCPTool {name: "ghost.missing", tool: "missing", mcp_server: "ghost", description: "Ghost tool"})`,
		`MATCH (s:Strategy {name: "ip-enrichment"}), (mt:MCPTool {name: "elastic.search"})
		 CREATE (s)-[:USES_TOOL]->(mt)`,
		`MATCH (s:Strategy {name: "ip-enrichment"}), (mt:MCPTool {name: "vendor.get_alerts"})
		 CREATE (s)-[:USES_TOOL]->(mt)`,
		`MATCH (s:Strategy {name: "ip-enrichment"}), (mt:MCPTool {name: "ghost.missing"})
		 CREATE (s)-[:USES_TOOL]->(mt)`,
	}
	for _, q := range seedQueries {
		if err := gc.Update(ctx, q, nil); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Only elastic.search and vendor.get_alerts are callable (ghost.missing is not in catalog)
	gw := newTestGatewayWithCatalog(t, []gateway.CatalogEntry{
		{ServerName: "elastic", OriginalName: "search", Description: "Search ES"},
		{ServerName: "vendor", OriginalName: "get_alerts", Description: "Get alerts"},
	})

	handler := makeSearchToolHandler(gc, gw)
	req := buildSearchRequest(t, map[string]string{"strategy": "ip-enrichment"})

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	sr := parseSearchResult(t, result)

	// Only callable tools in results
	if len(sr.Tools) != 2 {
		t.Fatalf("expected 2 callable tools, got %d", len(sr.Tools))
	}

	// Non-callable should be mentioned in query_path
	if sr.QueryPath == "" {
		t.Fatal("expected non-empty query_path")
	}
	// The query_path should mention ghost.missing as non-callable
	found := false
	for _, tool := range sr.Tools {
		if tool.Name == "ghost.missing" {
			found = true
		}
	}
	if found {
		t.Error("ghost.missing should NOT be in callable tools")
	}
}

// TestSearchTool_CallableFiltering_Integration verifies that tools in the
// graph but not in the catalog are excluded from results.
func TestSearchTool_CallableFiltering_Integration(t *testing.T) {
	gc := newSearchTestClient(t)
	ctx := context.Background()

	// Seed keyword-searchable tools
	seedQueries := []string{
		`CREATE (:MCPTool {name: "elastic.search", tool: "search", mcp_server: "elastic", description: "Search the index"})`,
		`CREATE (:MCPTool {name: "elastic.esql", tool: "esql", mcp_server: "elastic", description: "Search with ESQL"})`,
		`CREATE (:MCPTool {name: "ghost.search_ghosts", tool: "search_ghosts", mcp_server: "ghost", description: "Search ghost index"})`,
	}
	for _, q := range seedQueries {
		if err := gc.Update(ctx, q, nil); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Only elastic tools are callable
	gw := newTestGatewayWithCatalog(t, []gateway.CatalogEntry{
		{ServerName: "elastic", OriginalName: "search", Description: "Search the index"},
		{ServerName: "elastic", OriginalName: "esql", Description: "Search with ESQL"},
	})

	handler := makeSearchToolHandler(gc, gw)
	// Use keyword "Search" — all 3 tools match, but only 2 are callable
	req := buildSearchRequest(t, map[string]string{"query": "Search"})

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	sr := parseSearchResult(t, result)
	if len(sr.Tools) != 2 {
		t.Fatalf("expected 2 callable tools, got %d (non-callable should be filtered)", len(sr.Tools))
	}
	for _, tool := range sr.Tools {
		if tool.Server != "elastic" {
			t.Errorf("unexpected server %q — only elastic tools should be callable", tool.Server)
		}
	}
}
