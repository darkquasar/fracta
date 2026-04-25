package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestGraphUpdate_ProvenanceInjection(t *testing.T) {
	gc := &mockGraphClient{}
	handler := makeGraphUpdateHandler(gc)

	reqJSON := `{"method":"tools/call","params":{"name":"graph_update","arguments":{
		"cypher": "MERGE (n:Foo {name: $name}) SET n._source = $source, n._updated_at = $updated_at",
		"params": "{\"name\": \"bar\"}",
		"source": "agent:hunter",
		"confidence": "high",
		"correlation_key": "hunt-123"
	}}}`
	var req mcp.CallToolRequest
	if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "successfully") {
		t.Errorf("expected success, got: %s", text)
	}

	if len(gc.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(gc.updates))
	}

	params := gc.updates[0].params
	if params["source"] != "agent:hunter" {
		t.Errorf("source = %v", params["source"])
	}
	if params["confidence"] != "high" {
		t.Errorf("confidence = %v", params["confidence"])
	}
	if params["correlation_key"] != "hunt-123" {
		t.Errorf("correlation_key = %v", params["correlation_key"])
	}
	if _, ok := params["updated_at"]; !ok {
		t.Error("expected updated_at to be auto-injected")
	}
	if params["name"] != "bar" {
		t.Errorf("user param name = %v", params["name"])
	}
}

func TestGraphUpdate_ProvenanceConflict(t *testing.T) {
	gc := &mockGraphClient{}
	handler := makeGraphUpdateHandler(gc)

	// User params contain "source" which conflicts with the provenance param.
	reqJSON := `{"method":"tools/call","params":{"name":"graph_update","arguments":{
		"cypher": "MERGE (n:Foo {name: 'x'})",
		"params": "{\"source\": \"user-injected\"}",
		"source": "agent:hunter"
	}}}`
	var req mcp.CallToolRequest
	json.Unmarshal([]byte(reqJSON), &req)

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "reserved provenance key") {
		t.Errorf("expected conflict error, got: %s", text)
	}

	if len(gc.updates) != 0 {
		t.Error("should not have called Update on conflict")
	}
}

func TestGraphUpdate_NoProvenance(t *testing.T) {
	gc := &mockGraphClient{}
	handler := makeGraphUpdateHandler(gc)

	reqJSON := `{"method":"tools/call","params":{"name":"graph_update","arguments":{
		"cypher": "CREATE (n:Foo {name: 'x'})"
	}}}`
	var req mcp.CallToolRequest
	json.Unmarshal([]byte(reqJSON), &req)

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "successfully") {
		t.Errorf("expected success, got: %s", text)
	}

	if len(gc.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(gc.updates))
	}
	// No provenance params should be injected.
	if gc.updates[0].params != nil {
		t.Errorf("expected nil params, got %v", gc.updates[0].params)
	}
}

func TestGraphUpdate_SourceAutoSetsUpdatedAt(t *testing.T) {
	gc := &mockGraphClient{}
	handler := makeGraphUpdateHandler(gc)

	// Only source provided, no confidence/correlation_key.
	reqJSON := `{"method":"tools/call","params":{"name":"graph_update","arguments":{
		"cypher": "MERGE (n:Foo {name: 'x'}) SET n._source = $source",
		"source": "user:admin"
	}}}`
	var req mcp.CallToolRequest
	json.Unmarshal([]byte(reqJSON), &req)

	handler(context.Background(), req)

	params := gc.updates[0].params
	if params["source"] != "user:admin" {
		t.Errorf("source = %v", params["source"])
	}
	if _, ok := params["updated_at"]; !ok {
		t.Error("expected updated_at when source is set")
	}
	// confidence and correlation_key should not be present.
	if _, ok := params["confidence"]; ok {
		t.Error("confidence should not be present when not set")
	}
	if _, ok := params["correlation_key"]; ok {
		t.Error("correlation_key should not be present when not set")
	}
}
