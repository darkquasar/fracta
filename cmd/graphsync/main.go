// One-shot graph knowledge sync: copies scaffold/discovered nodes and edges
// from a source FalkorDB to a target FalkorDB. Inventory nodes (MCPTool,
// MCPServer) are NOT copied — the target reconciler manages those.
//
// Usage: go run ./cmd/graphsync <source-addr> <target-addr>
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/darkquasar/fracta/internal/graph"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: graphsync <source-addr> <target-addr>")
		os.Exit(1)
	}
	srcAddr, dstAddr := os.Args[1], os.Args[2]

	src := graph.NewFalkorDBClient(srcAddr, graph.WithGraphName("fracta_knowledge"))
	dst := graph.NewFalkorDBClient(dstAddr, graph.WithGraphName("fracta_knowledge"))
	defer src.Close()
	defer dst.Close()

	ctx := context.Background()
	for _, c := range []struct {
		name string
		c    *graph.FalkorDBClient
		addr string
	}{{"source", src, srcAddr}, {"target", dst, dstAddr}} {
		if err := c.c.Ping(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "cannot reach %s FalkorDB at %s: %v\n", c.name, c.addr, err)
			os.Exit(1)
		}
	}

	// Scaffold nodes to sync — query individual properties (not properties() which
	// FalkorDB returns in a format Go parseResult can't handle as map[string]interface{})
	type nodeSync struct {
		label      string
		key        string
		returnCols string // Cypher RETURN clause listing columns
		props      []string
	}

	nodes := []nodeSync{
		{"DomainSource", "name",
			"n.name AS name, n.description AS description, n._source AS _source, n._updated_at AS _updated_at, n._migrated_from AS _migrated_from",
			[]string{"name", "description", "_source", "_updated_at", "_migrated_from"}},
		{"DataStore", "uri",
			"n.uri AS uri, n.type AS type, n.description AS description, n.index_pattern AS index_pattern, n._source AS _source, n._status AS _status, n._updated_at AS _updated_at, n._migrated_from AS _migrated_from",
			[]string{"uri", "type", "description", "index_pattern", "_source", "_status", "_updated_at", "_migrated_from"}},
		{"MCPField", "name",
			"n.name AS name, n.type AS type, n.semantic AS semantic, n._source AS _source, n._status AS _status, n._updated_at AS _updated_at",
			[]string{"name", "type", "semantic", "_source", "_status", "_updated_at"}},
		{"Semantic", "name",
			"n.name AS name, n.description AS description",
			[]string{"name", "description"}},
		{"FieldType", "name",
			"n.name AS name, n.data_type AS data_type, n.semantic AS semantic, n._source AS _source, n._status AS _status",
			[]string{"name", "data_type", "semantic", "_source", "_status"}},
	}

	totalNodes := 0
	for _, ns := range nodes {
		query := fmt.Sprintf("MATCH (n:%s) RETURN %s", ns.label, ns.returnCols)
		records, err := src.Query(ctx, query, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "query %s: %v\n", ns.label, err)
			continue
		}

		synced := 0
		for _, rec := range records {
			keyVal := rec[ns.key]
			if keyVal == nil {
				continue
			}

			params := map[string]any{"key_val": keyVal}
			var setClauses []string
			for _, p := range ns.props {
				if v, exists := rec[p]; exists && v != nil && p != ns.key {
					paramName := "p_" + strings.ReplaceAll(p, ".", "_")
					params[paramName] = v
					setClauses = append(setClauses, fmt.Sprintf("n.%s = $%s", p, paramName))
				}
			}

			cypher := fmt.Sprintf("MERGE (n:%s {%s: $key_val})", ns.label, ns.key)
			if len(setClauses) > 0 {
				cypher += " ON CREATE SET " + strings.Join(setClauses, ", ")
			}

			if err := dst.Update(ctx, cypher, params); err != nil {
				fmt.Fprintf(os.Stderr, "  MERGE %s %v: %v\n", ns.label, keyVal, err)
				continue
			}
			synced++
		}
		totalNodes += synced
		fmt.Printf("synced %d %s nodes\n", synced, ns.label)
	}

	// Edges to sync (scaffold edges — not PROVIDES which is inventory)
	type edgeSync struct {
		edgeType  string
		fromLabel string
		fromKey   string
		toLabel   string
		toKey     string
	}

	edges := []edgeSync{
		{"STORED_IN", "DomainSource", "name", "DataStore", "uri"},
		{"QUERYABLE_VIA", "DataStore", "uri", "MCPServer", "config_key"},
		{"RETURNS_FIELD", "MCPTool", "name", "MCPField", "name"},
		{"HAS_FIELD", "DomainSource", "name", "FieldType", "name"},
	}

	totalEdges := 0
	for _, et := range edges {
		query := fmt.Sprintf(
			"MATCH (a:%s)-[r:%s]->(b:%s) RETURN a.%s AS from_key, b.%s AS to_key",
			et.fromLabel, et.edgeType, et.toLabel, et.fromKey, et.toKey,
		)
		records, err := src.Query(ctx, query, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "query %s edges: %v\n", et.edgeType, err)
			continue
		}

		synced := 0
		for _, rec := range records {
			fromVal := rec["from_key"]
			toVal := rec["to_key"]
			if fromVal == nil || toVal == nil {
				continue
			}
			cypher := fmt.Sprintf(
				"MATCH (a:%s {%s: $from_val}) MATCH (b:%s {%s: $to_val}) MERGE (a)-[:%s]->(b)",
				et.fromLabel, et.fromKey, et.toLabel, et.toKey, et.edgeType,
			)
			if err := dst.Update(ctx, cypher, map[string]any{"from_val": fromVal, "to_val": toVal}); err != nil {
				fmt.Fprintf(os.Stderr, "  MERGE %s %v→%v: %v\n", et.edgeType, fromVal, toVal, err)
				continue
			}
			synced++
		}
		totalEdges += synced
		fmt.Printf("synced %d %s edges\n", synced, et.edgeType)
	}

	fmt.Printf("\nSync complete: %d nodes, %d edges\n", totalNodes, totalEdges)
}
