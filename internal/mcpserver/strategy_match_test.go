package mcpserver

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/darkquasar/fracta/internal/graph"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestExtractKeywords(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"lateral movement detection from DNS logs", []string{"lateral", "movement", "detection", "dns", "logs"}},
		{"", nil},
		{"a an the", nil}, // all stop words
		{"IP-based correlation", []string{"ip-based", "correlation"}},
		{"find C2 beacons", []string{"find", "c2", "beacons"}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractKeywords(tt.input)
			if len(got) != len(tt.expected) {
				t.Errorf("extractKeywords(%q) = %v, want %v", tt.input, got, tt.expected)
				return
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("extractKeywords(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.expected[i])
				}
			}
		})
	}
}

func TestComputeOverlapScore(t *testing.T) {
	tests := []struct {
		name       string
		query      []string
		candidates []string
		expected   float64
	}{
		{"empty query", nil, []string{"a", "b"}, 0},
		{"empty candidates", []string{"a"}, nil, 0},
		{"exact match", []string{"dns", "hunt"}, []string{"dns", "hunt"}, 1.0},
		{"partial match", []string{"dns", "hunt", "ip"}, []string{"dns", "hunt"}, 2.0 / 3.0},
		{"no match", []string{"dns"}, []string{"http"}, 0},
		{"substring match", []string{"dns"}, []string{"dns-exfil"}, 1.0},
		{"reverse substring", []string{"dns-exfil"}, []string{"dns"}, 1.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeOverlapScore(tt.query, tt.candidates)
			if math.Abs(got-tt.expected) > 0.01 {
				t.Errorf("computeOverlapScore(%v, %v) = %f, want %f", tt.query, tt.candidates, got, tt.expected)
			}
		})
	}
}

func TestSortStrategyMatches(t *testing.T) {
	matches := []strategyMatchResult{
		{Name: "low", Score: 0.2},
		{Name: "high", Score: 0.9},
		{Name: "mid", Score: 0.5},
	}
	sortStrategyMatches(matches)
	if matches[0].Name != "high" {
		t.Errorf("first = %q, want %q", matches[0].Name, "high")
	}
	if matches[1].Name != "mid" {
		t.Errorf("second = %q, want %q", matches[1].Name, "mid")
	}
	if matches[2].Name != "low" {
		t.Errorf("third = %q, want %q", matches[2].Name, "low")
	}
}

func TestSortStrategyMatches_Empty(t *testing.T) {
	var matches []strategyMatchResult
	sortStrategyMatches(matches) // should not panic
}

func TestSortStrategyMatches_Single(t *testing.T) {
	matches := []strategyMatchResult{{Name: "only", Score: 0.5}}
	sortStrategyMatches(matches)
	if matches[0].Name != "only" {
		t.Errorf("got %q", matches[0].Name)
	}
}

// matchGraphClient provides controlled query results for strategy_match handler tests.
type matchGraphClient struct {
	queryRows []graph.Record
	updates   []mockUpdate
}

func (m *matchGraphClient) Query(_ context.Context, cypher string, params map[string]any) ([]graph.Record, error) {
	return m.queryRows, nil
}
func (m *matchGraphClient) Update(_ context.Context, cypher string, params map[string]any) error {
	m.updates = append(m.updates, mockUpdate{cypher: cypher, params: params})
	return nil
}
func (m *matchGraphClient) Close() error { return nil }

// setupMatchHandler creates a strategy_match handler backed by a mock graph
// with one promoted and one validated strategy.
func setupMatchHandler(t *testing.T) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	t.Helper()
	gc := &matchGraphClient{
		queryRows: []graph.Record{
			{
				"name":            "promoted-strat",
				"description":     "A promoted strategy",
				"tags":            "hunt,network",
				"current_version": "2",
				"composite_score": 0.8,
				"source_names":    []interface{}{"CloudTrail"},
				"field_semantics": []interface{}{"ip_address"},
			},
			{
				"name":            "validated-strat",
				"description":     "A validated strategy",
				"tags":            "hunt,dns",
				"current_version": "1",
				"composite_score": 0.6,
				"source_names":    []interface{}{"VPCFlowLogs"},
				"field_semantics": []interface{}{"hostname"},
			},
		},
	}
	// The handler calls resolveEffectiveStatus which queries StrategyVersion.
	// Override the query results based on the strategy name.
	origQuery := gc.queryRows
	gc2 := &statusAwareMatchClient{
		strategies: origQuery,
		statuses: map[string]string{
			"promoted-strat":  StatusPromoted,
			"validated-strat": StatusValidated,
		},
	}
	return makeStrategyMatchHandler(gc2, false)
}

// statusAwareMatchClient returns different results for strategy listing vs status resolution queries.
type statusAwareMatchClient struct {
	strategies []graph.Record
	statuses   map[string]string
	updates    []mockUpdate
}

func (m *statusAwareMatchClient) Query(_ context.Context, cypher string, params map[string]any) ([]graph.Record, error) {
	// If querying StrategyVersion (resolveEffectiveStatus), return status for that strategy.
	if name, ok := params["name"]; ok {
		if nameStr, ok := name.(string); ok {
			if status, ok := m.statuses[nameStr]; ok {
				return []graph.Record{{
					"status":          status,
					"total_runs":      int64(10),
					"reliability":     0.9,
					"composite_score": 0.8,
					"last_run":        "",
				}}, nil
			}
		}
	}
	return m.strategies, nil
}
func (m *statusAwareMatchClient) Update(_ context.Context, cypher string, params map[string]any) error {
	m.updates = append(m.updates, mockUpdate{cypher: cypher, params: params})
	return nil
}
func (m *statusAwareMatchClient) Close() error { return nil }

func TestStrategyMatch_AutoExecuteExcludesValidated(t *testing.T) {
	handler := setupMatchHandler(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"intent": "auto_execute",
		"tags":   "hunt",
	}

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error: %v", result)
	}

	var resp strategyMatchResponse
	text := result.Content[0].(mcp.TextContent).Text
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Intent != "auto_execute" {
		t.Errorf("intent = %q, want %q", resp.Intent, "auto_execute")
	}
	for _, m := range resp.Matches {
		if m.Status != StatusPromoted {
			t.Errorf("auto_execute returned strategy %q with status %q — contract violation: must only return promoted",
				m.Name, m.Status)
		}
	}
}

func TestStrategyMatch_RecommendIncludesBoth(t *testing.T) {
	handler := setupMatchHandler(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"tags": "hunt",
		// intent omitted — defaults to "recommend"
	}

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp strategyMatchResponse
	text := result.Content[0].(mcp.TextContent).Text
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Intent != "recommend" {
		t.Errorf("intent = %q, want %q", resp.Intent, "recommend")
	}

	hasValidated := false
	hasPromoted := false
	for _, m := range resp.Matches {
		if m.Status == StatusValidated {
			hasValidated = true
		}
		if m.Status == StatusPromoted {
			hasPromoted = true
		}
		if m.Status != StatusValidated && m.Status != StatusPromoted {
			t.Errorf("recommend returned strategy %q with status %q — contract violation",
				m.Name, m.Status)
		}
	}
	if !hasValidated {
		t.Errorf("recommend mode returned no validated strategies — contract requires both buckets be eligible")
	}
	if !hasPromoted {
		t.Errorf("recommend mode returned no promoted strategies — contract requires both buckets be eligible")
	}
}

func TestStrategyMatch_InvalidIntentRejected(t *testing.T) {
	handler := setupMatchHandler(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"intent": "yolo",
		"tags":   "hunt",
	}

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for invalid intent, got success")
	}
}

func TestStrategyMatch_ResponseEchoesIntent(t *testing.T) {
	handler := setupMatchHandler(t)
	for _, intent := range []string{"recommend", "auto_execute"} {
		t.Run(intent, func(t *testing.T) {
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]interface{}{
				"intent": intent,
				"tags":   "hunt",
			}
			result, err := handler(context.Background(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var resp strategyMatchResponse
			text := result.Content[0].(mcp.TextContent).Text
			if err := json.Unmarshal([]byte(text), &resp); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}
			if resp.Intent != intent {
				t.Errorf("response intent = %q, want %q", resp.Intent, intent)
			}
		})
	}
}

func TestCountFindings(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected int
	}{
		{"nil result", nil, 0},
		{"string result", "done", 0},
		{"empty map", map[string]any{}, 0},
		{"no finding keys", map[string]any{"summary": "ok", "metadata": 1}, 0},
		{"single finding key", map[string]any{"findings": []any{"a", "b"}, "summary": "ok"}, 2},
		{"alert key scalar", map[string]any{"alert_count": 5}, 1},
		{"multiple patterns", map[string]any{"findings": []any{"a"}, "detections": []any{"b", "c"}}, 3},
		{"slice result", []any{"f1", "f2", "f3"}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countFindings(tt.input)
			if got != tt.expected {
				t.Errorf("countFindings(%v) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}
