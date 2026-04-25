package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/darkquasar/fracta/internal/contract"
	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/resolve"
	"github.com/darkquasar/fracta/internal/strategy"
)

// mockGraphClient records Update calls for verification.
type mockGraphClient struct {
	updates []mockUpdate
}

type mockUpdate struct {
	cypher string
	params map[string]any
}

func (m *mockGraphClient) Query(_ context.Context, cypher string, params map[string]any) ([]graph.Record, error) {
	return nil, nil
}

func (m *mockGraphClient) Update(_ context.Context, cypher string, params map[string]any) error {
	m.updates = append(m.updates, mockUpdate{cypher: cypher, params: params})
	return nil
}

func (m *mockGraphClient) Close() error { return nil }

func TestCreateStrategyGraphNodes(t *testing.T) {
	gc := &mockGraphClient{}
	metadata := `{
		"description": "Test strategy",
		"tags": ["test", "unit"],
		"requires": {
			"sources": ["CloudTrail", "VPCFlowLogs"]
		}
	}`

	err := createStrategyGraphNodes(context.Background(), gc, "test-strategy", metadata)
	if err != nil {
		t.Fatalf("createStrategyGraphNodes: %v", err)
	}

	// Expect: 1 MERGE for Strategy node + 2 MERGE for USES_SOURCE edges = 3 updates
	if len(gc.updates) != 3 {
		t.Fatalf("expected 3 updates, got %d", len(gc.updates))
	}

	// First update creates Strategy node
	if gc.updates[0].params["name"] != "test-strategy" {
		t.Errorf("strategy name = %v", gc.updates[0].params["name"])
	}
	if gc.updates[0].params["desc"] != "Test strategy" {
		t.Errorf("strategy desc = %v", gc.updates[0].params["desc"])
	}

	// Second and third create USES_SOURCE edges
	if gc.updates[1].params["src"] != "CloudTrail" {
		t.Errorf("source[0] = %v, want CloudTrail", gc.updates[1].params["src"])
	}
	if gc.updates[2].params["src"] != "VPCFlowLogs" {
		t.Errorf("source[1] = %v, want VPCFlowLogs", gc.updates[2].params["src"])
	}
}

func TestCreateStrategyGraphNodesNoSources(t *testing.T) {
	gc := &mockGraphClient{}
	metadata := `{"description": "Simple strategy", "tags": ["simple"]}`

	err := createStrategyGraphNodes(context.Background(), gc, "simple", metadata)
	if err != nil {
		t.Fatalf("createStrategyGraphNodes: %v", err)
	}

	// Only the Strategy node creation, no USES_SOURCE edges
	if len(gc.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(gc.updates))
	}
}

func TestCreateStrategyGraphNodesInvalidJSON(t *testing.T) {
	gc := &mockGraphClient{}
	err := createStrategyGraphNodes(context.Background(), gc, "bad", "not json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// mockGraphClientError always returns errors on Update.
type mockGraphClientError struct {
	mockGraphClient
}

func (m *mockGraphClientError) Update(_ context.Context, _ string, _ map[string]any) error {
	return fmt.Errorf("graph unavailable")
}

func TestCreateStrategyGraphNodesGraphError(t *testing.T) {
	gc := &mockGraphClientError{}
	metadata := `{"description": "test", "tags": []}`

	err := createStrategyGraphNodes(context.Background(), gc, "test", metadata)
	if err == nil {
		t.Fatal("expected error when graph fails")
	}
}

func TestBuildStagingManifest(t *testing.T) {
	rr := &resolveResult{
		Staged: []resolveStaged{
			{
				Table:       "alerts",
				Backend:     "vendor",
				FetchMode:   "mcp_client",
				RowsStaged:  100,
				ParquetPath: "/tmp/fracta-staging/abc123/alerts.parquet",
				Fields:      []resolve.FieldMapping{{SourceField: "id", TargetColumn: "alert_id"}},
			},
			{
				Table:       "events",
				Backend:     "elasticsearch",
				FetchMode:   "api",
				RowsStaged:  50,
				ParquetPath: "/tmp/fracta-staging/abc123/events.parquet",
				Fields:      []resolve.FieldMapping{},
				QueryUsed:   "SELECT * FROM events",
			},
		},
		Pending: []resolvePending{
			{
				Table:     "enrichment",
				Backend:   "mcp",
				FetchMode: "mcp",
			},
		},
		Native: []resolveNative{
			{Table: "computed"},
		},
	}

	cs := &contract.ContractSpec{
		Requires: contract.RequiresSpec{
			Tables: map[string]contract.TableSpec{
				"alerts":     {Optional: false},
				"events":     {Optional: true},
				"enrichment": {Optional: false},
				"computed":   {Optional: true},
			},
		},
	}

	manifest := buildStagingManifest(rr, cs)
	if manifest == nil {
		t.Fatal("manifest is nil")
	}
	if len(manifest) != 4 {
		t.Fatalf("manifest len = %d, want 4", len(manifest))
	}

	// Check staged mcp_client table
	alerts := manifest["alerts"]
	if alerts.Mode != "mcp_client" {
		t.Errorf("alerts.mode = %q, want %q", alerts.Mode, "mcp_client")
	}
	if !alerts.Required {
		t.Error("alerts.required should be true")
	}
	if !alerts.Staged {
		t.Error("alerts.staged should be true")
	}
	if alerts.ParquetPath != "/tmp/fracta-staging/abc123/alerts.parquet" {
		t.Errorf("alerts.parquet_path = %q", alerts.ParquetPath)
	}

	// Check staged api table (optional)
	events := manifest["events"]
	if events.Mode != "api" {
		t.Errorf("events.mode = %q", events.Mode)
	}
	if events.Required {
		t.Error("events.required should be false (optional)")
	}
	if !events.Staged {
		t.Error("events.staged should be true")
	}

	// Check pending mcp table
	enrichment := manifest["enrichment"]
	if enrichment.Mode != "mcp" {
		t.Errorf("enrichment.mode = %q", enrichment.Mode)
	}
	if !enrichment.Required {
		t.Error("enrichment.required should be true")
	}
	if enrichment.Staged {
		t.Error("enrichment.staged should be false")
	}
	if enrichment.ParquetPath != "" {
		t.Errorf("enrichment.parquet_path = %q, want empty", enrichment.ParquetPath)
	}

	// Check native table
	computed := manifest["computed"]
	if computed.Mode != "native" {
		t.Errorf("computed.mode = %q", computed.Mode)
	}
	if computed.Required {
		t.Error("computed.required should be false (optional)")
	}
	if computed.Staged {
		t.Error("computed.staged should be false")
	}
}

func TestBuildStagingManifestNilInputs(t *testing.T) {
	// Both nil
	if manifest := buildStagingManifest(nil, nil); manifest != nil {
		t.Errorf("expected nil, got %v", manifest)
	}

	// rr nil
	cs := &contract.ContractSpec{}
	if manifest := buildStagingManifest(nil, cs); manifest != nil {
		t.Errorf("expected nil, got %v", manifest)
	}

	// cs nil
	rr := &resolveResult{}
	if manifest := buildStagingManifest(rr, nil); manifest != nil {
		t.Errorf("expected nil, got %v", manifest)
	}
}

func TestStrategyRunResponse_PartialResults(t *testing.T) {
	// Verify partial result fields are marshalled when present.
	resp := strategyRunResponse{
		Status:                  "error",
		Error:                   "step 2 failed",
		PartialResults:          map[string]any{"step1": "ok"},
		PartialResultsTruncated: true,
		OmittedSteps:            []string{"step3"},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out["partial_results"] == nil {
		t.Error("partial_results missing from JSON")
	}
	pr := out["partial_results"].(map[string]any)
	if pr["step1"] != "ok" {
		t.Errorf("partial_results.step1 = %v, want \"ok\"", pr["step1"])
	}
	if out["partial_results_truncated"] != true {
		t.Errorf("partial_results_truncated = %v, want true", out["partial_results_truncated"])
	}
	omitted, ok := out["omitted_steps"].([]any)
	if !ok || len(omitted) != 1 || omitted[0] != "step3" {
		t.Errorf("omitted_steps = %v, want [\"step3\"]", out["omitted_steps"])
	}
}

func TestStrategyRunResponse_PartialResultsOmittedWhenEmpty(t *testing.T) {
	// Verify partial result fields are omitted from JSON when zero-valued.
	resp := strategyRunResponse{
		Status: "ok",
		Result: map[string]any{"findings": 42},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, exists := out["partial_results"]; exists {
		t.Error("partial_results should be omitted for successful runs")
	}
	if _, exists := out["partial_results_truncated"]; exists {
		t.Error("partial_results_truncated should be omitted when false")
	}
	if _, exists := out["omitted_steps"]; exists {
		t.Error("omitted_steps should be omitted when empty")
	}
}

func TestStrategyRunResponse_SessionID(t *testing.T) {
	// Verify session_id appears in pending response.
	resp := strategyRunResponse{
		Status:    "pending",
		SessionID: "abc12345",
		Pending:   []resolvePending{{Table: "alerts"}},
		Message:   "1 table(s) require MCP data.",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out["session_id"] != "abc12345" {
		t.Errorf("session_id = %v, want %q", out["session_id"], "abc12345")
	}
}

func TestStrategyRunResponse_SessionIDOmittedWhenEmpty(t *testing.T) {
	resp := strategyRunResponse{
		Status: "ok",
		Result: "done",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, exists := out["session_id"]; exists {
		t.Error("session_id should be omitted for terminal runs")
	}
}

func TestSessionMergeIntoManifest(t *testing.T) {
	// Simulate the session merge logic from makeStrategyRunHandler.
	ss := strategy.NewStagingSessionStore(t.TempDir())
	session := ss.Create()
	session.Put("alerts", "/tmp/staging/"+session.ID+"/alerts.parquet")

	// Build a manifest with "alerts" as pending (unstaged).
	manifest := strategy.StagingManifest{
		"alerts": strategy.StagingManifestEntry{
			Mode:     "mcp",
			Required: true,
			Staged:   false,
		},
		"events": strategy.StagingManifestEntry{
			Mode:        "api",
			Required:    true,
			Staged:      true,
			ParquetPath: "/tmp/staging/events.parquet",
		},
	}

	// Merge session data into manifest (same logic as in the handler).
	for table, path := range session.All() {
		if entry, ok := manifest[table]; ok && !entry.Staged {
			entry.Staged = true
			entry.ParquetPath = path
			manifest[table] = entry
		}
	}

	// alerts should now be staged with the session path.
	alerts := manifest["alerts"]
	if !alerts.Staged {
		t.Error("alerts should be staged after session merge")
	}
	if alerts.ParquetPath != "/tmp/staging/"+session.ID+"/alerts.parquet" {
		t.Errorf("alerts.parquet_path = %q", alerts.ParquetPath)
	}

	// events should be unchanged.
	events := manifest["events"]
	if events.ParquetPath != "/tmp/staging/events.parquet" {
		t.Errorf("events.parquet_path should be unchanged, got %q", events.ParquetPath)
	}
}

func TestSessionCleanupAfterRun(t *testing.T) {
	ss := strategy.NewStagingSessionStore(t.TempDir())
	session := ss.Create()
	id := session.ID

	// Verify session exists.
	if _, ok := ss.Get(id); !ok {
		t.Fatal("session should exist after Create")
	}

	// Simulate terminal run cleanup.
	ss.Remove(id)

	// Verify session is gone.
	if _, ok := ss.Get(id); ok {
		t.Error("session should be removed after terminal run")
	}
}

func TestStageResponse_IncludesSessionID(t *testing.T) {
	resp := strategyStageResponse{
		SessionID: "deadbeef",
		Table:     "alerts",
		Rows:      42,
		Columns:   3,
		Mode:      "parquet",
		Message:   "Table \"alerts\" staged.",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out["session_id"] != "deadbeef" {
		t.Errorf("session_id = %v, want %q", out["session_id"], "deadbeef")
	}
	if out["table"] != "alerts" {
		t.Errorf("table = %v, want %q", out["table"], "alerts")
	}
	if int(out["rows"].(float64)) != 42 {
		t.Errorf("rows = %v, want 42", out["rows"])
	}
}

// --- S8: Recursive mcp_args interpolation tests ---

func TestInterpolateArgs_NestedStringValues(t *testing.T) {
	args := map[string]any{
		"query": `{"range": {"@timestamp": {"gte": "{{time_start}}", "lte": "{{time_end}}"}}}`,
		"index": "dns-*",
	}
	params := map[string]any{
		"time_start": "2026-01-01T00:00:00Z",
		"time_end":   "2026-01-02T00:00:00Z",
	}

	result := interpolateArgs(args, params)

	expected := `{"range": {"@timestamp": {"gte": "2026-01-01T00:00:00Z", "lte": "2026-01-02T00:00:00Z"}}}`
	if result["query"] != expected {
		t.Errorf("query = %v, want %v", result["query"], expected)
	}
	if result["index"] != "dns-*" {
		t.Errorf("index should be unchanged, got %v", result["index"])
	}
}

func TestInterpolateArgs_DeeplyNestedMap(t *testing.T) {
	args := map[string]any{
		"outer": map[string]any{
			"middle": map[string]any{
				"inner": "value is {{param}}",
			},
		},
	}
	params := map[string]any{"param": "resolved"}

	result := interpolateArgs(args, params)

	outer := result["outer"].(map[string]any)
	middle := outer["middle"].(map[string]any)
	if middle["inner"] != "value is resolved" {
		t.Errorf("inner = %v, want %q", middle["inner"], "value is resolved")
	}
}

func TestInterpolateArgs_SliceWithTemplates(t *testing.T) {
	args := map[string]any{
		"filters": []any{
			"host:{{hostname}}",
			"static-value",
			"port:{{port}}",
		},
	}
	params := map[string]any{
		"hostname": "web-01",
		"port":     "8080",
	}

	result := interpolateArgs(args, params)

	filters := result["filters"].([]any)
	if filters[0] != "host:web-01" {
		t.Errorf("filters[0] = %v, want %q", filters[0], "host:web-01")
	}
	if filters[1] != "static-value" {
		t.Errorf("filters[1] = %v, want %q", filters[1], "static-value")
	}
	if filters[2] != "port:8080" {
		t.Errorf("filters[2] = %v, want %q", filters[2], "port:8080")
	}
}

func TestInterpolateArgs_NoTemplateVars(t *testing.T) {
	args := map[string]any{
		"query": "SELECT * FROM events",
		"limit": 100,
	}
	params := map[string]any{"unused": "value"}

	result := interpolateArgs(args, params)

	if result["query"] != "SELECT * FROM events" {
		t.Errorf("query should be unchanged, got %v", result["query"])
	}
	if result["limit"] != 100 {
		t.Errorf("limit should be unchanged, got %v", result["limit"])
	}
}

func TestInterpolateArgs_UnresolvedParamFallback(t *testing.T) {
	// When a template references a param that's not in the map,
	// InterpolateSimple returns an error. We should fall back to the original string.
	args := map[string]any{
		"query": "host:{{missing_param}}",
	}
	params := map[string]any{}

	result := interpolateArgs(args, params)

	if result["query"] != "host:{{missing_param}}" {
		t.Errorf("query = %v, want unchanged template", result["query"])
	}
}

func TestInterpolateArgs_NilValue(t *testing.T) {
	args := map[string]any{
		"key":  nil,
		"key2": "{{param}}",
	}
	params := map[string]any{"param": "val"}

	result := interpolateArgs(args, params)

	if result["key"] != nil {
		t.Errorf("nil key should stay nil, got %v", result["key"])
	}
	if result["key2"] != "val" {
		t.Errorf("key2 = %v, want %q", result["key2"], "val")
	}
}

func TestInterpolateValue_NonStringScalar(t *testing.T) {
	// Non-string scalars (int, float, bool) should pass through unchanged.
	result := interpolateValue(42, map[string]any{"x": "y"})
	if result != 42 {
		t.Errorf("int should pass through, got %v", result)
	}

	result = interpolateValue(3.14, map[string]any{"x": "y"})
	if result != 3.14 {
		t.Errorf("float should pass through, got %v", result)
	}

	result = interpolateValue(true, map[string]any{"x": "y"})
	if result != true {
		t.Errorf("bool should pass through, got %v", result)
	}
}

// --- S3: Error wrapping tests ---

func TestSidecarErrorMessage_TransportError(t *testing.T) {
	err := &strategy.SidecarTransportError{Err: fmt.Errorf("write: broken pipe")}
	msg, ok := sidecarErrorMessage(err, nil, "test-strategy")
	if !ok {
		t.Fatal("expected sidecarErrorMessage to detect transport error")
	}
	if !strings.Contains(msg, "runner unavailable") {
		t.Errorf("expected 'runner unavailable' in message, got: %s", msg)
	}
	if strings.Contains(msg, "not found") {
		t.Errorf("message should not contain 'not found', got: %s", msg)
	}
}

func TestSidecarErrorMessage_RestartedError(t *testing.T) {
	err := &strategy.SidecarRestartedError{Err: fmt.Errorf("read: EOF")}
	msg, ok := sidecarErrorMessage(err, nil, "test-strategy")
	if !ok {
		t.Fatal("expected sidecarErrorMessage to detect restarted error")
	}
	if !strings.Contains(msg, "restarted") {
		t.Errorf("expected 'restarted' in message, got: %s", msg)
	}
	if strings.Contains(msg, "not found") {
		t.Errorf("message should not contain 'not found', got: %s", msg)
	}
}

func TestSidecarErrorMessage_WrappedTransportError(t *testing.T) {
	// Transport errors may be wrapped by other code — errors.As should still detect them.
	inner := &strategy.SidecarTransportError{Err: fmt.Errorf("connection reset")}
	wrapped := fmt.Errorf("describe call: %w", inner)
	msg, ok := sidecarErrorMessage(wrapped, nil, "test-strategy")
	if !ok {
		t.Fatal("expected sidecarErrorMessage to detect wrapped transport error")
	}
	if !strings.Contains(msg, "runner unavailable") {
		t.Errorf("expected 'runner unavailable' in message, got: %s", msg)
	}
}

func TestSidecarErrorMessage_NonSidecarError(t *testing.T) {
	err := errors.New("strategy not registered")
	msg, ok := sidecarErrorMessage(err, nil, "test-strategy")
	if ok {
		t.Errorf("should return false for non-sidecar error, got msg: %s", msg)
	}
}

// --- S9: Param normalization tests ---

func TestNormalizeParams_MissingRequired(t *testing.T) {
	cs := &contract.ContractSpec{
		Params: map[string]contract.ParamSpec{
			"ip": {Type: "string", Required: true},
		},
	}
	_, err := normalizeParams(nil, cs)
	if err == nil {
		t.Fatal("expected error for missing required param")
	}
	if !strings.Contains(err.Error(), "required parameter") {
		t.Errorf("error = %v, want 'required parameter' message", err)
	}
}

func TestNormalizeParams_DefaultApplied(t *testing.T) {
	cs := &contract.ContractSpec{
		Params: map[string]contract.ParamSpec{
			"days_back": {Type: "int", Default: float64(30)},
		},
	}
	params, err := normalizeParams(nil, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params["days_back"] != float64(30) {
		t.Errorf("days_back = %v, want 30", params["days_back"])
	}
}

func TestNormalizeParams_Float64ToInt(t *testing.T) {
	cs := &contract.ContractSpec{
		Params: map[string]contract.ParamSpec{
			"days_back": {Type: "int"},
		},
	}
	params := map[string]any{"days_back": float64(7)}
	result, err := normalizeParams(params, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, ok := result["days_back"].(int); !ok || v != 7 {
		t.Errorf("days_back = %v (%T), want int(7)", result["days_back"], result["days_back"])
	}
}

func TestNormalizeParams_StringToInt(t *testing.T) {
	cs := &contract.ContractSpec{
		Params: map[string]contract.ParamSpec{
			"limit": {Type: "integer"},
		},
	}
	params := map[string]any{"limit": "100"}
	result, err := normalizeParams(params, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, ok := result["limit"].(int); !ok || v != 100 {
		t.Errorf("limit = %v (%T), want int(100)", result["limit"], result["limit"])
	}
}

func TestNormalizeParams_StringToIntInvalid(t *testing.T) {
	cs := &contract.ContractSpec{
		Params: map[string]contract.ParamSpec{
			"limit": {Type: "int"},
		},
	}
	params := map[string]any{"limit": "abc"}
	_, err := normalizeParams(params, cs)
	if err == nil {
		t.Fatal("expected error for invalid int string")
	}
}

func TestNormalizeParams_StringToBool(t *testing.T) {
	cs := &contract.ContractSpec{
		Params: map[string]contract.ParamSpec{
			"verbose": {Type: "bool"},
		},
	}
	params := map[string]any{"verbose": "true"}
	result, err := normalizeParams(params, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, ok := result["verbose"].(bool); !ok || !v {
		t.Errorf("verbose = %v (%T), want bool(true)", result["verbose"], result["verbose"])
	}
}

func TestNormalizeParams_StringToBoolInvalid(t *testing.T) {
	cs := &contract.ContractSpec{
		Params: map[string]contract.ParamSpec{
			"verbose": {Type: "boolean"},
		},
	}
	params := map[string]any{"verbose": "maybe"}
	_, err := normalizeParams(params, cs)
	if err == nil {
		t.Fatal("expected error for invalid bool string")
	}
}

func TestNormalizeParams_NilContractSpec(t *testing.T) {
	params := map[string]any{"ip": "10.0.0.1"}
	result, err := normalizeParams(params, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["ip"] != "10.0.0.1" {
		t.Errorf("params should pass through unchanged with nil spec")
	}
}

func TestNormalizeParams_ExtraParamsPreserved(t *testing.T) {
	cs := &contract.ContractSpec{
		Params: map[string]contract.ParamSpec{
			"ip": {Type: "string", Required: true},
		},
	}
	params := map[string]any{"ip": "10.0.0.1", "extra": "value"}
	result, err := normalizeParams(params, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["extra"] != "value" {
		t.Error("extra params should be preserved")
	}
}

// --- S9: Session fingerprint validation tests ---

func TestSessionParamsFingerprint_Mismatch(t *testing.T) {
	// Simulate session created with one param set, re-used with another.
	ss := strategy.NewStagingSessionStore(t.TempDir())
	session := ss.Create()
	session.ParamsFingerprint = strategy.ComputeParamsFingerprint(map[string]any{"ip": "10.0.0.1"})

	// Different params should produce different fingerprint.
	newFP := strategy.ComputeParamsFingerprint(map[string]any{"ip": "192.168.1.1"})
	if newFP == session.ParamsFingerprint {
		t.Fatal("fingerprints should differ for different params")
	}
}

func TestSessionParamsFingerprint_Match(t *testing.T) {
	params := map[string]any{"ip": "10.0.0.1", "days_back": 7}
	fp1 := strategy.ComputeParamsFingerprint(params)
	fp2 := strategy.ComputeParamsFingerprint(params)
	if fp1 != fp2 {
		t.Errorf("same params should produce same fingerprint: %q vs %q", fp1, fp2)
	}
}

func TestSessionParamsFingerprint_EmptyParams(t *testing.T) {
	fp := strategy.ComputeParamsFingerprint(nil)
	if fp != "" {
		t.Errorf("empty params should produce empty fingerprint, got %q", fp)
	}
}

func TestBuildStagingManifestEmptyResolve(t *testing.T) {
	rr := &resolveResult{
		Staged:  []resolveStaged{},
		Pending: []resolvePending{},
		Native:  []resolveNative{},
	}
	cs := &contract.ContractSpec{
		Requires: contract.RequiresSpec{
			Tables: map[string]contract.TableSpec{},
		},
	}

	manifest := buildStagingManifest(rr, cs)
	if manifest == nil {
		t.Fatal("manifest is nil")
	}
	if len(manifest) != 0 {
		t.Errorf("manifest len = %d, want 0", len(manifest))
	}
}
