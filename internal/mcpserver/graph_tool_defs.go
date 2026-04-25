package mcpserver

import (
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// Shared MCP tool definitions for the 5 core graph tools.
// Used by both graph_tools.go (direct path) and cp_graph_tools.go (CP proxy path)
// to prevent description/schema drift.

var graphQueryTool = mcp.NewTool("graph_query",
	mcp.WithDescription("Run a read-only Cypher query against the knowledge graph. Returns results as JSON array of records. Use for reading nodes, relationships, paths, and aggregations."),
	mcp.WithString("cypher",
		mcp.Description("Cypher query (read-only: MATCH, RETURN, WITH, etc.)"),
		mcp.Required(),
	),
	mcp.WithString("params",
		mcp.Description("Optional JSON object of query parameters (e.g. {\"name\": \"CloudTrail\"})"),
	),
)

var graphUpdateTool = mcp.NewTool("graph_update",
	mcp.WithDescription(
		"Run a write Cypher query against the knowledge graph. Use for CREATE, MERGE, SET, DELETE operations. "+
			"Optional provenance parameters (source, confidence, correlation_key) are injected as Cypher $-parameters. "+
			"Reference them in your query: SET n._source = $source, n._updated_at = $updated_at",
	),
	mcp.WithString("cypher",
		mcp.Description("Cypher write query (CREATE, MERGE, SET, DELETE)"),
		mcp.Required(),
	),
	mcp.WithString("params",
		mcp.Description("Optional JSON object of query parameters"),
	),
	mcp.WithString("source",
		mcp.Description("Writer identity for provenance tracking (e.g. 'agent:hunter', 'user:admin'). Injected as $source; also auto-sets $updated_at."),
	),
	mcp.WithString("confidence",
		mcp.Description("Confidence level: 'high', 'medium', or 'low'. Injected as $confidence."),
	),
	mcp.WithString("correlation_key",
		mcp.Description("Groups related writes (e.g. hunt ID, strategy run ID). Injected as $correlation_key."),
	),
)

var graphSchemaTool = mcp.NewTool("graph_schema",
	mcp.WithDescription("Return all node labels, relationship types, and property keys from the knowledge graph. Use for orientation before writing queries."),
)

var graphPathTool = mcp.NewTool("graph_path",
	mcp.WithDescription("Find the shortest path between two nodes in the knowledge graph."),
	mcp.WithString("from_label",
		mcp.Description("Label of the source node (e.g. DomainSource)"),
		mcp.Required(),
	),
	mcp.WithString("from_key",
		mcp.Description("Property name to identify the source node (e.g. name)"),
		mcp.Required(),
	),
	mcp.WithString("from_value",
		mcp.Description("Property value to identify the source node (e.g. CloudTrail)"),
		mcp.Required(),
	),
	mcp.WithString("to_label",
		mcp.Description("Label of the target node"),
		mcp.Required(),
	),
	mcp.WithString("to_key",
		mcp.Description("Property name to identify the target node"),
		mcp.Required(),
	),
	mcp.WithString("to_value",
		mcp.Description("Property value to identify the target node"),
		mcp.Required(),
	),
)

var graphNeighborsTool = mcp.NewTool("graph_neighbors",
	mcp.WithDescription("Get the neighborhood of a node in the knowledge graph. Returns connected nodes up to a given depth."),
	mcp.WithString("label",
		mcp.Description("Label of the center node (e.g. DomainSource)"),
		mcp.Required(),
	),
	mcp.WithString("key",
		mcp.Description("Property name to identify the node (e.g. name)"),
		mcp.Required(),
	),
	mcp.WithString("value",
		mcp.Description("Property value to identify the node (e.g. CloudTrail)"),
		mcp.Required(),
	),
	mcp.WithString("depth",
		mcp.Description("Traversal depth (default: 1)"),
	),
	mcp.WithString("edge_types",
		mcp.Description("Comma-separated relationship types to follow (e.g. HAS_FIELD,JOINS_WITH). Omit for all types."),
	),
)

// parseParams extracts optional JSON params from a request.
func parseParams(request mcp.CallToolRequest) (map[string]any, error) {
	raw := request.GetString("params", "")
	if raw == "" {
		return nil, nil
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return nil, fmt.Errorf("invalid params JSON: %w", err)
	}
	return params, nil
}
