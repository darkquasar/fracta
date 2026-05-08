package staging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/parquet-go/parquet-go"
)

func TestTypeMap(t *testing.T) {
	tests := []struct {
		input    string
		wantKind string // check the node kind via string representation
	}{
		{"VARCHAR", "byte_array"},
		{"varchar", "byte_array"},
		{"BIGINT", "int64"},
		{"INT64", "int64"},
		{"INTEGER", "int64"},
		{"DOUBLE", "float64"},
		{"FLOAT64", "float64"},
		{"FLOAT", "float64"},
		{"BOOLEAN", "boolean"},
		{"BOOL", "boolean"},
		{"TIMESTAMP", "int64"}, // timestamp is logical type on int64
		{"unknown_type", "byte_array"},
	}
	for _, tt := range tests {
		node := TypeMap(tt.input)
		if node == nil {
			t.Errorf("TypeMap(%q) returned nil", tt.input)
		}
	}
}

func TestBuildSchema(t *testing.T) {
	fields := []FieldMapping{
		{Source: "z_field", Column: "z_col", Type: "VARCHAR"},
		{Source: "a_field", Column: "a_col", Type: "INT64"},
		{Source: "m_field", Column: "m_col", Type: "BOOLEAN"},
	}

	schema, sorted := BuildSchema("test", fields)

	// Sorted fields should be alphabetical by Column
	if sorted[0].Column != "a_col" || sorted[1].Column != "m_col" || sorted[2].Column != "z_col" {
		t.Errorf("fields not sorted: %v", sorted)
	}

	if schema == nil {
		t.Fatal("schema is nil")
	}
}

func TestCoerceValue(t *testing.T) {
	// VARCHAR
	v := CoerceValue("VARCHAR", "hello")
	if v.IsNull() {
		t.Error("expected non-null string value")
	}

	// INT64
	v = CoerceValue("INT64", float64(42))
	if v.IsNull() || v.Int64() != 42 {
		t.Errorf("expected INT64(42), got %v", v)
	}

	// DOUBLE
	v = CoerceValue("DOUBLE", float64(3.14))
	if v.IsNull() || v.Double() != 3.14 {
		t.Errorf("expected DOUBLE(3.14), got %v", v)
	}

	// BOOLEAN
	v = CoerceValue("BOOLEAN", true)
	if v.IsNull() || !v.Boolean() {
		t.Errorf("expected BOOLEAN(true), got %v", v)
	}

	// TIMESTAMP from string
	v = CoerceValue("TIMESTAMP", "2026-03-26T10:00:00Z")
	if v.IsNull() {
		t.Error("expected non-null timestamp value")
	}

	// nil
	v = CoerceValue("VARCHAR", nil)
	if !v.IsNull() {
		t.Error("expected null value for nil input")
	}
}

func TestWriteParquetBasic(t *testing.T) {
	dir := t.TempDir()

	columns := []string{"name", "age", "active"}
	types := []string{"VARCHAR", "BIGINT", "BOOLEAN"}
	data := [][]any{
		{"alice", float64(30), true},
		{"bob", float64(25), false},
		{nil, nil, nil},
	}

	path, err := WriteParquet("test_table", columns, types, data, dir)
	if err != nil {
		t.Fatalf("WriteParquet: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if info.Size() == 0 {
		t.Error("output file is empty")
	}
	if filepath.Base(path) != "test_table.parquet" {
		t.Errorf("filename = %q, want test_table.parquet", filepath.Base(path))
	}
	t.Logf("wrote %d bytes to %s", info.Size(), path)

	// Verify we can read the file back with parquet-go
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer f.Close()

	pf, err := parquet.OpenFile(f, info.Size())
	if err != nil {
		t.Fatalf("open parquet: %v", err)
	}

	if pf.NumRows() != 3 {
		t.Errorf("expected 3 rows, got %d", pf.NumRows())
	}
}

func TestWriteParquetAllVarchar(t *testing.T) {
	dir := t.TempDir()

	columns := []string{"col_a", "col_b"}
	data := [][]any{
		{"hello", "world"},
		{"foo", "bar"},
	}

	// No types provided -- defaults to VARCHAR
	path, err := WriteParquet("varchar_table", columns, nil, data, dir)
	if err != nil {
		t.Fatalf("WriteParquet: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if info.Size() == 0 {
		t.Error("output file is empty")
	}
}

func TestWriteParquetFloat64(t *testing.T) {
	dir := t.TempDir()

	columns := []string{"score"}
	types := []string{"DOUBLE"}
	data := [][]any{
		{float64(3.14)},
		{float64(2.71)},
		{nil},
	}

	path, err := WriteParquet("floats", columns, types, data, dir)
	if err != nil {
		t.Fatalf("WriteParquet: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() == 0 {
		t.Error("output file is empty")
	}
}

func TestWriteParquetTimestamp(t *testing.T) {
	dir := t.TempDir()

	columns := []string{"event_time", "label"}
	types := []string{"TIMESTAMP", "VARCHAR"}
	data := [][]any{
		{"2026-03-26T10:00:00Z", "event1"},
		{"2026-03-26T11:00:00Z", "event2"},
		{nil, "no_time"},
	}

	path, err := WriteParquet("timestamps", columns, types, data, dir)
	if err != nil {
		t.Fatalf("WriteParquet: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() == 0 {
		t.Error("output file is empty")
	}
}

func TestWriteParquetDefaultDir(t *testing.T) {
	// Use a unique subdir to avoid conflicts
	dir := filepath.Join(t.TempDir(), "staging-test")

	path, err := WriteParquet("auto_dir", []string{"x"}, nil, [][]any{{"a"}}, dir)
	if err != nil {
		t.Fatalf("WriteParquet: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestWriteParquetEmptyData(t *testing.T) {
	dir := t.TempDir()

	path, err := WriteParquet("empty", []string{"x"}, nil, [][]any{}, dir)
	if err != nil {
		t.Fatalf("WriteParquet: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// Even empty Parquet has metadata
	if info.Size() == 0 {
		t.Error("output file is empty")
	}
}

func TestCleanupDir(t *testing.T) {
	dir := t.TempDir()

	// Create some Parquet files and a non-Parquet file
	os.WriteFile(filepath.Join(dir, "a.parquet"), []byte("fake"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.parquet"), []byte("fake"), 0o644)
	os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep"), 0o644)

	if err := CleanupDir(dir); err != nil {
		t.Fatalf("CleanupDir: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".parquet" {
			t.Errorf("parquet file %q still exists after cleanup", e.Name())
		}
	}
	// Non-parquet file should remain
	if _, err := os.Stat(filepath.Join(dir, "keep.txt")); err != nil {
		t.Error("keep.txt was incorrectly removed")
	}
}

func TestCleanupDirNonexistent(t *testing.T) {
	if err := CleanupDir("/tmp/nonexistent-dir-that-does-not-exist"); err != nil {
		t.Fatalf("CleanupDir on nonexistent dir: %v", err)
	}
}

func TestExtractField(t *testing.T) {
	source := map[string]any{
		"user": "alice",
		"nested": map[string]any{
			"field": "value",
			"deep": map[string]any{
				"item": 42,
			},
		},
	}

	tests := []struct {
		path     string
		expected any
	}{
		{"user", "alice"},
		{"nested.field", "value"},
		{"nested.deep.item", 42},
		{"nonexistent", nil},
		{"nested.missing", nil},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := ExtractField(source, tt.path)
			if got != tt.expected {
				t.Errorf("ExtractField(%q) = %v, want %v", tt.path, got, tt.expected)
			}
		})
	}
}

func TestCoerceStringCompositeValues(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		wantJSON string // expected JSON output; empty means check fmt.Sprintf fallback
	}{
		{
			name:     "untyped slice",
			input:    []any{"Malware", "Security Risks"},
			wantJSON: `["Malware","Security Risks"]`,
		},
		{
			name:     "typed string slice",
			input:    []string{"a", "b c"},
			wantJSON: `["a","b c"]`,
		},
		{
			name:     "untyped map",
			input:    map[string]any{"key": "val"},
			wantJSON: `{"key":"val"}`,
		},
		{
			name:     "typed int map",
			input:    map[string]int{"count": 5},
			wantJSON: `{"count":5}`,
		},
		{
			name:     "empty slice",
			input:    []any{},
			wantJSON: `[]`,
		},
		{
			name:     "nested composite",
			input:    []any{"a", map[string]any{"b": 1}},
			wantJSON: `["a",{"b":1}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := coerceString(tt.input)
			if v.IsNull() {
				t.Fatal("expected non-null value")
			}
			got := string(v.ByteArray())
			if got != tt.wantJSON {
				t.Errorf("coerceString(%v) = %q, want %q", tt.input, got, tt.wantJSON)
			}
			// Verify the output is valid JSON that round-trips
			var parsed any
			if err := json.Unmarshal([]byte(got), &parsed); err != nil {
				t.Errorf("output is not valid JSON: %v", err)
			}
		})
	}
}

func TestCoerceStringScalarsUnchanged(t *testing.T) {
	// Scalars should still use fmt.Sprintf, not JSON encoding
	tests := []struct {
		name  string
		input any
		want  string
	}{
		{"int", 42, "42"},
		{"float", 3.14, "3.14"},
		{"bool", true, "true"},
		{"string", "hello", "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := coerceString(tt.input)
			got := string(v.ByteArray())
			if got != tt.want {
				t.Errorf("coerceString(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildRows(t *testing.T) {
	fields := []FieldMapping{
		{Source: "source.ip", Column: "ip", Type: "VARCHAR"},
		{Source: "event.count", Column: "count", Type: "INT64"},
	}

	_, sortedFields := BuildSchema("test", fields)

	hits := []map[string]any{
		{"source": map[string]any{"ip": "10.0.0.1"}, "event": map[string]any{"count": float64(5)}},
		{"source": map[string]any{"ip": "10.0.0.2"}, "event": map[string]any{"count": float64(3)}},
	}

	rows := BuildRows(sortedFields, hits)
	if len(rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(rows))
	}
	for _, row := range rows {
		if len(row) != 2 {
			t.Errorf("expected 2 columns per row, got %d", len(row))
		}
	}
}
