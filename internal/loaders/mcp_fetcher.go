package loaders

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darkquasar/fracta/internal/contract"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/mcpclient"
	"github.com/darkquasar/fracta/internal/staging"
)

// MCPFetcher calls MCP tools via the ToolCaller interface and stages
// results as Parquet files. Not a StreamLoader — different inputs.
type MCPFetcher struct {
	caller mcpclient.ToolCaller
}

// NewMCPFetcher creates a fetcher backed by the given ToolCaller.
func NewMCPFetcher(caller mcpclient.ToolCaller) *MCPFetcher {
	return &MCPFetcher{caller: caller}
}

// MCPFetchOpts configures a single MCP fetch operation.
type MCPFetchOpts struct {
	Server          string
	Tool            string
	Args            map[string]any // resolved tool arguments (mcp_args merged with strategy params)
	Fields          []FieldMapping // includes ColumnType from contract
	ItemsPath       string         // dot-path to items array (e.g., "data")
	SingleItem      bool           // true if response is one object, not array
	MaxRows         int            // hard cap (default: 10,000)
	Timeout         time.Duration  // per-call timeout (default: 30s)
	StagingDir      string
	Table           string
	RunID           string // namespaces Parquet: {StagingDir}/{RunID}/{Table}.parquet
	ResponseFormat  string // "json" (default), "csv", "ndjson"
	ResponseAdapter string // tool-specific adapter name (e.g., "tabular_text")
}

const defaultMCPMaxRows = 10_000
const defaultMCPTimeout = 30 * time.Second

// toStagingFields converts loaders.FieldMapping to staging.FieldMapping.
func toStagingFields(fields []FieldMapping) []staging.FieldMapping {
	sf := make([]staging.FieldMapping, len(fields))
	for i, f := range fields {
		sf[i] = staging.FieldMapping{Source: f.Source, Column: f.Column, Type: f.Type}
	}
	return sf
}

// parseResponse dispatches to the appropriate parser based on opts.ResponseAdapter/ResponseFormat.
// Returns parsed items, the raw parsed JSON value (nil for non-JSON formats), and any error.
// The rawParsed return is only non-nil for JSON responses — callers that need cursor extraction
// from a JSON envelope (e.g., FetchPaginated) use it; others can ignore it.
func parseResponse(text string, opts MCPFetchOpts, singleItem bool) ([]map[string]any, any, error) {
	if opts.ResponseAdapter != "" {
		adapter, ok := GetResponseAdapter(opts.ResponseAdapter)
		if !ok {
			return nil, nil, fmt.Errorf("unknown response adapter: %q", opts.ResponseAdapter)
		}
		items, err := adapter(text, opts.Fields)
		if err != nil {
			return nil, nil, fmt.Errorf("response adapter %q: %w", opts.ResponseAdapter, err)
		}
		return items, nil, nil
	}

	switch opts.ResponseFormat {
	case "csv":
		items, err := parseCSVResponse(text)
		if err != nil {
			return nil, nil, fmt.Errorf("parsing CSV response: %w", err)
		}
		return items, nil, nil
	case "ndjson":
		items, err := parseNDJSONResponse(text)
		if err != nil {
			return nil, nil, fmt.Errorf("parsing NDJSON response: %w", err)
		}
		return items, nil, nil
	default: // "json" or ""
		// Use json.Decoder with UseNumber() so large integer IDs round-trip
		// as json.Number (preserving exact textual form) instead of float64
		// (which corrupts them to scientific notation like 1.018941958e+09).
		// Closes Bug 9 (readwise:1.018941958e+09 graph-key corruption).
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		var parsed any
		if err := dec.Decode(&parsed); err != nil {
			preview := text
			if len(preview) > 200 {
				preview = preview[:200]
			}
			return nil, nil, fmt.Errorf("MCP tool returned invalid JSON: %s", preview)
		}
		items, err := navigateToItems(parsed, opts.ItemsPath, singleItem)
		if err != nil {
			return nil, nil, fmt.Errorf("navigating MCP response: %w", err)
		}
		return items, parsed, nil
	}
}

// Fetch calls the MCP tool, parses the response, and writes a Parquet file.
func (f *MCPFetcher) Fetch(ctx context.Context, opts MCPFetchOpts) (*LoadResult, error) {
	start := time.Now()

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultMCPTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	maxRows := opts.MaxRows
	if maxRows <= 0 {
		maxRows = defaultMCPMaxRows
	}

	// 1. Call MCP tool
	result, err := f.caller.CallTool(ctx, opts.Server, opts.Tool, opts.Args)
	if err != nil {
		return nil, fmt.Errorf("MCP call %s/%s: %w", opts.Server, opts.Tool, err)
	}

	// 2. Check for tool-level error
	if result.IsError {
		return nil, fmt.Errorf("MCP tool error from %s/%s: %s", opts.Server, opts.Tool, result.Text)
	}

	// 3. Parse response based on format/adapter
	items, _, err := parseResponse(result.Text, opts, opts.SingleItem)
	if err != nil {
		return nil, err
	}

	// 5. Cap at MaxRows
	if len(items) > maxRows {
		fractalog.Component("loaders").Warn("MCP response truncated", "table", opts.Table, "actual", len(items), "max", maxRows)
		items = items[:maxRows]
	}

	// 6. Build parquet-go schema
	stagingFields := toStagingFields(opts.Fields)
	schema, sortedFields := staging.BuildSchema(opts.Table, stagingFields)

	// 7. Create run directory and Parquet path
	runDir := filepath.Join(opts.StagingDir, opts.RunID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return nil, fmt.Errorf("create run dir: %w", err)
	}
	path := filepath.Join(runDir, opts.Table+".parquet")

	// 8. Write Parquet using staging StreamingParquetWriter
	pw, err := staging.NewStreamingParquetWriter(path, schema)
	if err != nil {
		return nil, fmt.Errorf("create parquet writer: %w", err)
	}

	chunkSize := DefaultChunkSize

	for i := 0; i < len(items); i += chunkSize {
		end := i + chunkSize
		if end > len(items) {
			end = len(items)
		}
		rows := staging.BuildRows(sortedFields, items[i:end])
		if err := pw.WriteRows(rows); err != nil {
			pw.Close()
			os.Remove(path)
			return nil, fmt.Errorf("write batch: %w", err)
		}
		if err := pw.Flush(); err != nil {
			pw.Close()
			os.Remove(path)
			return nil, fmt.Errorf("flush batch: %w", err)
		}
	}

	if err := pw.Close(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("close writer: %w", err)
	}

	info, _ := os.Stat(path)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}

	absPath, _ := filepath.Abs(path)
	return &LoadResult{
		ParquetPath: absPath,
		RowCount:    pw.Rows(),
		FileSize:    size,
		Duration:    time.Since(start),
		RowGroups:   pw.RowGroups(),
	}, nil
}

// defaultTotalBudget is the maximum wall-clock time for the entire paginated fetch.
const defaultTotalBudget = 5 * time.Minute

// FetchPaginated calls the MCP tool in a loop, paginating through results.
// Each page is written to the same Parquet file via StreamingParquetWriter.
func (f *MCPFetcher) FetchPaginated(ctx context.Context, opts MCPFetchOpts, pagination *contract.PaginationConfig) (*LoadResult, error) {
	start := time.Now()
	log := fractalog.Component("loaders")

	// Cursor-mode pagination requires a JSON envelope to extract the next cursor.
	// Non-JSON formats (csv, ndjson, adapters) don't have an envelope, so reject upfront.
	isNonJSON := opts.ResponseFormat == "csv" || opts.ResponseFormat == "ndjson" || opts.ResponseAdapter != ""
	if pagination.Mode == "cursor" && isNonJSON {
		return nil, fmt.Errorf("cursor pagination is incompatible with non-JSON response format/adapter (format=%q, adapter=%q)",
			opts.ResponseFormat, opts.ResponseAdapter)
	}

	pageTimeout := opts.Timeout
	if pageTimeout == 0 {
		pageTimeout = defaultMCPTimeout
	}

	maxRows := opts.MaxRows
	if maxRows <= 0 {
		maxRows = defaultMCPMaxRows
	}

	pageSize := pagination.PageSize
	if pageSize <= 0 {
		pageSize = 100
	}

	// Total budget wrapping the loop.
	budgetCtx, budgetCancel := context.WithTimeout(ctx, defaultTotalBudget)
	defer budgetCancel()

	// Set up Parquet writer.
	stagingFields := toStagingFields(opts.Fields)
	schema, sortedFields := staging.BuildSchema(opts.Table, stagingFields)

	runDir := filepath.Join(opts.StagingDir, opts.RunID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return nil, fmt.Errorf("create run dir: %w", err)
	}
	path := filepath.Join(runDir, opts.Table+".parquet")

	pw, err := staging.NewStreamingParquetWriter(path, schema)
	if err != nil {
		return nil, fmt.Errorf("create parquet writer: %w", err)
	}

	totalRows := int64(0)
	pageNum := 0
	var cursor string // for cursor mode
	done := false

	for !done {
		if budgetCtx.Err() != nil {
			break
		}

		// Clone args and set pagination params.
		pageArgs := cloneArgs(opts.Args)
		switch pagination.Mode {
		case "offset":
			offset := pageNum * pageSize
			if pagination.OffsetParam != "" {
				pageArgs[pagination.OffsetParam] = offset
			}
			if pagination.LimitParam != "" {
				pageArgs[pagination.LimitParam] = pageSize
			}
		case "cursor":
			if pagination.LimitParam != "" {
				pageArgs[pagination.LimitParam] = pageSize
			}
			if pageNum > 0 && pagination.CursorParam != "" {
				pageArgs[pagination.CursorParam] = cursor
			}
		}

		// Per-page timeout.
		pageCtx, pageCancel := context.WithTimeout(budgetCtx, pageTimeout)
		result, err := f.caller.CallTool(pageCtx, opts.Server, opts.Tool, pageArgs)
		pageCancel()
		if err != nil {
			// Preserve partial data: close writer gracefully, return what we have.
			pw.Close()
			if totalRows > 0 {
				return buildPartialResult(path, pw, totalRows, pageNum, start, fmt.Sprintf("MCP page %d: %v", pageNum, err)), nil
			}
			os.Remove(path)
			return nil, fmt.Errorf("MCP page %d: %w", pageNum, err)
		}
		if result.IsError {
			pw.Close()
			if totalRows > 0 {
				return buildPartialResult(path, pw, totalRows, pageNum, start, fmt.Sprintf("MCP tool error page %d: %s", pageNum, result.Text)), nil
			}
			os.Remove(path)
			return nil, fmt.Errorf("MCP tool error page %d: %s", pageNum, result.Text)
		}

		// Parse response using format dispatch (supports JSON, CSV, NDJSON, adapters).
		items, parsed, err := parseResponse(result.Text, opts, false)
		if err != nil {
			pw.Close()
			if totalRows > 0 {
				return buildPartialResult(path, pw, totalRows, pageNum, start, fmt.Sprintf("parse page %d: %v", pageNum, err)), nil
			}
			os.Remove(path)
			return nil, fmt.Errorf("parse page %d: %w", pageNum, err)
		}

		if len(items) == 0 {
			break // empty page = done
		}

		// Cap to max rows remaining.
		remaining := maxRows - int(totalRows)
		if remaining <= 0 {
			break
		}
		if len(items) > remaining {
			items = items[:remaining]
		}

		// Write rows.
		rows := staging.BuildRows(sortedFields, items)
		if err := pw.WriteRows(rows); err != nil {
			pw.Close()
			if totalRows > 0 {
				return buildPartialResult(path, pw, totalRows, pageNum, start, fmt.Sprintf("write page %d: %v", pageNum, err)), nil
			}
			os.Remove(path)
			return nil, fmt.Errorf("write page %d: %w", pageNum, err)
		}
		if err := pw.Flush(); err != nil {
			pw.Close()
			if totalRows > 0 {
				return buildPartialResult(path, pw, totalRows, pageNum, start, fmt.Sprintf("flush page %d: %v", pageNum, err)), nil
			}
			os.Remove(path)
			return nil, fmt.Errorf("flush page %d: %w", pageNum, err)
		}

		totalRows += int64(len(items))
		pageNum++

		log.Info("fetched page", "table", opts.Table, "page", pageNum, "items", len(items), "total", totalRows)

		// Termination conditions.
		if int(totalRows) >= maxRows {
			break
		}
		if len(items) < pageSize {
			break // partial page = last page
		}

		// Mode-specific: extract next cursor or continue offset.
		if pagination.Mode == "cursor" {
			parsedMap, ok := parsed.(map[string]any)
			if !ok || pagination.NextCursorPath == "" {
				done = true
			} else {
				nextVal := staging.ExtractField(parsedMap, pagination.NextCursorPath)
				if nextVal == nil {
					done = true
				} else if nextStr, ok := nextVal.(string); !ok || nextStr == "" {
					done = true
				} else {
					cursor = nextStr
				}
			}
		}
	}

	if err := pw.Close(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("close writer: %w", err)
	}

	info, _ := os.Stat(path)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}

	absPath, _ := filepath.Abs(path)
	return &LoadResult{
		ParquetPath: absPath,
		RowCount:    pw.Rows(),
		FileSize:    size,
		Duration:    time.Since(start),
		RowGroups:   pw.RowGroups(),
	}, nil
}

// cloneArgs makes a shallow copy of the args map.
func cloneArgs(args map[string]any) map[string]any {
	cp := make(map[string]any, len(args))
	for k, v := range args {
		cp[k] = v
	}
	return cp
}

// buildPartialResult constructs a LoadResult for a partially-staged paginated fetch.
// The writer must already be closed before calling this.
func buildPartialResult(path string, pw *staging.StreamingParquetWriter, totalRows int64, pagesCompleted int, start time.Time, partialErr string) *LoadResult {
	info, _ := os.Stat(path)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}
	absPath, _ := filepath.Abs(path)
	return &LoadResult{
		ParquetPath:    absPath,
		RowCount:       totalRows,
		FileSize:       size,
		Duration:       time.Since(start),
		RowGroups:      pw.RowGroups(),
		Partial:        true,
		PartialError:   partialErr,
		PagesCompleted: pagesCompleted,
	}
}

// navigateToItems extracts the items array from the parsed JSON response.
func navigateToItems(parsed any, itemsPath string, singleItem bool) ([]map[string]any, error) {
	switch v := parsed.(type) {
	case []any:
		return toMapSlice(v)
	case map[string]any:
		if itemsPath != "" {
			val := staging.ExtractField(v, itemsPath)
			if val == nil {
				return nil, fmt.Errorf("items_path %q not found in response", itemsPath)
			}
			arr, ok := val.([]any)
			if !ok {
				// Single object at items_path (e.g., scalar aggregation result) —
				// wrap in a one-element slice, consistent with singleItem behavior.
				if m, ok := val.(map[string]any); ok {
					return []map[string]any{m}, nil
				}
				return nil, fmt.Errorf("items_path %q resolved to %T, expected array or object", itemsPath, val)
			}
			return toMapSlice(arr)
		}
		if singleItem {
			return []map[string]any{v}, nil
		}
		return nil, fmt.Errorf("response is an object but no items_path or single_item specified")
	default:
		return nil, fmt.Errorf("unexpected JSON type: %T", parsed)
	}
}

// toMapSlice converts []any to []map[string]any. Returns error if any element
// is not an object — mixed arrays are a contract violation.
func toMapSlice(arr []any) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(arr))
	for i, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("array element %d is %T, expected object", i, item)
		}
		result = append(result, m)
	}
	return result, nil
}
