package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/darkquasar/fracta/internal/gateway"
	"github.com/darkquasar/fracta/internal/graph"
	"github.com/mark3labs/mcp-go/mcp"
)

// searchGraphClient is a mock GraphClient that returns canned results based on
// query substring matching. Uses the same response-map pattern as
// checkpointGraphClient but keyed by substring for flexibility across search modes.
type searchGraphClient struct {
	responses map[string][]graph.Record // substring → records
	queryErr  error                     // if set, all queries return this error
}

func (c *searchGraphClient) Query(_ context.Context, cypher string, _ map[string]any) ([]graph.Record, error) {
	if c.queryErr != nil {
		return nil, c.queryErr
	}
	for key, recs := range c.responses {
		if strings.Contains(cypher, key) {
			return recs, nil
		}
	}
	return nil, nil
}

func (c *searchGraphClient) Update(_ context.Context, _ string, _ map[string]any) error {
	return nil
}

func (c *searchGraphClient) Close() error { return nil }

// stubCatalog implements catalogProvider with a fixed set of entries.
type stubCatalog struct {
	entries []gateway.CatalogEntry
}

func (s *stubCatalog) Catalog() []gateway.CatalogEntry {
	return s.entries
}

// makeSearchReq builds a mcp.CallToolRequest with the given arguments.
func makeSearchReq(args map[string]any) mcp.CallToolRequest {
	argsJSON, _ := json.Marshal(args)
	reqJSON := fmt.Sprintf(`{"method":"tools/call","params":{"name":"search_tool","arguments":%s}}`, string(argsJSON))
	var req mcp.CallToolRequest
	_ = json.Unmarshal([]byte(reqJSON), &req)
	return req
}

// parseSearchRes extracts the searchToolResult from a CallToolResult.
func parseSearchRes(t *testing.T, result *mcp.CallToolResult) searchToolResult {
	t.Helper()
	text := result.Content[0].(mcp.TextContent).Text
	var sr searchToolResult
	if err := json.Unmarshal([]byte(text), &sr); err != nil {
		t.Fatalf("failed to parse search result: %v", err)
	}
	return sr
}

// === F1: 7 search_tool handler-level tests ===

// Test 1: Semantic mode — graph returns 2 tools with fields, catalog has 1 → only callable returned with fields
func TestSearchHandler_SemanticMode_CallableFilter(t *testing.T) {
	gc := &searchGraphClient{
		responses: map[string][]graph.Record{
			"MCPField": {
				{"tool": "elastic.search", "server": "elastic", "description": "Search", "field_name": "src_ip", "field_semantic": "ip_address"},
				{"tool": "elastic.search", "server": "elastic", "description": "Search", "field_name": "dst_ip", "field_semantic": "ip_address"},
				{"tool": "vendor.get_alerts", "server": "vendor", "description": "Alerts", "field_name": "src_ip", "field_semantic": "ip_address"},
			},
		},
	}

	// Only elastic.search is callable (in catalog)
	cp := &stubCatalog{entries: []gateway.CatalogEntry{
		{ServerName: "elastic", OriginalName: "search", Description: "Search"},
	}}

	handler := makeSearchToolHandler(gc, cp)
	req := makeSearchReq(map[string]any{"semantic": "ip_address"})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	sr := parseSearchRes(t, result)

	if len(sr.Tools) != 1 {
		t.Fatalf("expected 1 callable tool, got %d", len(sr.Tools))
	}
	if sr.Tools[0].Name != "elastic.search" {
		t.Errorf("expected elastic.search, got %s", sr.Tools[0].Name)
	}
	if !sr.Tools[0].Grounded {
		t.Error("semantic mode result should be grounded")
	}
	if sr.Tools[0].MatchType != "graph" {
		t.Errorf("expected match_type graph, got %s", sr.Tools[0].MatchType)
	}
	if len(sr.Tools[0].Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(sr.Tools[0].Fields))
	}
	if !strings.Contains(sr.QueryPath, "Semantic(ip_address)") {
		t.Errorf("QueryPath should contain semantic description, got: %s", sr.QueryPath)
	}
}

// Test 2: Source mode — graph returns tools from 4-tier traversal → results grounded, match_type=graph
func TestSearchHandler_SourceMode_Grounded(t *testing.T) {
	gc := &searchGraphClient{
		responses: map[string][]graph.Record{
			"DomainSource": {
				{"tool": "elastic.search", "server": "elastic", "description": "Search"},
				{"tool": "elastic.esql", "server": "elastic", "description": "ESQL"},
			},
		},
	}

	cp := &stubCatalog{entries: []gateway.CatalogEntry{
		{ServerName: "elastic", OriginalName: "search", Description: "Search"},
		{ServerName: "elastic", OriginalName: "esql", Description: "ESQL"},
	}}

	handler := makeSearchToolHandler(gc, cp)
	req := makeSearchReq(map[string]any{"source": "AWS CloudTrail"})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	sr := parseSearchRes(t, result)

	if len(sr.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(sr.Tools))
	}
	for _, tool := range sr.Tools {
		if !tool.Grounded {
			t.Errorf("source mode tool %s should be grounded", tool.Name)
		}
		if tool.MatchType != "graph" {
			t.Errorf("source mode tool %s should have match_type=graph, got %s", tool.Name, tool.MatchType)
		}
	}
	if !strings.Contains(sr.QueryPath, "DomainSource(AWS CloudTrail)") {
		t.Errorf("QueryPath should contain source description, got: %s", sr.QueryPath)
	}
}

// Test 3: Strategy mode — graph returns 3 tools, catalog has 2 → non-callable in query_path diagnostic
func TestSearchHandler_StrategyMode_NonCallableDiagnostic(t *testing.T) {
	gc := &searchGraphClient{
		responses: map[string][]graph.Record{
			"Strategy": {
				{"tool": "elastic.search", "server": "elastic", "description": "Search"},
				{"tool": "elastic.esql", "server": "elastic", "description": "ESQL"},
				{"tool": "vendor.get_alerts", "server": "vendor", "description": "Alerts"},
			},
		},
	}

	cp := &stubCatalog{entries: []gateway.CatalogEntry{
		{ServerName: "elastic", OriginalName: "search", Description: "Search"},
		{ServerName: "elastic", OriginalName: "esql", Description: "ESQL"},
	}}

	handler := makeSearchToolHandler(gc, cp)
	req := makeSearchReq(map[string]any{"strategy": "lateral-movement"})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	sr := parseSearchRes(t, result)

	if len(sr.Tools) != 2 {
		t.Fatalf("expected 2 callable tools, got %d", len(sr.Tools))
	}
	if !strings.Contains(sr.QueryPath, "non-callable") {
		t.Errorf("QueryPath should mention non-callable deps, got: %s", sr.QueryPath)
	}
	if !strings.Contains(sr.QueryPath, "vendor.get_alerts") {
		t.Errorf("QueryPath should name non-callable tool, got: %s", sr.QueryPath)
	}
}

// Test 4: Keyword mode — empty catalog → empty result (not error)
func TestSearchHandler_KeywordMode_EmptyCatalog(t *testing.T) {
	gc := &searchGraphClient{
		responses: map[string][]graph.Record{
			"CONTAINS": {
				{"tool": "elastic.search", "server": "elastic", "description": "Search"},
			},
		},
	}

	cp := &stubCatalog{entries: nil}

	handler := makeSearchToolHandler(gc, cp)
	req := makeSearchReq(map[string]any{"query": "search"})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	if result.IsError {
		t.Fatal("empty catalog keyword search should not return error")
	}

	sr := parseSearchRes(t, result)
	if len(sr.Tools) != 0 {
		t.Fatalf("expected 0 tools with empty catalog, got %d", len(sr.Tools))
	}
	if !strings.Contains(sr.QueryPath, "Keyword(search)") {
		t.Errorf("QueryPath should contain keyword description, got: %s", sr.QueryPath)
	}
}

// Test 5: Default mode (no params) — returns catalog entries as match_type=catalog
func TestSearchHandler_DefaultMode_CatalogEntries(t *testing.T) {
	gc := &searchGraphClient{}

	cp := &stubCatalog{entries: []gateway.CatalogEntry{
		{ServerName: "elastic", OriginalName: "search", Description: "Search things"},
		{ServerName: "vendor", OriginalName: "get_alerts", Description: "Get alerts"},
	}}

	handler := makeSearchToolHandler(gc, cp)
	req := makeSearchReq(map[string]any{})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	sr := parseSearchRes(t, result)

	if len(sr.Tools) != 2 {
		t.Fatalf("expected 2 catalog tools, got %d", len(sr.Tools))
	}
	for _, tool := range sr.Tools {
		if tool.MatchType != "catalog" {
			t.Errorf("default mode tool %s should have match_type=catalog, got %s", tool.Name, tool.MatchType)
		}
		if tool.Grounded {
			t.Errorf("catalog tools should not be grounded")
		}
	}
	if sr.QueryPath != "Full catalog (no filter)" {
		t.Errorf("unexpected QueryPath: %s", sr.QueryPath)
	}
}

// Test 6: Graph error — returns MCP error result
func TestSearchHandler_GraphError_ReturnsError(t *testing.T) {
	gc := &searchGraphClient{
		queryErr: fmt.Errorf("connection refused"),
	}

	cp := &stubCatalog{entries: []gateway.CatalogEntry{
		{ServerName: "elastic", OriginalName: "search", Description: "Search"},
	}}

	handler := makeSearchToolHandler(gc, cp)
	req := makeSearchReq(map[string]any{"semantic": "ip_address"})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler should not return Go error: %v", err)
	}

	if !result.IsError {
		t.Fatal("expected MCP error result on graph failure")
	}

	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "graph query failed") {
		t.Errorf("error text should mention graph query failure, got: %s", text)
	}
}

// Test 7: Empty graph result — returns empty tools array, valid JSON
func TestSearchHandler_EmptyGraphResult_ValidJSON(t *testing.T) {
	gc := &searchGraphClient{
		responses: map[string][]graph.Record{},
	}

	cp := &stubCatalog{entries: []gateway.CatalogEntry{
		{ServerName: "elastic", OriginalName: "search", Description: "Search"},
	}}

	handler := makeSearchToolHandler(gc, cp)
	req := makeSearchReq(map[string]any{"source": "NonexistentSource"})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	if result.IsError {
		t.Fatal("empty graph result should not be an error")
	}

	sr := parseSearchRes(t, result)
	if len(sr.Tools) != 0 {
		t.Fatalf("expected 0 tools from empty graph result, got %d", len(sr.Tools))
	}

	// Verify the JSON is valid by re-marshaling
	text := result.Content[0].(mcp.TextContent).Text
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
}
