package loaders

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
)

// ResponseAdapter parses tool-specific output into rows.
// text is the raw response body; fields provides column metadata for mapping.
type ResponseAdapter func(text string, fields []FieldMapping) ([]map[string]any, error)

var responseAdapters = map[string]ResponseAdapter{
	"tabular_text": parseTabularTextResponse,
}

// RegisterResponseAdapter adds a named adapter to the registry.
func RegisterResponseAdapter(name string, adapter ResponseAdapter) {
	responseAdapters[name] = adapter
}

// GetResponseAdapter returns the adapter for the given name, if registered.
func GetResponseAdapter(name string) (ResponseAdapter, bool) {
	a, ok := responseAdapters[name]
	return a, ok
}

// parseCSVResponse parses a CSV response where the first row is headers.
// Returns each subsequent row as a map[string]any keyed by header name.
func parseCSVResponse(text string) ([]map[string]any, error) {
	r := csv.NewReader(strings.NewReader(text))
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("csv parse: %w", err)
	}
	if len(records) < 1 {
		return nil, nil
	}

	headers := records[0]
	items := make([]map[string]any, 0, len(records)-1)
	for _, row := range records[1:] {
		m := make(map[string]any, len(headers))
		for i, h := range headers {
			if i < len(row) {
				m[h] = row[i]
			}
		}
		items = append(items, m)
	}
	return items, nil
}

// parseNDJSONResponse parses newline-delimited JSON (one JSON object per line).
func parseNDJSONResponse(text string) ([]map[string]any, error) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	items := make([]map[string]any, 0, len(lines))
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			return nil, fmt.Errorf("ndjson line %d: %w", i+1, err)
		}
		items = append(items, m)
	}
	return items, nil
}

// parseTabularTextResponse parses prose-headed text-table responses.
// Supports two formats:
//
// Format A (pipe-delimited):
//
//	Query executed successfully.
//	Columns: id | hostname | severity
//	---
//	abc123 | host-1 | High
//	def456 | host-2 | Low
//
// Format B (Python-list literals):
//
//	Column Names: id, hostname, severity
//	Row 0: ['abc123', 'host-1', 'High']
//	Row 1: ['def456', 'host-2', 'Low']
//
// Detection: if any line starts with "Column Names:", use format B; otherwise format A.
func parseTabularTextResponse(text string, fields []FieldMapping) ([]map[string]any, error) {
	lines := strings.Split(strings.TrimSpace(text), "\n")

	// Detect format by scanning for "Column Names:" (format B).
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "Column Names:") {
			return parseTabularTextIndexed(lines)
		}
	}
	return parseTabularTextPipe(lines)
}

// parseTabularTextPipe handles "Columns: ... | ---" + pipe-delimited rows.
func parseTabularTextPipe(lines []string) ([]map[string]any, error) {
	var headers []string
	dataStart := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "Columns:") {
			headerStr := strings.TrimPrefix(trimmed, "Columns:")
			for _, h := range strings.Split(headerStr, "|") {
				headers = append(headers, strings.TrimSpace(h))
			}
			continue
		}

		if strings.HasPrefix(trimmed, "---") {
			dataStart = i + 1
			continue
		}
	}

	if dataStart < 0 || len(headers) == 0 {
		return nil, fmt.Errorf("tabular_text: could not find column headers or data separator in response")
	}

	items := make([]map[string]any, 0, len(lines)-dataStart)
	for _, line := range lines[dataStart:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		parts := strings.Split(trimmed, "|")
		m := make(map[string]any, len(headers))
		for j, h := range headers {
			if j < len(parts) {
				m[h] = strings.TrimSpace(parts[j])
			}
		}
		items = append(items, m)
	}

	return items, nil
}

// parseTabularTextIndexed handles "Column Names: ..." + "Row N: [...]" Python-list rows.
func parseTabularTextIndexed(lines []string) ([]map[string]any, error) {
	var headers []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Column Names:") {
			headerStr := strings.TrimPrefix(trimmed, "Column Names:")
			for _, h := range strings.Split(headerStr, ",") {
				headers = append(headers, strings.TrimSpace(h))
			}
			break
		}
	}

	if len(headers) == 0 {
		return nil, fmt.Errorf("tabular_text: could not find 'Column Names:' header")
	}

	var items []map[string]any
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Match "Row N: [...]"
		if !strings.HasPrefix(trimmed, "Row ") {
			continue
		}
		bracketIdx := strings.Index(trimmed, "[")
		if bracketIdx < 0 {
			continue
		}
		// Extract content between [ and ]
		inner := trimmed[bracketIdx+1:]
		if endIdx := strings.LastIndex(inner, "]"); endIdx >= 0 {
			inner = inner[:endIdx]
		}

		values := parsePythonList(inner)
		m := make(map[string]any, len(headers))
		for j, h := range headers {
			if j < len(values) {
				m[h] = values[j]
			}
		}
		items = append(items, m)
	}

	return items, nil
}

// parsePythonList tokenizes a Python-list interior like "'val1', 'Doe, Jane', \"val3\""
// into unquoted string values. Handles commas and escaped quotes inside quoted fields.
func parsePythonList(inner string) []string {
	var values []string
	var current strings.Builder
	var inQuote byte // 0 = not in quote, '\'' or '"' = in that quote

	for i := 0; i < len(inner); i++ {
		ch := inner[i]
		switch {
		case inQuote != 0:
			if ch == '\\' && i+1 < len(inner) && inner[i+1] == inQuote {
				current.WriteByte(inQuote) // emit the escaped quote
				i++                        // skip next char
			} else if ch == inQuote {
				inQuote = 0 // close quote
			} else {
				current.WriteByte(ch)
			}
		case ch == '\'' || ch == '"':
			inQuote = ch // open quote
		case ch == ',':
			values = append(values, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteByte(ch)
		}
	}
	// Last value (or only value if no commas).
	values = append(values, strings.TrimSpace(current.String()))
	return values
}
