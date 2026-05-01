// Package agentpolicy owns fracta platform semantics that apply to ALL host
// adapters. It extracts host-neutral logic (tool expansion, MCP topology,
// instruction section resolution) so that multiple hosts can consume shared
// platform decisions without duplicating code.
package agentpolicy

import "fmt"

// ExpandFractaTools returns the fracta platform tool permission strings that
// should be granted to an agent. The prefix is host-specific (e.g.
// "mcp__fracta__" in gateway mode, "mcp__fracta-agent__" in local mode).
//
// backendWildcards contains additional per-backend wildcards (e.g.
// "mcp__elastic__*") that the caller computes from gateway/server config.
//
// This is a direct lift of the tool expansion logic from claude/delivery.go.
func ExpandFractaTools(prefix, objectiveID string, backendWildcards []string) []string {
	tools := []string{
		// Coordination
		prefix + "fracta_list",
		prefix + "fracta_peek",
		prefix + "fracta_send",
		prefix + "fracta_inbox",
		prefix + "fracta_set_intent",
		// Knowledge graph
		prefix + "graph_query",
		prefix + "graph_update",
		prefix + "graph_schema",
		prefix + "graph_path",
		prefix + "graph_neighbors",
		prefix + "graph_checkpoint",
		// Strategy engine
		prefix + "strategy_list",
		prefix + "strategy_match",
		prefix + "strategy_describe",
		prefix + "strategy_run",
		prefix + "strategy_create",
		prefix + "strategy_stage",
		prefix + "strategy_stage_status",
		prefix + "strategy_resolve",
		prefix + "strategy_promote",
		// Tool discovery
		prefix + "search_tool",
	}

	if objectiveID != "" {
		tools = append(tools,
			prefix+"fracta_propose_mission",
			prefix+"fracta_report_finding",
			prefix+"fracta_resolve_objective",
		)
	}

	tools = append(tools, backendWildcards...)
	return tools
}

// BackendWildcards returns the MCP backend tool wildcards based on gateway
// mode. In gateway mode, all backends are proxied through "mcp__fracta__*".
// In non-gateway mode, each server gets its own wildcard "mcp__<name>__*".
func BackendWildcards(gatewayURL string, serverNames []string) []string {
	if gatewayURL != "" {
		return []string{"mcp__fracta__*"}
	}
	wildcards := make([]string, 0, len(serverNames))
	for _, name := range serverNames {
		wildcards = append(wildcards, fmt.Sprintf("mcp__%s__*", name))
	}
	return wildcards
}

// MCPPermissionPrefix returns the permission prefix based on gateway mode.
// Gateway mode uses "mcp__fracta__" (single gateway entry), non-gateway uses
// "mcp__fracta-agent__" (local subprocess entry).
func MCPPermissionPrefix(gatewayURL string) string {
	if gatewayURL != "" {
		return "mcp__fracta__"
	}
	return "mcp__fracta-agent__"
}
