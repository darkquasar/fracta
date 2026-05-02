package resolve

import (
	"context"
	"fmt"
	"strings"

	"github.com/darkquasar/fracta/internal/contract"
)

// GraphQuerier is the subset of graph.GraphClient needed by the resolver.
// Defined here (consumption site) per Go interface convention.
type GraphQuerier interface {
	Query(ctx context.Context, cypher string, params map[string]any) ([]map[string]interface{}, error)
}

// Resolver produces ResolutionPlans from contracts and optional bindings.
type Resolver struct {
	graph GraphQuerier
}

// NewResolver creates a Resolver backed by the given graph client.
func NewResolver(graph GraphQuerier) *Resolver {
	return &Resolver{graph: graph}
}

// Resolve produces a ResolutionPlan for the given contract.
// If binding is nil, the resolver uses graph-only resolution (auto-resolve).
// If binding is provided, explicit mappings take precedence over graph lookups.
func (r *Resolver) Resolve(ctx context.Context, cs *contract.ContractSpec, bs *contract.BindingSpec) (*ResolutionPlan, error) {
	plan := &ResolutionPlan{Strategy: cs.Name}

	for tableName, tableSpec := range cs.Requires.Tables {
		tp, warnings, err := r.resolveTable(ctx, tableName, tableSpec, bs)
		if err != nil {
			if tableSpec.Optional {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("optional table %q: %v", tableName, err))
				continue
			}
			return nil, fmt.Errorf("resolve table %q: %w", tableName, err)
		}
		tp.Optional = tableSpec.Optional
		plan.Tables = append(plan.Tables, *tp)
		plan.Warnings = append(plan.Warnings, warnings...)
	}

	return plan, nil
}

func (r *Resolver) resolveTable(
	ctx context.Context,
	tableName string,
	tableSpec contract.TableSpec,
	bs *contract.BindingSpec,
) (*TablePlan, []string, error) {
	// 1. If binding provides an explicit mapping, use it.
	if bs != nil {
		if sb, ok := bs.SourceBindings[tableName]; ok {
			return r.resolveFromBinding(tableName, tableSpec, sb, bs.FieldOverrides[tableName])
		}
	}

	// 2. Auto-resolve via graph: find source -> backend -> field mapping.
	tp, err := r.resolveFromGraph(ctx, tableName, tableSpec)
	return tp, nil, err
}

func (r *Resolver) resolveFromBinding(
	tableName string,
	tableSpec contract.TableSpec,
	sb contract.SourceBinding,
	fieldOverrides map[string]string,
) (*TablePlan, []string, error) {
	var warnings []string

	tp := &TablePlan{
		Table:           tableName,
		Backend:         sb.Backend,
		FetchMode:       sb.FetchModeOrDefault(),
		MCPTool:         sb.MCPTool,
		MCPServer:       sb.MCPServer,
		Query:           sb.Index,
		ResponseFormat:  sb.ResponseFormat,
		ResponseAdapter: sb.ResponseAdapter,
	}

	// Warn if fracta_mcp_gateway targets a tool with "tabular_text" in its name but no adapter
	if tp.FetchMode == "fracta_mcp_gateway" && sb.ResponseFormat == "" && sb.ResponseAdapter == "" && sb.MCPTool != "" {
		if strings.Contains(strings.ToLower(sb.MCPTool), "tabular_text") {
			warnings = append(warnings, fmt.Sprintf(
				"table %q: fracta_mcp_gateway binding targets %q which may return non-JSON output; "+
					"consider adding response_adapter: tabular_text or using fetch_mode: mcp",
				tableName, sb.MCPTool,
			))
		}
	}

	// Use query_template from binding if present (overrides index as query).
	if sb.QueryTemplate != "" {
		tp.Query = sb.QueryTemplate
	}

	// Build field mappings from binding's field_map first.
	if sb.FieldMap != nil {
		for colName, srcField := range sb.FieldMap {
			colType := "VARCHAR"
			if colSpec, ok := tableSpec.Columns[colName]; ok && colSpec.Type != "" {
				colType = colSpec.Type
			}
			tp.Fields = append(tp.Fields, FieldMapping{
				SourceField:  srcField,
				TargetColumn: colName,
				ColumnType:   colType,
			})
		}
	}

	// Supplement with field overrides (semantic-based) for columns not already mapped.
	if fieldOverrides != nil {
		mapped := make(map[string]bool)
		for _, f := range tp.Fields {
			mapped[f.TargetColumn] = true
		}
		for colName, colSpec := range tableSpec.Columns {
			if mapped[colName] || colSpec.Semantic == "" {
				continue
			}
			if srcField, ok := fieldOverrides[colSpec.Semantic]; ok {
				colType := "VARCHAR"
				if colSpec.Type != "" {
					colType = colSpec.Type
				}
				tp.Fields = append(tp.Fields, FieldMapping{
					SourceField:  srcField,
					TargetColumn: colName,
					Semantic:     colSpec.Semantic,
					ColumnType:   colType,
				})
			}
		}
	}

	return tp, warnings, nil
}

func (r *Resolver) resolveFromGraph(
	ctx context.Context,
	tableName string,
	tableSpec contract.TableSpec,
) (*TablePlan, error) {
	// Collect semantics from the table's columns.
	var semantics []string
	for _, col := range tableSpec.Columns {
		if col.Semantic != "" {
			semantics = append(semantics, col.Semantic)
		}
	}
	if len(semantics) == 0 {
		return nil, fmt.Errorf("no semantic tags on columns -- cannot auto-resolve")
	}

	// Step 1: Find DomainSources with matching fields and their backends.
	// 4-tier traversal: DomainSource → STORED_IN → DataStore → QUERYABLE_VIA → MCPServer
	cypher := `
		MATCH (d:DomainSource)-[:HAS_FIELD]->(f:FieldType)
		WHERE f.semantic IN $semantics
		OPTIONAL MATCH (d)-[:STORED_IN]->(store:DataStore)-[:QUERYABLE_VIA]->(srv:MCPServer)
		RETURN d.name AS source, f.name AS field, f.semantic AS semantic,
		       srv.type AS backend, srv.config_key AS config_key,
		       store.uri AS store_uri`

	records, err := r.graph.Query(ctx, cypher, map[string]any{"semantics": semantics})
	if err != nil {
		return nil, fmt.Errorf("graph query: %w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("no domain sources found with semantics %v", semantics)
	}

	// Pick the source that covers the most semantics.
	sourceCoverage := make(map[string][]map[string]interface{})
	for _, rec := range records {
		src := fmt.Sprint(rec["source"])
		sourceCoverage[src] = append(sourceCoverage[src], rec)
	}

	var bestSource string
	var bestRecs []map[string]interface{}
	for src, recs := range sourceCoverage {
		if len(recs) > len(bestRecs) {
			bestSource = src
			bestRecs = recs
		}
	}

	tp := &TablePlan{
		Table:  tableName,
		Source: bestSource,
	}

	// Extract backend info from the first record that has it.
	for _, rec := range bestRecs {
		if b, ok := rec["backend"]; ok && b != nil && fmt.Sprint(b) != "<nil>" {
			tp.Backend = fmt.Sprint(b)
			if uri, ok := rec["store_uri"]; ok && uri != nil && fmt.Sprint(uri) != "<nil>" {
				tp.Query = fmt.Sprint(uri)
			}
			break
		}
	}

	// Step 2: Try MCP tool resolution via 4-tier chain.
	// MCPServer → PROVIDES → MCPTool
	mcpCypher := `
		MATCH (srv:MCPServer)-[:PROVIDES]->(mt:MCPTool)
		WHERE srv.config_key IN $config_keys
		RETURN mt.name AS tool, mt.mcp_server AS mcp_server`

	var configKeys []string
	for _, rec := range bestRecs {
		if ck, ok := rec["config_key"]; ok && ck != nil && fmt.Sprint(ck) != "<nil>" {
			configKeys = append(configKeys, fmt.Sprint(ck))
		}
	}
	if len(configKeys) > 0 {
		mcpRecords, err := r.graph.Query(ctx, mcpCypher, map[string]any{"config_keys": configKeys})
		if err == nil && len(mcpRecords) > 0 {
			first := mcpRecords[0]
			tp.MCPTool = fmt.Sprint(first["tool"])
			tp.MCPServer = fmt.Sprint(first["mcp_server"])
			tp.Backend = "mcp"
		}
	}

	// Build field mappings using semantic matching.
	fieldsBySemantic := make(map[string]string)
	for _, rec := range bestRecs {
		sem := fmt.Sprint(rec["semantic"])
		field := fmt.Sprint(rec["field"])
		fieldsBySemantic[sem] = field
	}

	for colName, colSpec := range tableSpec.Columns {
		if colSpec.Semantic == "" {
			continue
		}
		if srcField, ok := fieldsBySemantic[colSpec.Semantic]; ok {
			colType := "VARCHAR"
			if colSpec.Type != "" {
				colType = colSpec.Type
			}
			tp.Fields = append(tp.Fields, FieldMapping{
				SourceField:  srcField,
				TargetColumn: colName,
				Semantic:     colSpec.Semantic,
				ColumnType:   colType,
			})
		}
	}

	return tp, nil
}
