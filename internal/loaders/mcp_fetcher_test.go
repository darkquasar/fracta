package loaders

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/darkquasar/fracta/internal/contract"
	"github.com/darkquasar/fracta/internal/mcpclient"
)

// mockToolCaller implements mcpclient.ToolCaller for testing.
type mockToolCaller struct {
	result *mcpclient.ToolResult
	err    error
}

func (m *mockToolCaller) CallTool(ctx context.Context, server, tool string, args map[string]any) (*mcpclient.ToolResult, error) {
	return m.result, m.err
}

func TestMCPFetcher_JSONArrayResponse(t *testing.T) {
	items := []map[string]any{
		{"id": "a1", "severity": "high", "count": float64(42)},
		{"id": "a2", "severity": "low", "count": float64(7)},
	}
	data, _ := json.Marshal(items)

	caller := &mockToolCaller{
		result: &mcpclient.ToolResult{Text: string(data)},
	}
	fetcher := NewMCPFetcher(caller)

	dir := t.TempDir()
	result, err := fetcher.Fetch(context.Background(), MCPFetchOpts{
		Server: "test-server",
		Tool:   "get_items",
		Fields: []FieldMapping{
			{Source: "id", Column: "alert_id", Type: "VARCHAR"},
			{Source: "severity", Column: "severity", Type: "VARCHAR"},
			{Source: "count", Column: "count", Type: "INT64"},
		},
		StagingDir: dir,
		Table:      "alerts",
		RunID:      "test1234",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.RowCount != 2 {
		t.Errorf("expected 2 rows, got %d", result.RowCount)
	}

	expected := filepath.Join(dir, "test1234", "alerts.parquet")
	if result.ParquetPath != expected {
		// Check absolute path
		abs, _ := filepath.Abs(expected)
		if result.ParquetPath != abs {
			t.Errorf("unexpected path: %s", result.ParquetPath)
		}
	}

	// Verify Parquet file can be read.
	verifyParquet(t, result.ParquetPath, 2, []string{"alert_id", "severity", "count"})
}

func TestMCPFetcher_ObjectWithItemsPath(t *testing.T) {
	response := map[string]any{
		"status": "ok",
		"data": []any{
			map[string]any{"name": "alice"},
			map[string]any{"name": "bob"},
			map[string]any{"name": "charlie"},
		},
	}
	data, _ := json.Marshal(response)

	caller := &mockToolCaller{
		result: &mcpclient.ToolResult{Text: string(data)},
	}
	fetcher := NewMCPFetcher(caller)

	dir := t.TempDir()
	result, err := fetcher.Fetch(context.Background(), MCPFetchOpts{
		Server:     "test-server",
		Tool:       "list_users",
		Fields:     []FieldMapping{{Source: "name", Column: "name", Type: "VARCHAR"}},
		ItemsPath:  "data",
		StagingDir: dir,
		Table:      "users",
		RunID:      "abc12345",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RowCount != 3 {
		t.Errorf("expected 3 rows, got %d", result.RowCount)
	}
}

func TestMCPFetcher_SingleItem(t *testing.T) {
	response := map[string]any{"id": "host1", "os": "linux"}
	data, _ := json.Marshal(response)

	caller := &mockToolCaller{
		result: &mcpclient.ToolResult{Text: string(data)},
	}
	fetcher := NewMCPFetcher(caller)

	dir := t.TempDir()
	result, err := fetcher.Fetch(context.Background(), MCPFetchOpts{
		Server:     "test-server",
		Tool:       "get_host",
		Fields:     []FieldMapping{{Source: "id", Column: "host_id", Type: "VARCHAR"}, {Source: "os", Column: "os", Type: "VARCHAR"}},
		SingleItem: true,
		StagingDir: dir,
		Table:      "host",
		RunID:      "si123456",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RowCount != 1 {
		t.Errorf("expected 1 row, got %d", result.RowCount)
	}
}

func TestMCPFetcher_ToolError(t *testing.T) {
	caller := &mockToolCaller{
		result: &mcpclient.ToolResult{Text: "access denied", IsError: true},
	}
	fetcher := NewMCPFetcher(caller)

	_, err := fetcher.Fetch(context.Background(), MCPFetchOpts{
		Server:     "test-server",
		Tool:       "get_items",
		Fields:     []FieldMapping{{Source: "id", Column: "id", Type: "VARCHAR"}},
		StagingDir: t.TempDir(),
		Table:      "t",
		RunID:      "err12345",
	})
	if err == nil {
		t.Fatal("expected error for tool error response")
	}
	if want := "MCP tool error"; !contains(err.Error(), want) {
		t.Errorf("expected error containing %q, got: %v", want, err)
	}
}

func TestMCPFetcher_MaxRowsTruncation(t *testing.T) {
	items := make([]any, 50)
	for i := range items {
		items[i] = map[string]any{"id": fmt.Sprintf("item-%d", i)}
	}
	data, _ := json.Marshal(items)

	caller := &mockToolCaller{
		result: &mcpclient.ToolResult{Text: string(data)},
	}
	fetcher := NewMCPFetcher(caller)

	dir := t.TempDir()
	result, err := fetcher.Fetch(context.Background(), MCPFetchOpts{
		Server:     "test-server",
		Tool:       "list_items",
		Fields:     []FieldMapping{{Source: "id", Column: "id", Type: "VARCHAR"}},
		MaxRows:    10,
		StagingDir: dir,
		Table:      "items",
		RunID:      "trunc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RowCount != 10 {
		t.Errorf("expected 10 rows (truncated from 50), got %d", result.RowCount)
	}
}

func TestMCPFetcher_TypedFields(t *testing.T) {
	items := []any{
		map[string]any{"id": "1", "count": float64(42), "active": true, "ts": "2026-01-01T00:00:00Z"},
	}
	data, _ := json.Marshal(items)

	caller := &mockToolCaller{
		result: &mcpclient.ToolResult{Text: string(data)},
	}
	fetcher := NewMCPFetcher(caller)

	dir := t.TempDir()
	result, err := fetcher.Fetch(context.Background(), MCPFetchOpts{
		Server: "test-server",
		Tool:   "typed_tool",
		Fields: []FieldMapping{
			{Source: "id", Column: "id", Type: "VARCHAR"},
			{Source: "count", Column: "count", Type: "INT64"},
			{Source: "active", Column: "active", Type: "BOOLEAN"},
			{Source: "ts", Column: "ts", Type: "TIMESTAMP"},
		},
		StagingDir: dir,
		Table:      "typed",
		RunID:      "type1234",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RowCount != 1 {
		t.Errorf("expected 1 row, got %d", result.RowCount)
	}
	verifyParquet(t, result.ParquetPath, 1, []string{"id", "count", "active", "ts"})
}

func TestMCPFetcher_InvalidJSON(t *testing.T) {
	caller := &mockToolCaller{
		result: &mcpclient.ToolResult{Text: "this is not json"},
	}
	fetcher := NewMCPFetcher(caller)

	_, err := fetcher.Fetch(context.Background(), MCPFetchOpts{
		Server:     "test-server",
		Tool:       "bad_tool",
		Fields:     []FieldMapping{{Source: "id", Column: "id", Type: "VARCHAR"}},
		StagingDir: t.TempDir(),
		Table:      "t",
		RunID:      "bad12345",
	})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if want := "invalid JSON"; !contains(err.Error(), want) {
		t.Errorf("expected error containing %q, got: %v", want, err)
	}
}

func TestMCPFetcher_MissingItemsPath(t *testing.T) {
	response := map[string]any{"status": "ok"}
	data, _ := json.Marshal(response)

	caller := &mockToolCaller{
		result: &mcpclient.ToolResult{Text: string(data)},
	}
	fetcher := NewMCPFetcher(caller)

	_, err := fetcher.Fetch(context.Background(), MCPFetchOpts{
		Server:     "test-server",
		Tool:       "get_obj",
		Fields:     []FieldMapping{{Source: "id", Column: "id", Type: "VARCHAR"}},
		StagingDir: t.TempDir(),
		Table:      "t",
		RunID:      "miss1234",
	})
	if err == nil {
		t.Fatal("expected error for object without items_path or single_item")
	}
}

func TestMCPFetcher_ScalarAggAtItemsPath(t *testing.T) {
	// Scalar aggregation result: items_path points to a single object, not an array.
	// Should wrap it in a one-element slice and produce a 1-row Parquet file.
	response := map[string]any{
		"aggregations": map[string]any{
			"total": map[string]any{
				"value": float64(1234),
			},
		},
	}
	data, _ := json.Marshal(response)

	caller := &mockToolCaller{
		result: &mcpclient.ToolResult{Text: string(data)},
	}
	fetcher := NewMCPFetcher(caller)

	res, err := fetcher.Fetch(context.Background(), MCPFetchOpts{
		Server:     "test-server",
		Tool:       "agg_query",
		Fields:     []FieldMapping{{Source: "value", Column: "total", Type: "VARCHAR"}},
		ItemsPath:  "aggregations.total",
		StagingDir: t.TempDir(),
		Table:      "agg_result",
		RunID:      "scalar1234",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RowCount != 1 {
		t.Fatalf("expected 1 row, got %d", res.RowCount)
	}
}

func TestMCPFetcher_TransportError(t *testing.T) {
	caller := &mockToolCaller{
		err: fmt.Errorf("connection refused"),
	}
	fetcher := NewMCPFetcher(caller)

	_, err := fetcher.Fetch(context.Background(), MCPFetchOpts{
		Server:     "test-server",
		Tool:       "get_items",
		Fields:     []FieldMapping{{Source: "id", Column: "id", Type: "VARCHAR"}},
		StagingDir: t.TempDir(),
		Table:      "t",
		RunID:      "tran1234",
	})
	if err == nil {
		t.Fatal("expected error for transport error")
	}
}

func TestMCPFetcher_RunIDDirectoryCreated(t *testing.T) {
	items := []any{map[string]any{"id": "x"}}
	data, _ := json.Marshal(items)

	caller := &mockToolCaller{
		result: &mcpclient.ToolResult{Text: string(data)},
	}
	fetcher := NewMCPFetcher(caller)

	dir := t.TempDir()
	runID := "dir12345"
	_, err := fetcher.Fetch(context.Background(), MCPFetchOpts{
		Server:     "test-server",
		Tool:       "get_items",
		Fields:     []FieldMapping{{Source: "id", Column: "id", Type: "VARCHAR"}},
		StagingDir: dir,
		Table:      "t",
		RunID:      runID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	runDir := filepath.Join(dir, runID)
	info, err := os.Stat(runDir)
	if err != nil {
		t.Fatalf("run directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("run path is not a directory")
	}
}

// paginatedMockCaller returns a different response per call, for pagination tests.
type paginatedMockCaller struct {
	calls   int
	results []*mcpclient.ToolResult
	argsLog []map[string]any // records args from each call
}

func (m *paginatedMockCaller) CallTool(ctx context.Context, server, tool string, args map[string]any) (*mcpclient.ToolResult, error) {
	idx := m.calls
	// Record a copy of args.
	cp := make(map[string]any, len(args))
	for k, v := range args {
		cp[k] = v
	}
	m.argsLog = append(m.argsLog, cp)
	m.calls++
	if idx >= len(m.results) {
		return &mcpclient.ToolResult{Text: "[]"}, nil // empty page
	}
	return m.results[idx], nil
}

func TestFetchPaginated_OffsetMode(t *testing.T) {
	// 3 pages of 2 items each, last page has 1 item (partial = done).
	page0 := []any{map[string]any{"id": "a"}, map[string]any{"id": "b"}}
	page1 := []any{map[string]any{"id": "c"}, map[string]any{"id": "d"}}
	page2 := []any{map[string]any{"id": "e"}} // partial page → stops

	mkResult := func(items []any) *mcpclient.ToolResult {
		data, _ := json.Marshal(items)
		return &mcpclient.ToolResult{Text: string(data)}
	}

	caller := &paginatedMockCaller{
		results: []*mcpclient.ToolResult{mkResult(page0), mkResult(page1), mkResult(page2)},
	}
	fetcher := NewMCPFetcher(caller)

	dir := t.TempDir()
	pagination := &contract.PaginationConfig{
		Mode:        "offset",
		PageSize:    2,
		OffsetParam: "skip",
		LimitParam:  "limit",
	}

	result, err := fetcher.FetchPaginated(context.Background(), MCPFetchOpts{
		Server:     "test-server",
		Tool:       "list_items",
		Fields:     []FieldMapping{{Source: "id", Column: "id", Type: "VARCHAR"}},
		StagingDir: dir,
		Table:      "items",
		RunID:      "off12345",
	}, pagination)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2 + 2 + 1 = 5 rows
	if result.RowCount != 5 {
		t.Errorf("expected 5 rows, got %d", result.RowCount)
	}

	// Verify 3 calls were made.
	if caller.calls != 3 {
		t.Errorf("expected 3 calls, got %d", caller.calls)
	}

	// Verify offset/limit args were set correctly.
	if caller.argsLog[0]["skip"] != 0 {
		t.Errorf("page 0 skip = %v, want 0", caller.argsLog[0]["skip"])
	}
	if caller.argsLog[0]["limit"] != 2 {
		t.Errorf("page 0 limit = %v, want 2", caller.argsLog[0]["limit"])
	}
	if caller.argsLog[1]["skip"] != 2 {
		t.Errorf("page 1 skip = %v, want 2", caller.argsLog[1]["skip"])
	}
	if caller.argsLog[2]["skip"] != 4 {
		t.Errorf("page 2 skip = %v, want 4", caller.argsLog[2]["skip"])
	}

	verifyParquet(t, result.ParquetPath, 5, []string{"id"})
}

func TestFetchPaginated_CursorMode(t *testing.T) {
	// 2 pages with cursor tokens. Second page has no next_cursor → stops.
	resp0 := map[string]any{
		"items":       []any{map[string]any{"id": "x1"}, map[string]any{"id": "x2"}},
		"next_cursor": "page2token",
	}
	resp1 := map[string]any{
		"items": []any{map[string]any{"id": "x3"}, map[string]any{"id": "x4"}},
		// no next_cursor → done
	}

	mkResult := func(resp map[string]any) *mcpclient.ToolResult {
		data, _ := json.Marshal(resp)
		return &mcpclient.ToolResult{Text: string(data)}
	}

	caller := &paginatedMockCaller{
		results: []*mcpclient.ToolResult{mkResult(resp0), mkResult(resp1)},
	}
	fetcher := NewMCPFetcher(caller)

	dir := t.TempDir()
	pagination := &contract.PaginationConfig{
		Mode:           "cursor",
		PageSize:       2,
		LimitParam:     "count",
		CursorParam:    "cursor",
		NextCursorPath: "next_cursor",
	}

	result, err := fetcher.FetchPaginated(context.Background(), MCPFetchOpts{
		Server:     "test-server",
		Tool:       "list_items",
		Fields:     []FieldMapping{{Source: "id", Column: "id", Type: "VARCHAR"}},
		ItemsPath:  "items",
		StagingDir: dir,
		Table:      "items",
		RunID:      "cur12345",
	}, pagination)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2 + 2 = 4 rows
	if result.RowCount != 4 {
		t.Errorf("expected 4 rows, got %d", result.RowCount)
	}

	// Verify 2 calls were made.
	if caller.calls != 2 {
		t.Errorf("expected 2 calls, got %d", caller.calls)
	}

	// First call should NOT have cursor param.
	if _, exists := caller.argsLog[0]["cursor"]; exists {
		t.Error("page 0 should not have cursor param")
	}
	if caller.argsLog[0]["count"] != 2 {
		t.Errorf("page 0 count = %v, want 2", caller.argsLog[0]["count"])
	}

	// Second call should have cursor = "page2token".
	if caller.argsLog[1]["cursor"] != "page2token" {
		t.Errorf("page 1 cursor = %v, want %q", caller.argsLog[1]["cursor"], "page2token")
	}

	verifyParquet(t, result.ParquetPath, 4, []string{"id"})
}

func TestFetchPaginated_MaxRowsCap(t *testing.T) {
	// 2 full pages of 3 items each, but maxRows = 4 → stops mid-page-2.
	page0 := []any{map[string]any{"id": "1"}, map[string]any{"id": "2"}, map[string]any{"id": "3"}}
	page1 := []any{map[string]any{"id": "4"}, map[string]any{"id": "5"}, map[string]any{"id": "6"}}

	mkResult := func(items []any) *mcpclient.ToolResult {
		data, _ := json.Marshal(items)
		return &mcpclient.ToolResult{Text: string(data)}
	}

	caller := &paginatedMockCaller{
		results: []*mcpclient.ToolResult{mkResult(page0), mkResult(page1)},
	}
	fetcher := NewMCPFetcher(caller)

	dir := t.TempDir()
	pagination := &contract.PaginationConfig{
		Mode:        "offset",
		PageSize:    3,
		OffsetParam: "offset",
		LimitParam:  "limit",
	}

	result, err := fetcher.FetchPaginated(context.Background(), MCPFetchOpts{
		Server:     "test-server",
		Tool:       "list_items",
		Fields:     []FieldMapping{{Source: "id", Column: "id", Type: "VARCHAR"}},
		MaxRows:    4,
		StagingDir: dir,
		Table:      "items",
		RunID:      "cap12345",
	}, pagination)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 3 + 1 (capped) = 4 rows
	if result.RowCount != 4 {
		t.Errorf("expected 4 rows, got %d", result.RowCount)
	}
}

func TestFetchPaginated_EmptyFirstPage(t *testing.T) {
	// First page returns empty array → 0 rows, no error.
	caller := &paginatedMockCaller{
		results: []*mcpclient.ToolResult{
			{Text: "[]"},
		},
	}
	fetcher := NewMCPFetcher(caller)

	dir := t.TempDir()
	pagination := &contract.PaginationConfig{
		Mode:        "offset",
		PageSize:    10,
		OffsetParam: "offset",
		LimitParam:  "limit",
	}

	result, err := fetcher.FetchPaginated(context.Background(), MCPFetchOpts{
		Server:     "test-server",
		Tool:       "list_items",
		Fields:     []FieldMapping{{Source: "id", Column: "id", Type: "VARCHAR"}},
		StagingDir: dir,
		Table:      "items",
		RunID:      "empty123",
	}, pagination)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RowCount != 0 {
		t.Errorf("expected 0 rows, got %d", result.RowCount)
	}
}

// failingMockCaller returns normal results for the first N calls, then errors.
type failingMockCaller struct {
	successResults []*mcpclient.ToolResult
	calls          int
	failErr        error
}

func (m *failingMockCaller) CallTool(ctx context.Context, server, tool string, args map[string]any) (*mcpclient.ToolResult, error) {
	idx := m.calls
	m.calls++
	if idx < len(m.successResults) {
		return m.successResults[idx], nil
	}
	return nil, m.failErr
}

func TestFetchPaginated_PartialDataPreserved(t *testing.T) {
	// 3 pages: 2 succeed, page 3 fails → pages 0..1 preserved with Partial=true.
	page0 := []any{map[string]any{"id": "a"}, map[string]any{"id": "b"}}
	page1 := []any{map[string]any{"id": "c"}, map[string]any{"id": "d"}}

	mkResult := func(items []any) *mcpclient.ToolResult {
		data, _ := json.Marshal(items)
		return &mcpclient.ToolResult{Text: string(data)}
	}

	caller := &failingMockCaller{
		successResults: []*mcpclient.ToolResult{mkResult(page0), mkResult(page1)},
		failErr:        fmt.Errorf("connection timeout"),
	}
	fetcher := NewMCPFetcher(caller)

	dir := t.TempDir()
	pagination := &contract.PaginationConfig{
		Mode:        "offset",
		PageSize:    2,
		OffsetParam: "skip",
		LimitParam:  "limit",
	}

	result, err := fetcher.FetchPaginated(context.Background(), MCPFetchOpts{
		Server:     "test-server",
		Tool:       "list_items",
		Fields:     []FieldMapping{{Source: "id", Column: "id", Type: "VARCHAR"}},
		StagingDir: dir,
		Table:      "items",
		RunID:      "part1234",
	}, pagination)
	if err != nil {
		t.Fatalf("expected no error (partial result), got: %v", err)
	}

	// 2 + 2 = 4 rows preserved from pages 0 and 1.
	if result.RowCount != 4 {
		t.Errorf("expected 4 rows preserved, got %d", result.RowCount)
	}
	if !result.Partial {
		t.Error("expected Partial=true")
	}
	if result.PartialError == "" {
		t.Error("expected PartialError to be set")
	}
	if result.PagesCompleted != 2 {
		t.Errorf("expected PagesCompleted=2, got %d", result.PagesCompleted)
	}

	// Verify Parquet file exists with preserved data.
	verifyParquet(t, result.ParquetPath, 4, []string{"id"})
}

func TestFetchPaginated_FirstPageError_NoPartial(t *testing.T) {
	// First page fails → no partial data, returns error.
	caller := &failingMockCaller{
		successResults: nil, // no successful pages
		failErr:        fmt.Errorf("connection refused"),
	}
	fetcher := NewMCPFetcher(caller)

	dir := t.TempDir()
	pagination := &contract.PaginationConfig{
		Mode:        "offset",
		PageSize:    10,
		OffsetParam: "offset",
		LimitParam:  "limit",
	}

	_, err := fetcher.FetchPaginated(context.Background(), MCPFetchOpts{
		Server:     "test-server",
		Tool:       "list_items",
		Fields:     []FieldMapping{{Source: "id", Column: "id", Type: "VARCHAR"}},
		StagingDir: dir,
		Table:      "items",
		RunID:      "fail1234",
	}, pagination)
	if err == nil {
		t.Fatal("expected error when first page fails (no partial data)")
	}
}

func TestFetchPaginated_ToolError_PartialPreserved(t *testing.T) {
	// 1 page succeeds, then tool returns IsError=true → partial preserved.
	page0 := []any{map[string]any{"id": "x"}, map[string]any{"id": "y"}}
	data, _ := json.Marshal(page0)

	caller := &paginatedMockCaller{
		results: []*mcpclient.ToolResult{
			{Text: string(data)},
			{Text: "rate limited", IsError: true},
		},
	}
	fetcher := NewMCPFetcher(caller)

	dir := t.TempDir()
	pagination := &contract.PaginationConfig{
		Mode:        "offset",
		PageSize:    2,
		OffsetParam: "offset",
		LimitParam:  "limit",
	}

	result, err := fetcher.FetchPaginated(context.Background(), MCPFetchOpts{
		Server:     "test-server",
		Tool:       "list_items",
		Fields:     []FieldMapping{{Source: "id", Column: "id", Type: "VARCHAR"}},
		StagingDir: dir,
		Table:      "items",
		RunID:      "terr1234",
	}, pagination)
	if err != nil {
		t.Fatalf("expected no error (partial result), got: %v", err)
	}
	if result.RowCount != 2 {
		t.Errorf("expected 2 rows preserved, got %d", result.RowCount)
	}
	if !result.Partial {
		t.Error("expected Partial=true")
	}
	if result.PagesCompleted != 1 {
		t.Errorf("expected PagesCompleted=1, got %d", result.PagesCompleted)
	}
}

// verifyParquet checks the Parquet file exists and is non-empty.
func verifyParquet(t *testing.T, path string, expectedRows int64, expectedCols []string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("parquet file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Error("parquet file is empty")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
