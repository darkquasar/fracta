package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/darkquasar/fracta/internal/graph"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerGraphTools registers the 5 core graph MCP tools on the given MCPServer.
// Called only when a GraphClient is available. Checkpoint is registered
// separately by callers via registerCheckpointTool.
func registerGraphTools(m *server.MCPServer, gc graph.GraphClient) {
	m.AddTool(graphQueryTool, makeGraphQueryHandler(gc))
	m.AddTool(graphUpdateTool, makeGraphUpdateHandler(gc))
	m.AddTool(graphSchemaTool, makeGraphSchemaHandler(gc))
	m.AddTool(graphPathTool, makeGraphPathHandler(gc))
	m.AddTool(graphNeighborsTool, makeGraphNeighborsHandler(gc))
}

func makeGraphQueryHandler(gc graph.GraphClient) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		cypher, err := request.RequireString("cypher")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: cypher"), nil
		}

		params, err := parseParams(request)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		records, err := gc.Query(ctx, cypher, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("graph query failed: %v", err)), nil
		}

		data, err := json.Marshal(records)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshalling results: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

func makeGraphUpdateHandler(gc graph.GraphClient) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		cypher, err := request.RequireString("cypher")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: cypher"), nil
		}

		params, err := parseParams(request)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		source := request.GetString("source", "")
		confidence := request.GetString("confidence", "")
		correlationKey := request.GetString("correlation_key", "")

		params, err = graph.InjectProvenance(params, source, confidence, correlationKey)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		if err := gc.Update(ctx, cypher, params); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("graph update failed: %v", err)), nil
		}

		return mcp.NewToolResultText("Update executed successfully."), nil
	}
}

func makeGraphSchemaHandler(gc graph.GraphClient) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := graph.QuerySchema(ctx, gc)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		type schemaResult struct {
			Labels            []string `json:"labels"`
			RelationshipTypes []string `json:"relationship_types"`
			PropertyKeys      []string `json:"property_keys"`
		}

		data, err := json.Marshal(schemaResult{
			Labels:            result.Labels,
			RelationshipTypes: result.RelationshipTypes,
			PropertyKeys:      result.PropertyKeys,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshalling schema: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

func makeGraphPathHandler(gc graph.GraphClient) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		fromLabel, err := request.RequireString("from_label")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: from_label"), nil
		}
		fromKey, err := request.RequireString("from_key")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: from_key"), nil
		}
		fromValue, err := request.RequireString("from_value")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: from_value"), nil
		}
		toLabel, err := request.RequireString("to_label")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: to_label"), nil
		}
		toKey, err := request.RequireString("to_key")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: to_key"), nil
		}
		toValue, err := request.RequireString("to_value")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: to_value"), nil
		}

		cypher, params, err := graph.BuildPathQuery(fromLabel, fromKey, fromValue, toLabel, toKey, toValue)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		records, err := gc.Query(ctx, cypher, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("graph path query failed: %v", err)), nil
		}

		if len(records) == 0 {
			return mcp.NewToolResultText("No path found between the specified nodes."), nil
		}

		data, err := json.Marshal(records)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshalling path: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

func makeGraphNeighborsHandler(gc graph.GraphClient) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		label, err := request.RequireString("label")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: label"), nil
		}
		key, err := request.RequireString("key")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: key"), nil
		}
		value, err := request.RequireString("value")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: value"), nil
		}

		depthStr := request.GetString("depth", "1")
		depth, err := strconv.Atoi(depthStr)
		if err != nil || depth < 1 {
			depth = 1
		}

		edgeTypesStr := request.GetString("edge_types", "")
		var edgeTypes []string
		if edgeTypesStr != "" {
			types := strings.Split(edgeTypesStr, ",")
			for i, t := range types {
				types[i] = strings.TrimSpace(t)
			}
			edgeTypes = types
		}

		cypher, params, err := graph.BuildNeighborsQuery(label, key, value, depth, edgeTypes)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		records, err := gc.Query(ctx, cypher, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("graph neighbors query failed: %v", err)), nil
		}

		if len(records) == 0 {
			return mcp.NewToolResultText("No neighbors found."), nil
		}

		data, err := json.Marshal(records)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshalling neighbors: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}
