package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerCPGraphTools registers the 5 core graph MCP tools via the CP proxy path.
// Handlers delegate to cpClient.Graph*() methods instead of a direct GraphClient.
// Uses the same shared tool definitions as the direct path (graph_tool_defs.go).
// Does NOT register graph_checkpoint (deferred to a future spec; not Spec-38).
func registerCPGraphTools(m *server.MCPServer, cpClient cpapi.ControlPlaneClient) {
	m.AddTool(graphQueryTool, makeCPGraphQueryHandler(cpClient))
	m.AddTool(graphUpdateTool, makeCPGraphUpdateHandler(cpClient))
	m.AddTool(graphSchemaTool, makeCPGraphSchemaHandler(cpClient))
	m.AddTool(graphPathTool, makeCPGraphPathHandler(cpClient))
	m.AddTool(graphNeighborsTool, makeCPGraphNeighborsHandler(cpClient))
}

func makeCPGraphQueryHandler(cpClient cpapi.ControlPlaneClient) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		cypher, err := request.RequireString("cypher")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: cypher"), nil
		}

		params, err := parseParams(request)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		resp, err := cpClient.GraphQuery(ctx, cpapi.GraphQueryRequest{
			Cypher: cypher,
			Params: params,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("graph query failed: %v", err)), nil
		}

		data, err := json.Marshal(resp.Records)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshalling results: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

func makeCPGraphUpdateHandler(cpClient cpapi.ControlPlaneClient) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

		_, err = cpClient.GraphUpdate(ctx, cpapi.GraphUpdateRequest{
			Cypher:         cypher,
			Params:         params,
			Source:         source,
			Confidence:     confidence,
			CorrelationKey: correlationKey,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("graph update failed: %v", err)), nil
		}

		return mcp.NewToolResultText("Update executed successfully."), nil
	}
}

func makeCPGraphSchemaHandler(cpClient cpapi.ControlPlaneClient) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resp, err := cpClient.GraphSchema(ctx, cpapi.GraphSchemaRequest{})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("graph schema failed: %v", err)), nil
		}

		type schemaResult struct {
			Labels            []string `json:"labels"`
			RelationshipTypes []string `json:"relationship_types"`
			PropertyKeys      []string `json:"property_keys"`
		}

		data, err := json.Marshal(schemaResult{
			Labels:            resp.Labels,
			RelationshipTypes: resp.RelationshipTypes,
			PropertyKeys:      resp.PropertyKeys,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshalling schema: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

func makeCPGraphPathHandler(cpClient cpapi.ControlPlaneClient) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

		resp, err := cpClient.GraphPath(ctx, cpapi.GraphPathRequest{
			FromLabel: fromLabel,
			FromKey:   fromKey,
			FromValue: fromValue,
			ToLabel:   toLabel,
			ToKey:     toKey,
			ToValue:   toValue,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("graph path query failed: %v", err)), nil
		}

		if len(resp.Records) == 0 {
			return mcp.NewToolResultText("No path found between the specified nodes."), nil
		}

		data, err := json.Marshal(resp.Records)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshalling path: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

func makeCPGraphNeighborsHandler(cpClient cpapi.ControlPlaneClient) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

		resp, err := cpClient.GraphNeighbors(ctx, cpapi.GraphNeighborsRequest{
			Label:     label,
			Key:       key,
			Value:     value,
			Depth:     depth,
			EdgeTypes: edgeTypes,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("graph neighbors query failed: %v", err)), nil
		}

		if len(resp.Records) == 0 {
			return mcp.NewToolResultText("No neighbors found."), nil
		}

		data, err := json.Marshal(resp.Records)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshalling neighbors: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}
