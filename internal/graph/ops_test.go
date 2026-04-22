package graph

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// --- mock GraphClient for ops tests ---

type mockOpsGraphClient struct {
	queries []mockQuery
	err     error // if non-nil, all queries return this error
}

type mockQuery struct {
	cypher string
	params map[string]any
}

func (m *mockOpsGraphClient) Query(_ context.Context, cypher string, params map[string]any) ([]Record, error) {
	m.queries = append(m.queries, mockQuery{cypher: cypher, params: params})
	if m.err != nil {
		return nil, m.err
	}
	// Return canned data based on the query.
	switch {
	case strings.Contains(cypher, "db.labels"):
		return []Record{{"label": "Foo"}, {"label": "Bar"}}, nil
	case strings.Contains(cypher, "db.relationshipTypes"):
		return []Record{{"relationshipType": "CONNECTS"}}, nil
	case strings.Contains(cypher, "db.propertyKeys"):
		return []Record{{"propertyKey": "name"}, {"propertyKey": "uri"}}, nil
	default:
		return nil, nil
	}
}

func (m *mockOpsGraphClient) Update(_ context.Context, _ string, _ map[string]any) error {
	return m.err
}

func (m *mockOpsGraphClient) Close() error { return nil }

// --- InjectProvenance tests ---

func TestInjectProvenance_HappyPath(t *testing.T) {
	params := map[string]any{"name": "bar"}
	merged, err := InjectProvenance(params, "agent:hunter", "high", "hunt-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if merged["source"] != "agent:hunter" {
		t.Errorf("source = %v", merged["source"])
	}
	if merged["confidence"] != "high" {
		t.Errorf("confidence = %v", merged["confidence"])
	}
	if merged["correlation_key"] != "hunt-123" {
		t.Errorf("correlation_key = %v", merged["correlation_key"])
	}
	// updated_at should be present and RFC3339
	ua, ok := merged["updated_at"]
	if !ok {
		t.Fatal("expected updated_at to be auto-injected")
	}
	if _, err := time.Parse(time.RFC3339, ua.(string)); err != nil {
		t.Errorf("updated_at not RFC3339: %v", ua)
	}
	// Original param preserved.
	if merged["name"] != "bar" {
		t.Errorf("user param name = %v", merged["name"])
	}
}

func TestInjectProvenance_ReservedKeyConflict(t *testing.T) {
	params := map[string]any{"source": "user-injected"}
	_, err := InjectProvenance(params, "agent:hunter", "", "")
	if err == nil {
		t.Fatal("expected error for reserved key conflict")
	}
	if !strings.Contains(err.Error(), "reserved provenance key") {
		t.Errorf("expected reserved key error, got: %v", err)
	}
}

func TestInjectProvenance_NoProvenance(t *testing.T) {
	params := map[string]any{"name": "bar"}
	merged, err := InjectProvenance(params, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return original params unchanged.
	if len(merged) != 1 || merged["name"] != "bar" {
		t.Errorf("expected original params, got %v", merged)
	}
}

func TestInjectProvenance_NilParams(t *testing.T) {
	merged, err := InjectProvenance(nil, "agent:x", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged["source"] != "agent:x" {
		t.Errorf("source = %v", merged["source"])
	}
	if _, ok := merged["updated_at"]; !ok {
		t.Error("expected updated_at")
	}
}

func TestInjectProvenance_SourceOnlySetsUpdatedAt(t *testing.T) {
	merged, err := InjectProvenance(nil, "user:admin", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := merged["updated_at"]; !ok {
		t.Error("expected updated_at when source is set")
	}
	if _, ok := merged["confidence"]; ok {
		t.Error("confidence should not be present when not set")
	}
}

func TestInjectProvenance_ConfidenceOnlyNoUpdatedAt(t *testing.T) {
	merged, err := InjectProvenance(nil, "", "low", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged["confidence"] != "low" {
		t.Errorf("confidence = %v", merged["confidence"])
	}
	if _, ok := merged["updated_at"]; ok {
		t.Error("updated_at should not be set when source is empty")
	}
}

// --- QuerySchema tests ---

func TestQuerySchema_HappyPath(t *testing.T) {
	gc := &mockOpsGraphClient{}
	result, err := QuerySchema(context.Background(), gc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Labels) != 2 || result.Labels[0] != "Foo" || result.Labels[1] != "Bar" {
		t.Errorf("labels = %v", result.Labels)
	}
	if len(result.RelationshipTypes) != 1 || result.RelationshipTypes[0] != "CONNECTS" {
		t.Errorf("relationship types = %v", result.RelationshipTypes)
	}
	if len(result.PropertyKeys) != 2 || result.PropertyKeys[0] != "name" || result.PropertyKeys[1] != "uri" {
		t.Errorf("property keys = %v", result.PropertyKeys)
	}
	if len(gc.queries) != 3 {
		t.Errorf("expected 3 queries, got %d", len(gc.queries))
	}
}

func TestQuerySchema_Error(t *testing.T) {
	gc := &mockOpsGraphClient{err: fmt.Errorf("connection refused")}
	_, err := QuerySchema(context.Background(), gc)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "fetching labels") {
		t.Errorf("expected labels error, got: %v", err)
	}
}

// --- BuildPathQuery tests ---

func TestBuildPathQuery_HappyPath(t *testing.T) {
	cypher, params, err := BuildPathQuery("DomainSource", "name", "CloudTrail", "MCPTool", "name", "search")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(cypher, "DomainSource") || !strings.Contains(cypher, "MCPTool") {
		t.Errorf("cypher missing labels: %s", cypher)
	}
	if !strings.Contains(cypher, "shortestPath") {
		t.Errorf("cypher missing shortestPath: %s", cypher)
	}
	if params["from_val"] != "CloudTrail" {
		t.Errorf("from_val = %v", params["from_val"])
	}
	if params["to_val"] != "search" {
		t.Errorf("to_val = %v", params["to_val"])
	}
}

func TestBuildPathQuery_InvalidLabel(t *testing.T) {
	_, _, err := BuildPathQuery("DROP TABLE", "name", "x", "Foo", "name", "y")
	if err == nil {
		t.Fatal("expected validation error for bad label")
	}
	if !strings.Contains(err.Error(), "invalid label") {
		t.Errorf("expected label validation error, got: %v", err)
	}
}

func TestBuildPathQuery_InvalidKey(t *testing.T) {
	_, _, err := BuildPathQuery("Foo", "bad key!", "x", "Bar", "name", "y")
	if err == nil {
		t.Fatal("expected validation error for bad key")
	}
	if !strings.Contains(err.Error(), "invalid property key") {
		t.Errorf("expected property key validation error, got: %v", err)
	}
}

// --- BuildNeighborsQuery tests ---

func TestBuildNeighborsQuery_HappyPath(t *testing.T) {
	cypher, params, err := BuildNeighborsQuery("DomainSource", "name", "CloudTrail", 2, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(cypher, "DomainSource") {
		t.Errorf("cypher missing label: %s", cypher)
	}
	if !strings.Contains(cypher, "*1..2") {
		t.Errorf("cypher missing depth: %s", cypher)
	}
	if params["val"] != "CloudTrail" {
		t.Errorf("val = %v", params["val"])
	}
}

func TestBuildNeighborsQuery_DepthCoercion(t *testing.T) {
	cypher, _, err := BuildNeighborsQuery("Foo", "name", "x", 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(cypher, "*1..1") {
		t.Errorf("expected depth coerced to 1, got: %s", cypher)
	}

	cypher, _, err = BuildNeighborsQuery("Foo", "name", "x", -5, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(cypher, "*1..1") {
		t.Errorf("expected negative depth coerced to 1, got: %s", cypher)
	}
}

func TestBuildNeighborsQuery_WithEdgeTypes(t *testing.T) {
	cypher, _, err := BuildNeighborsQuery("Foo", "name", "x", 3, []string{"STORED_IN", "PROVIDES"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(cypher, ":STORED_IN|:PROVIDES") {
		t.Errorf("cypher missing edge types: %s", cypher)
	}
	if !strings.Contains(cypher, "*1..3") {
		t.Errorf("cypher missing depth: %s", cypher)
	}
}

func TestBuildNeighborsQuery_InvalidEdgeType(t *testing.T) {
	_, _, err := BuildNeighborsQuery("Foo", "name", "x", 1, []string{"GOOD", "bad type!"})
	if err == nil {
		t.Fatal("expected validation error for bad edge type")
	}
	if !strings.Contains(err.Error(), "invalid edge type") {
		t.Errorf("expected edge type error, got: %v", err)
	}
}

func TestBuildNeighborsQuery_InvalidLabel(t *testing.T) {
	_, _, err := BuildNeighborsQuery("bad label", "name", "x", 1, nil)
	if err == nil {
		t.Fatal("expected validation error for bad label")
	}
}

// --- RecordsToMaps tests ---

func TestRecordsToMaps(t *testing.T) {
	records := []Record{
		{"name": "foo", "count": 42},
		{"name": "bar"},
	}
	maps := RecordsToMaps(records)
	if len(maps) != 2 {
		t.Fatalf("expected 2 maps, got %d", len(maps))
	}
	if maps[0]["name"] != "foo" || maps[0]["count"] != 42 {
		t.Errorf("first map = %v", maps[0])
	}
	if maps[1]["name"] != "bar" {
		t.Errorf("second map = %v", maps[1])
	}
}

func TestRecordsToMaps_Empty(t *testing.T) {
	maps := RecordsToMaps(nil)
	if len(maps) != 0 {
		t.Errorf("expected empty slice, got %v", maps)
	}
}
