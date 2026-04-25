package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/schema"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// CheckpointGap describes a single missing graph element.
type CheckpointGap struct {
	Layer           string `json:"layer"`            // "universal" or "particular"
	Type            string `json:"type"`             // gap category
	Description     string `json:"description"`
	SuggestedAction string `json:"suggested_action"`
}

// checkpointResult is the wire format returned by graph_checkpoint.
type checkpointResult struct {
	AllClear bool            `json:"all_clear"`
	GapCount int             `json:"gap_count"`
	Gaps     []CheckpointGap `json:"gaps"`
}

func registerCheckpointTool(m *server.MCPServer, gc graph.GraphClient, rules []schema.CheckpointRule) {
	m.AddTool(mcp.NewTool("graph_checkpoint",
		mcp.WithDescription(
			"Inspect the knowledge graph for missing universal and particular layer entries. "+
				"Call this after every investigation step, external tool query, or finding. "+
				"Returns a structured list of gaps with suggested actions. "+
				"Checks: orphaned semantic values, incomplete DomainSource→DataStore→MCPServer→MCPTool→MCPField "+
				"resolution chains, disconnected MCPTool nodes, and Hunt nodes with no linked findings.",
		),
		mcp.WithString("mcp_servers",
			mcp.Description(
				"Comma-separated MCP server names queried in this session (e.g. 'vendor_mcp,elasticsearch'). "+
					"Used to check whether those servers have MCPTool nodes registered in the graph.",
			),
		),
	), makeGraphCheckpointHandler(gc, rules))
}

func makeGraphCheckpointHandler(gc graph.GraphClient, rules []schema.CheckpointRule) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var gaps []CheckpointGap

		// Built-in 1: Orphaned semantic values (not expressible as a simple YAML rule
		// because the gap template requires the semantic name from a cross-layer check).
		orphanedSemantics, err := gc.Query(ctx,
			`MATCH (n) WHERE n.semantic IS NOT NULL
			 WITH DISTINCT n.semantic AS sem
			 OPTIONAL MATCH (s:Semantic {name: sem})
			 WITH sem WHERE s IS NULL
			 RETURN sem`, nil)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("checkpoint query (orphaned semantics): %v", err)), nil
		}
		for _, r := range orphanedSemantics {
			sem := fmt.Sprint(r["sem"])
			gaps = append(gaps, CheckpointGap{
				Layer:           "universal",
				Type:            "missing_semantic",
				Description:     fmt.Sprintf("Semantic %q is used on a node but not defined in the universal layer", sem),
				SuggestedAction: fmt.Sprintf("MERGE (s:Semantic {name: '%s'}) SET s.description = '<describe>'", sem),
			})
		}

		// YAML-defined rules: iterate each rule, run the query, expand templates per row.
		var errorCount, warningCount int
		for _, rule := range rules {
			rows, err := gc.Query(ctx, rule.Query, nil)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("checkpoint query (%s): %v", rule.Name, err)), nil
			}
			for _, row := range rows {
				gaps = append(gaps, CheckpointGap{
					Layer:           rule.Layer,
					Type:            rule.GapType,
					Description:     expandTemplate(rule.GapDescription, row),
					SuggestedAction: expandTemplate(rule.SuggestedAction, row),
				})
				switch rule.Severity {
				case "error":
					errorCount++
				case "warning":
					warningCount++
				}
			}
		}

		// Built-in 2: Check that explicitly named MCP servers have registered MCPTool nodes.
		serversRaw := request.GetString("mcp_servers", "")
		if serversRaw != "" {
			for _, srv := range strings.Split(serversRaw, ",") {
				srv = strings.TrimSpace(srv)
				if srv == "" {
					continue
				}
				rows, err := gc.Query(ctx,
					`MATCH (m:MCPTool {mcp_server: $srv}) RETURN count(m) AS cnt`,
					map[string]any{"srv": srv})
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("checkpoint query (mcp_server check): %v", err)), nil
				}
				cnt := 0
				if len(rows) > 0 {
					if v, ok := rows[0]["cnt"]; ok {
						switch n := v.(type) {
						case int:
							cnt = n
						case int64:
							cnt = int(n)
						case float64:
							cnt = int(n)
						}
					}
				}
				if cnt == 0 {
					gaps = append(gaps, CheckpointGap{
						Layer:           "universal",
						Type:            "unregistered_mcp_server",
						Description:     fmt.Sprintf("MCP server %q was queried but has no MCPTool nodes in the graph", srv),
						SuggestedAction: fmt.Sprintf("Create MCPTool nodes for each tool on %q and wire them into DomainSource→DataStore→MCPServer→MCPTool→MCPField chain", srv),
					})
				}
			}
		}

		result := checkpointResult{
			AllClear: len(gaps) == 0,
			GapCount: len(gaps),
			Gaps:     gaps,
		}
		if result.Gaps == nil {
			result.Gaps = []CheckpointGap{}
		}

		log := fractalog.Component("checkpoint")
		log.Info("checkpoint complete",
			"gap_count", result.GapCount,
			"errors", errorCount,
			"warnings", warningCount,
		)

		data, err := json.Marshal(result)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshalling checkpoint result: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// expandTemplate replaces {column} placeholders in a template string with
// values from a Cypher result row.
func expandTemplate(tmpl string, row map[string]any) string {
	result := tmpl
	for key, val := range row {
		result = strings.ReplaceAll(result, "{"+key+"}", fmt.Sprint(val))
	}
	return result
}
