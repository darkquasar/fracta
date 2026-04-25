package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/darkquasar/fracta/internal/gateway"
	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// catalogProvider abstracts the gateway catalog for search_tool, enabling
// handler-level testing with a stub catalog.
type catalogProvider interface {
	Catalog() []gateway.CatalogEntry
}

// registerSearchTool registers the search_tool MCP tool for graph-backed
// tool discovery. LLM-agnostic — works with any LLM that can call MCP tools.
func registerSearchTool(m *server.MCPServer, gc graph.GraphClient, gw *gateway.Gateway) {
	m.AddTool(mcp.NewTool("search_tool",
		mcp.WithDescription(
			"Search for relevant MCP tools by concept, data source, strategy, or keyword. "+
				"Call this before using unfamiliar tools to find the right ones for your task. "+
				"Returns only currently callable tools."),
		mcp.WithString("query",
			mcp.Description("Keyword search on tool names and descriptions"),
		),
		mcp.WithString("semantic",
			mcp.Description("Semantic concept (e.g., 'ip_address', 'credential_theft')"),
		),
		mcp.WithString("source",
			mcp.Description("Domain source name (e.g., 'AWS CloudTrail', 'VendorSecurity Alerts')"),
		),
		mcp.WithString("strategy",
			mcp.Description("Strategy name to find its required tools"),
		),
	), makeSearchToolHandler(gc, gw))
}

type searchToolResult struct {
	Tools     []searchToolEntry `json:"tools"`
	QueryPath string            `json:"query_path"`
}

type searchToolEntry struct {
	Name        string            `json:"name"`
	Server      string            `json:"server"`
	Description string            `json:"description"`
	MatchType   string            `json:"match_type"`      // "graph", "keyword", "catalog"
	Grounded    bool              `json:"grounded"`         // true if result came from graph edges (semantic knowledge exists)
	Fields      []searchToolField `json:"fields,omitempty"` // matched fields (semantic mode)
}

// searchToolField carries MCPField details for semantic-mode results.
type searchToolField struct {
	Name     string `json:"name"`
	Semantic string `json:"semantic"`
}

// buildCatalogNameSet builds a set of callable tool names from the gateway catalog.
func buildCatalogNameSet(catalog []gateway.CatalogEntry) map[string]bool {
	set := make(map[string]bool, len(catalog))
	for _, e := range catalog {
		set[e.ServerName+"."+e.OriginalName] = true
	}
	return set
}

// filterCallable intersects tool entries with the gateway catalog,
// returning only currently callable results.
func filterCallable(tools []searchToolEntry, catalogSet map[string]bool) []searchToolEntry {
	var callable []searchToolEntry
	for _, t := range tools {
		if catalogSet[t.Name] {
			callable = append(callable, t)
		}
	}
	return callable
}

func makeSearchToolHandler(gc graph.GraphClient, cp catalogProvider) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		semantic := req.GetString("semantic", "")
		source := req.GetString("source", "")
		strategyName := req.GetString("strategy", "")
		query := req.GetString("query", "")

		catalogSet := buildCatalogNameSet(cp.Catalog())

		var tools []searchToolEntry
		var queryPath string

		switch {
		case semantic != "":
			// By semantic concept: MCPField{semantic} ← RETURNS_FIELD ← MCPTool
			records, err := gc.Query(ctx,
				`MATCH (f:MCPField {semantic: $sem})<-[:RETURNS_FIELD]-(mt:MCPTool)
				 RETURN DISTINCT mt.name AS tool, mt.mcp_server AS server, mt.description AS description,
				        f.name AS field_name, f.semantic AS field_semantic`,
				map[string]any{"sem": semantic})
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("graph query failed: %v", err)), nil
			}
			tools = recordsToEntriesWithFields(records)
			tools = filterCallable(tools, catalogSet)
			queryPath = fmt.Sprintf("Semantic(%s) → MCPField → MCPTool", semantic)

		case source != "":
			// By domain source: DomainSource → STORED_IN → DataStore → QUERYABLE_VIA → MCPServer → PROVIDES → MCPTool
			records, err := gc.Query(ctx,
				`MATCH (ds:DomainSource {name: $src})-[:STORED_IN]->(store:DataStore)-[:QUERYABLE_VIA]->(srv:MCPServer)-[:PROVIDES]->(mt:MCPTool)
				 RETURN DISTINCT mt.name AS tool, mt.mcp_server AS server, mt.description AS description`,
				map[string]any{"src": source})
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("graph query failed: %v", err)), nil
			}
			tools = recordsToEntries(records, "graph", true)
			tools = filterCallable(tools, catalogSet)
			queryPath = fmt.Sprintf("DomainSource(%s) → DataStore → MCPServer → MCPTool", source)

		case strategyName != "":
			// By strategy: Strategy → USES_TOOL → MCPTool (direct, no ToolRef indirection)
			records, err := gc.Query(ctx,
				`MATCH (s:Strategy {name: $strat})-[:USES_TOOL]->(mt:MCPTool)
				 RETURN mt.name AS tool, mt.mcp_server AS server, mt.description AS description`,
				map[string]any{"strat": strategyName})
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("graph query failed: %v", err)), nil
			}
			allTools := recordsToEntries(records, "graph", true)

			// Strategy mode: callable tools go in results, non-callable noted in query_path diagnostic
			tools = filterCallable(allTools, catalogSet)
			var nonCallable []string
			for _, t := range allTools {
				if !catalogSet[t.Name] {
					nonCallable = append(nonCallable, t.Name)
				}
			}
			queryPath = fmt.Sprintf("Strategy(%s) → USES_TOOL → MCPTool", strategyName)
			if len(nonCallable) > 0 {
				queryPath += fmt.Sprintf(" (non-callable deps: %v)", nonCallable)
			}

		case query != "":
			// Keyword search on MCPTool names and descriptions
			records, err := gc.Query(ctx,
				`MATCH (mt:MCPTool)
				 WHERE mt.name CONTAINS $kw OR mt.description CONTAINS $kw
				 RETURN mt.name AS tool, mt.mcp_server AS server, mt.description AS description
				 LIMIT 10`,
				map[string]any{"kw": query})
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("graph query failed: %v", err)), nil
			}
			tools = recordsToEntries(records, "keyword", false)
			tools = filterCallable(tools, catalogSet)
			queryPath = fmt.Sprintf("Keyword(%s) on MCPTool.name/description", query)

		default:
			// No filter — return gateway catalog summary
			for _, e := range cp.Catalog() {
				tools = append(tools, searchToolEntry{
					Name:        e.ServerName + "." + e.OriginalName,
					Server:      e.ServerName,
					Description: e.Description,
					MatchType:   "catalog",
					Grounded:    false,
				})
			}
			queryPath = "Full catalog (no filter)"
		}

		result := searchToolResult{Tools: tools, QueryPath: queryPath}

		// Determine the search mode for logging
		var mode string
		switch {
		case semantic != "":
			mode = "semantic"
		case source != "":
			mode = "source"
		case strategyName != "":
			mode = "strategy"
		case query != "":
			mode = "keyword"
		default:
			mode = "catalog"
		}
		log := fractalog.Component("search_tool")
		log.Info("search complete", "mode", mode, "result_count", len(tools))

		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil
	}
}

func recordsToEntries(records []graph.Record, matchType string, grounded bool) []searchToolEntry {
	var entries []searchToolEntry
	for _, r := range records {
		entries = append(entries, searchToolEntry{
			Name:        fmt.Sprint(r["tool"]),
			Server:      fmt.Sprint(r["server"]),
			Description: fmt.Sprint(r["description"]),
			MatchType:   matchType,
			Grounded:    grounded,
		})
	}
	return entries
}

// recordsToEntriesWithFields groups graph records by tool and attaches MCPField details.
// Used by semantic mode where each record may carry field_name and field_semantic.
func recordsToEntriesWithFields(records []graph.Record) []searchToolEntry {
	type toolKey struct{ name, server string }
	order := []toolKey{}
	byTool := make(map[toolKey]*searchToolEntry)

	for _, r := range records {
		name := fmt.Sprint(r["tool"])
		srv := fmt.Sprint(r["server"])
		k := toolKey{name, srv}

		entry, exists := byTool[k]
		if !exists {
			entry = &searchToolEntry{
				Name:        name,
				Server:      srv,
				Description: fmt.Sprint(r["description"]),
				MatchType:   "graph",
				Grounded:    true,
			}
			byTool[k] = entry
			order = append(order, k)
		}

		fieldName := fmt.Sprint(r["field_name"])
		fieldSemantic := fmt.Sprint(r["field_semantic"])
		if fieldName != "<nil>" && fieldSemantic != "<nil>" {
			entry.Fields = append(entry.Fields, searchToolField{
				Name:     fieldName,
				Semantic: fieldSemantic,
			})
		}
	}

	entries := make([]searchToolEntry, 0, len(order))
	for _, k := range order {
		entries = append(entries, *byTool[k])
	}
	return entries
}
