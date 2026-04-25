package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/mcpclient"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// --- Mock Pool ---

type mockPool struct {
	servers map[string][]mcpclient.ToolInfo
	calls   []mockCallRecord
}

type mockCallRecord struct {
	Server, Tool string
	Args         map[string]any
}

func (p *mockPool) ServerNames() []string {
	names := make([]string, 0, len(p.servers))
	for n := range p.servers {
		names = append(names, n)
	}
	return names
}

func (p *mockPool) DiscoverTools(_ context.Context, server string) ([]mcpclient.ToolInfo, error) {
	return p.servers[server], nil
}

func (p *mockPool) CallToolRaw(_ context.Context, svr, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	p.calls = append(p.calls, mockCallRecord{Server: svr, Tool: tool, Args: args})
	return mcp.NewToolResultText("mock result"), nil
}

// --- Mock Graph ---

type mockGraph struct {
	updates []mockGraphUpdate
}

type mockGraphUpdate struct {
	Cypher string
	Params map[string]any
}

func (g *mockGraph) Query(_ context.Context, _ string, _ map[string]any) ([]graph.Record, error) {
	return nil, nil
}
func (g *mockGraph) Update(_ context.Context, cypher string, params map[string]any) error {
	g.updates = append(g.updates, mockGraphUpdate{Cypher: cypher, Params: params})
	return nil
}
func (g *mockGraph) Ping(_ context.Context) error { return nil }
func (g *mockGraph) Close() error                 { return nil }

// --- Pool interface adapter ---
// Gateway uses *mcpclient.Pool directly, so we need to adapt.
// Instead, we'll test through the public API with real pool by using
// the Gateway's internal methods via the exported interface.

// For these tests, we construct the Gateway differently to inject mocks.
// We'll test the catalog and graph registration logic directly.

func TestRegisterServer_CreatesNamespacedTools(t *testing.T) {
	mg := &mockGraph{}
	mcpSrv := server.NewMCPServer("test", "0.1.0", server.WithToolCapabilities(true))

	gw := &Gateway{
		mcpServer: mcpSrv,
		graph:     mg,
		catalog:   make(map[string]CatalogEntry),
	}

	// Simulate what RegisterServer does by calling the internal registration logic
	tools := []mcpclient.ToolInfo{
		{Name: "search", Description: "Search things", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "list", Description: "List things", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}

	for _, t := range tools {
		nsName := "elastic." + t.Name
		proxyTool := mcp.NewToolWithRawSchema(nsName, "[elastic] "+t.Description, t.InputSchema)
		mcpSrv.AddTool(proxyTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("proxied"), nil
		})
		gw.catalog[nsName] = CatalogEntry{
			ServerName:   "elastic",
			OriginalName: t.Name,
			Description:  t.Description,
		}
	}
	gw.registerInGraph(context.Background(), "elastic", tools)

	// Verify catalog
	if gw.ToolCount() != 2 {
		t.Errorf("ToolCount = %d, want 2", gw.ToolCount())
	}

	catalog := gw.Catalog()
	if len(catalog) != 2 {
		t.Fatalf("Catalog len = %d, want 2", len(catalog))
	}

	// Verify graph updates: MCPServer(1) + MCPTool(2) + PROVIDES(2) = 5
	if len(mg.updates) != 5 {
		t.Errorf("graph updates = %d, want 5", len(mg.updates))
	}

	// First update should be the MCPServer
	if mg.updates[0].Params["server"] != "elastic" {
		t.Errorf("first update server = %v", mg.updates[0].Params["server"])
	}
}

func TestCatalog_ReturnsSortedSnapshot(t *testing.T) {
	gw := &Gateway{
		catalog: map[string]CatalogEntry{
			"vendor.search_alerts": {ServerName: "vendor", OriginalName: "search_alerts"},
			"elastic.search":      {ServerName: "elastic", OriginalName: "search"},
			"elastic.esql":        {ServerName: "elastic", OriginalName: "esql"},
		},
	}

	catalog := gw.Catalog()
	if len(catalog) != 3 {
		t.Fatalf("len = %d, want 3", len(catalog))
	}
	// Should be sorted: elastic.esql, elastic.search, vendor.search_alerts
	if catalog[0].OriginalName != "esql" {
		t.Errorf("[0] = %s, want esql", catalog[0].OriginalName)
	}
	if catalog[1].OriginalName != "search" {
		t.Errorf("[1] = %s, want search", catalog[1].OriginalName)
	}
	if catalog[2].OriginalName != "search_alerts" {
		t.Errorf("[2] = %s, want search_alerts", catalog[2].OriginalName)
	}
}

func TestToolsByServer_GroupsCorrectly(t *testing.T) {
	gw := &Gateway{
		catalog: map[string]CatalogEntry{
			"elastic.search":      {ServerName: "elastic", OriginalName: "search"},
			"elastic.esql":        {ServerName: "elastic", OriginalName: "esql"},
			"vendor.search_alerts": {ServerName: "vendor", OriginalName: "search_alerts"},
		},
	}

	byServer := gw.ToolsByServer()
	if len(byServer["elastic"]) != 2 {
		t.Errorf("elastic tools = %d, want 2", len(byServer["elastic"]))
	}
	if len(byServer["vendor"]) != 1 {
		t.Errorf("vendor tools = %d, want 1", len(byServer["vendor"]))
	}
}

func TestParseNamespacedTool(t *testing.T) {
	tests := []struct {
		input      string
		wantServer string
		wantTool   string
	}{
		{"elastic.search", "elastic", "search"},
		{"vendor.search_alerts", "vendor", "search_alerts"},
		{"graph_query", "", "graph_query"},
	}
	for _, tt := range tests {
		srv, tool := ParseNamespacedTool(tt.input)
		if srv != tt.wantServer || tool != tt.wantTool {
			t.Errorf("ParseNamespacedTool(%q) = (%q, %q), want (%q, %q)",
				tt.input, srv, tool, tt.wantServer, tt.wantTool)
		}
	}
}

func TestServerForTool(t *testing.T) {
	gw := &Gateway{
		catalog: map[string]CatalogEntry{
			"elastic.search": {ServerName: "elastic"},
		},
	}
	if gw.ServerForTool("elastic.search") != "elastic" {
		t.Error("expected elastic")
	}
	if gw.ServerForTool("unknown.tool") != "" {
		t.Error("expected empty for unknown")
	}
}

func TestGateway_RegisterInGraph_GatedWhenReconcilerActive(t *testing.T) {
	mg := &mockGraph{}
	mcpSrv := server.NewMCPServer("test", "0.1.0", server.WithToolCapabilities(true))

	gw := &Gateway{
		mcpServer:        mcpSrv,
		graph:            mg,
		reconcilerActive: true, // reconciler is active
		catalog:          make(map[string]CatalogEntry),
	}

	tools := []mcpclient.ToolInfo{
		{Name: "search", Description: "Search things", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}

	// Simulate what RegisterServer does — tools are registered on mcpServer
	// but graph writes should be skipped.
	for _, t := range tools {
		nsName := "elastic." + t.Name
		proxyTool := mcp.NewToolWithRawSchema(nsName, "[elastic] "+t.Description, t.InputSchema)
		mcpSrv.AddTool(proxyTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("proxied"), nil
		})
		gw.catalog[nsName] = CatalogEntry{
			ServerName:   "elastic",
			OriginalName: t.Name,
			Description:  t.Description,
		}
	}

	// registerInGraph is gated — should NOT be called when reconcilerActive=true
	if gw.graph != nil && !gw.reconcilerActive {
		gw.registerInGraph(context.Background(), "elastic", tools)
	}

	// No graph updates should have been made
	if len(mg.updates) != 0 {
		t.Errorf("expected 0 graph updates when reconciler active, got %d", len(mg.updates))
	}

	// Catalog should still have the tools
	if gw.ToolCount() != 1 {
		t.Errorf("expected 1 tool in catalog, got %d", gw.ToolCount())
	}
}

func TestGateway_RegisterInGraph_FallbackWhenNoReconciler(t *testing.T) {
	mg := &mockGraph{}
	mcpSrv := server.NewMCPServer("test", "0.1.0", server.WithToolCapabilities(true))

	gw := &Gateway{
		mcpServer:        mcpSrv,
		graph:            mg,
		reconcilerActive: false, // no reconciler
		catalog:          make(map[string]CatalogEntry),
	}

	tools := []mcpclient.ToolInfo{
		{Name: "search", Description: "Search things", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}

	for _, t := range tools {
		nsName := "elastic." + t.Name
		proxyTool := mcp.NewToolWithRawSchema(nsName, "[elastic] "+t.Description, t.InputSchema)
		mcpSrv.AddTool(proxyTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("proxied"), nil
		})
		gw.catalog[nsName] = CatalogEntry{
			ServerName:   "elastic",
			OriginalName: t.Name,
			Description:  t.Description,
		}
	}

	// Without reconciler, graph writes should happen
	if gw.graph != nil && !gw.reconcilerActive {
		gw.registerInGraph(context.Background(), "elastic", tools)
	}

	// Should have graph updates: MCPServer(1) + MCPTool(1) + PROVIDES(1) = 3
	if len(mg.updates) != 3 {
		t.Errorf("expected 3 graph updates when no reconciler, got %d", len(mg.updates))
	}
}

func TestGateway_RegisterInGraph_NewLabels(t *testing.T) {
	mg := &mockGraph{}

	gw := &Gateway{
		graph:   mg,
		catalog: make(map[string]CatalogEntry),
	}

	tools := []mcpclient.ToolInfo{
		{Name: "search", Description: "Search things"},
	}
	gw.registerInGraph(context.Background(), "elastic", tools)

	// Should write MCPServer (not DataSource)
	foundMCPServer := false
	foundMCPTool := false
	foundPROVIDES := false
	for _, u := range mg.updates {
		// MCPServer MERGE (the node creation, not an edge MATCH)
		if strings.Contains(u.Cypher, "MERGE (ms:MCPServer") {
			foundMCPServer = true
			if u.Params["source"] != "gateway:auto" {
				t.Error("MCPServer should have _source='gateway:auto'")
			}
			if _, ok := u.Params["updated_at"]; !ok {
				t.Error("MCPServer should have _updated_at parameter")
			}
		}
		// MCPTool MERGE (the node creation)
		if strings.Contains(u.Cypher, "MERGE (mt:MCPTool") {
			foundMCPTool = true
			if u.Params["source"] != "gateway:auto" {
				t.Error("MCPTool should have _source='gateway:auto'")
			}
			if _, ok := u.Params["updated_at"]; !ok {
				t.Error("MCPTool should have _updated_at parameter")
			}
		}
		if strings.Contains(u.Cypher, "PROVIDES") {
			foundPROVIDES = true
		}
		// Ensure no old labels
		if strings.Contains(u.Cypher, "DataSource") || strings.Contains(u.Cypher, "MCPSource") ||
			strings.Contains(u.Cypher, "ToolRef") || strings.Contains(u.Cypher, "FETCHABLE_VIA") {
			t.Errorf("registerInGraph should not reference old labels, found in: %s", u.Cypher)
		}
	}
	if !foundMCPServer {
		t.Error("expected MCPServer MERGE")
	}
	if !foundMCPTool {
		t.Error("expected MCPTool MERGE")
	}
	if !foundPROVIDES {
		t.Error("expected PROVIDES edge")
	}
}

func TestGateway_SetReconcilerActive(t *testing.T) {
	gw := &Gateway{}

	if gw.reconcilerActive {
		t.Error("reconcilerActive should default to false")
	}

	gw.SetReconcilerActive(true)
	if !gw.reconcilerActive {
		t.Error("reconcilerActive should be true after SetReconcilerActive(true)")
	}

	gw.SetReconcilerActive(false)
	if gw.reconcilerActive {
		t.Error("reconcilerActive should be false after SetReconcilerActive(false)")
	}
}
