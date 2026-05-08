package staging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/parquet-go/parquet-go"
)

const DefaultStagingDir = "/tmp/fracta-staging"

// FieldMapping defines how to extract and type a column.
type FieldMapping struct {
	Source string // source field path (e.g., "source.ip" for ES)
	Column string // output column name in Parquet
	Type   string // type hint: VARCHAR, INT64, FLOAT64, BOOLEAN, TIMESTAMP
}

// TypeMap converts DuckDB-style type strings to parquet-go Node definitions.
func TypeMap(t string) parquet.Node {
	switch strings.ToUpper(t) {
	case "INT64", "INTEGER", "BIGINT":
		return parquet.Optional(parquet.Leaf(parquet.Int64Type))
	case "DOUBLE", "FLOAT64", "FLOAT":
		return parquet.Optional(parquet.Leaf(parquet.DoubleType))
	case "BOOLEAN", "BOOL":
		return parquet.Optional(parquet.Leaf(parquet.BooleanType))
	case "TIMESTAMP":
		return parquet.Optional(parquet.Timestamp(parquet.Microsecond))
	default:
		return parquet.Optional(parquet.String())
	}
}

// BuildSchema creates a parquet.Schema from field mappings.
// Returns both the schema and the fields sorted by Column name to match
// parquet.Group's alphabetical ordering. Callers must use the sorted
// fields when building rows to ensure correct column alignment.
func BuildSchema(name string, fields []FieldMapping) (*parquet.Schema, []FieldMapping) {
	sorted := make([]FieldMapping, len(fields))
	copy(sorted, fields)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Column < sorted[j].Column })

	group := parquet.Group{}
	for _, fm := range sorted {
		colType := fm.Type
		if colType == "" {
			colType = "VARCHAR"
		}
		group[fm.Column] = TypeMap(colType)
	}
	return parquet.NewSchema(name, group), sorted
}

// CoerceValue converts a raw value to the appropriate parquet.Value for the given type string.
func CoerceValue(typeStr string, val any) parquet.Value {
	if val == nil {
		return parquet.NullValue()
	}

	switch strings.ToUpper(typeStr) {
	case "INT64", "INTEGER", "BIGINT":
		return coerceInt64(val)
	case "DOUBLE", "FLOAT64", "FLOAT":
		return coerceFloat64(val)
	case "BOOLEAN", "BOOL":
		return coerceBoolean(val)
	case "TIMESTAMP":
		return coerceTimestamp(val)
	default:
		return coerceString(val)
	}
}

func coerceString(val any) parquet.Value {
	switch v := val.(type) {
	case string:
		return parquet.ByteArrayValue([]byte(v))
	default:
		rv := reflect.ValueOf(val)
		if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Map) {
			if b, err := json.Marshal(val); err == nil {
				return parquet.ByteArrayValue(b)
			}
		}
		return parquet.ByteArrayValue([]byte(fmt.Sprintf("%v", val)))
	}
}

func coerceInt64(val any) parquet.Value {
	switch v := val.(type) {
	case float64:
		return parquet.Int64Value(int64(v))
	case int:
		return parquet.Int64Value(int64(v))
	case int64:
		return parquet.Int64Value(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return parquet.Int64Value(n)
		}
		return parquet.NullValue()
	case string:
		n := json.Number(v)
		if i, err := n.Int64(); err == nil {
			return parquet.Int64Value(i)
		}
		return parquet.NullValue()
	default:
		return parquet.NullValue()
	}
}

func coerceFloat64(val any) parquet.Value {
	switch v := val.(type) {
	case float64:
		return parquet.DoubleValue(v)
	case int:
		return parquet.DoubleValue(float64(v))
	case int64:
		return parquet.DoubleValue(float64(v))
	case json.Number:
		if n, err := v.Float64(); err == nil {
			return parquet.DoubleValue(n)
		}
		return parquet.NullValue()
	case string:
		n := json.Number(v)
		if f, err := n.Float64(); err == nil {
			return parquet.DoubleValue(f)
		}
		return parquet.NullValue()
	default:
		return parquet.NullValue()
	}
}

func coerceBoolean(val any) parquet.Value {
	switch v := val.(type) {
	case bool:
		return parquet.BooleanValue(v)
	case float64:
		return parquet.BooleanValue(v != 0)
	case string:
		switch strings.ToLower(v) {
		case "true", "1":
			return parquet.BooleanValue(true)
		case "false", "0":
			return parquet.BooleanValue(false)
		default:
			return parquet.NullValue()
		}
	default:
		return parquet.NullValue()
	}
}

func coerceTimestamp(val any) parquet.Value {
	switch v := val.(type) {
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return parquet.Int64Value(t.UnixMicro())
		}
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return parquet.Int64Value(t.UnixMicro())
		}
		return parquet.NullValue()
	case float64:
		return parquet.Int64Value(int64(v) * 1_000_000)
	case int64:
		return parquet.Int64Value(v * 1_000_000)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return parquet.Int64Value(n * 1_000_000)
		}
		return parquet.NullValue()
	default:
		return parquet.NullValue()
	}
}

// ExtractField traverses a nested map by dot-delimited path.
func ExtractField(source map[string]any, path string) any {
	parts := strings.Split(path, ".")
	current := any(source)
	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[part]
	}
	return current
}

// BuildRows converts source maps into parquet.Row slices.
// sortedFields must come from BuildSchema (alphabetical order matching the schema).
func BuildRows(sortedFields []FieldMapping, hits []map[string]any) []parquet.Row {
	rows := make([]parquet.Row, 0, len(hits))
	for _, hit := range hits {
		row := make(parquet.Row, len(sortedFields))
		for i, fm := range sortedFields {
			val := ExtractField(hit, fm.Source)
			colType := fm.Type
			if colType == "" {
				colType = "VARCHAR"
			}
			v := CoerceValue(colType, val)
			defLevel := 1 // present
			if v.IsNull() {
				defLevel = 0 // null for optional column
			}
			v = v.Level(0, defLevel, i)
			row[i] = v
		}
		rows = append(rows, row)
	}
	return rows
}

// WriteParquet writes typed columnar data to a Parquet file.
// Returns the absolute path to the written file.
// This is a convenience function used by the sidecar for simple data staging.
func WriteParquet(table string, columns []string, types []string, data [][]any, dir string) (string, error) {
	if dir == "" {
		dir = DefaultStagingDir
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create staging dir: %w", err)
	}

	// Build FieldMappings from columns/types
	fields := make([]FieldMapping, len(columns))
	for i, col := range columns {
		t := "VARCHAR"
		if types != nil && i < len(types) {
			t = types[i]
		}
		fields[i] = FieldMapping{Source: col, Column: col, Type: t}
	}

	schema, sortedFields := BuildSchema(table, fields)

	outPath := filepath.Join(dir, table+".parquet")
	f, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	writer := parquet.NewWriter(f, schema)

	// Build a column-name-to-sorted-index map for WriteParquet's column-ordered data
	colIndex := make(map[string]int, len(sortedFields))
	for i, fm := range sortedFields {
		colIndex[fm.Column] = i
	}

	for _, dataRow := range data {
		row := make(parquet.Row, len(sortedFields))
		for colIdx, val := range dataRow {
			colType := "VARCHAR"
			if types != nil && colIdx < len(types) {
				colType = types[colIdx]
			}
			sortedIdx := colIndex[columns[colIdx]]
			v := CoerceValue(colType, val)
			defLevel := 1 // present
			if v.IsNull() {
				defLevel = 0 // null for optional column
			}
			v = v.Level(0, defLevel, sortedIdx)
			row[sortedIdx] = v
		}
		if _, err := writer.WriteRows([]parquet.Row{row}); err != nil {
			return "", fmt.Errorf("write row: %w", err)
		}
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close parquet writer: %w", err)
	}

	abs, err := filepath.Abs(outPath)
	if err != nil {
		return outPath, nil
	}
	return abs, nil
}

// CleanupDir removes all Parquet files in the staging directory.
func CleanupDir(dir string) error {
	if dir == "" {
		dir = DefaultStagingDir
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".parquet") {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}
