package agentpolicy

import (
	"sort"
	"testing"
)

func TestExpandFractaTools_LocalMode(t *testing.T) {
	tools := ExpandFractaTools("mcp__fracta-agent__", "", nil)

	// Should have coordination(5) + graph(6) + strategy(9) + discovery(1) = 21 tools
	if len(tools) != 21 {
		t.Errorf("expected 21 tools, got %d: %v", len(tools), tools)
	}

	// Spot check some tools
	assertContains(t, tools, "mcp__fracta-agent__fracta_list")
	assertContains(t, tools, "mcp__fracta-agent__graph_query")
	assertContains(t, tools, "mcp__fracta-agent__strategy_run")
	assertContains(t, tools, "mcp__fracta-agent__search_tool")

	// Should NOT have objective tools
	assertNotContains(t, tools, "mcp__fracta-agent__fracta_propose_mission")
}

func TestExpandFractaTools_GatewayMode(t *testing.T) {
	tools := ExpandFractaTools("mcp__fracta__", "", []string{"mcp__fracta__*"})

	assertContains(t, tools, "mcp__fracta__fracta_list")
	assertContains(t, tools, "mcp__fracta__graph_query")
	assertContains(t, tools, "mcp__fracta__*")
}

func TestExpandFractaTools_WithObjective(t *testing.T) {
	tools := ExpandFractaTools("mcp__fracta-agent__", "obj-123", nil)

	// Should have 21 base + 3 objective = 24 tools
	if len(tools) != 24 {
		t.Errorf("expected 24 tools, got %d", len(tools))
	}

	assertContains(t, tools, "mcp__fracta-agent__fracta_propose_mission")
	assertContains(t, tools, "mcp__fracta-agent__fracta_report_finding")
	assertContains(t, tools, "mcp__fracta-agent__fracta_resolve_objective")
}

func TestExpandFractaTools_WithBackendWildcards(t *testing.T) {
	wildcards := []string{"mcp__elastic__*", "mcp__vendor__*"}
	tools := ExpandFractaTools("mcp__fracta-agent__", "", wildcards)

	assertContains(t, tools, "mcp__elastic__*")
	assertContains(t, tools, "mcp__vendor__*")
}

func TestBackendWildcards_GatewayMode(t *testing.T) {
	wc := BackendWildcards("http://gateway:8080", []string{"elastic", "vendor"})

	if len(wc) != 1 {
		t.Fatalf("gateway mode should return 1 wildcard, got %d", len(wc))
	}
	if wc[0] != "mcp__fracta__*" {
		t.Errorf("got %q, want mcp__fracta__*", wc[0])
	}
}

func TestBackendWildcards_NoGateway(t *testing.T) {
	wc := BackendWildcards("", []string{"elastic", "vendor"})

	if len(wc) != 2 {
		t.Fatalf("expected 2 wildcards, got %d", len(wc))
	}
	sort.Strings(wc)
	if wc[0] != "mcp__elastic__*" {
		t.Errorf("got %q, want mcp__elastic__*", wc[0])
	}
	if wc[1] != "mcp__vendor__*" {
		t.Errorf("got %q, want mcp__vendor__*", wc[1])
	}
}

func TestBackendWildcards_NoServers(t *testing.T) {
	wc := BackendWildcards("", nil)
	if len(wc) != 0 {
		t.Errorf("expected 0 wildcards, got %d", len(wc))
	}
}

func TestMCPPermissionPrefix_Gateway(t *testing.T) {
	p := MCPPermissionPrefix("http://gateway:8080")
	if p != "mcp__fracta__" {
		t.Errorf("got %q, want mcp__fracta__", p)
	}
}

func TestMCPPermissionPrefix_Local(t *testing.T) {
	p := MCPPermissionPrefix("")
	if p != "mcp__fracta-agent__" {
		t.Errorf("got %q, want mcp__fracta-agent__", p)
	}
}

func assertContains(t *testing.T, slice []string, item string) {
	t.Helper()
	for _, s := range slice {
		if s == item {
			return
		}
	}
	t.Errorf("slice does not contain %q", item)
}

func assertNotContains(t *testing.T, slice []string, item string) {
	t.Helper()
	for _, s := range slice {
		if s == item {
			t.Errorf("slice should not contain %q", item)
			return
		}
	}
}
