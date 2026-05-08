package staging

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDuckDBCompatibility writes a Parquet file with all 5 types via parquet-go,
// then reads it with DuckDB CLI and verifies values and column types.
// Requires duckdb on PATH (skipped otherwise).
func TestDuckDBCompatibility(t *testing.T) {
	duckdbBin, err := exec.LookPath("duckdb")
	if err != nil {
		t.Skip("duckdb not found on PATH, skipping compatibility test")
	}

	dir := t.TempDir()

	// Write Parquet with all 5 types. Column names deliberately NOT alphabetical
	// to verify the alphabetical sort + round-trip works correctly.
	columns := []string{"name", "age", "score", "active", "created_at"}
	types := []string{"VARCHAR", "INT64", "FLOAT64", "BOOLEAN", "TIMESTAMP"}
	data := [][]any{
		{"alice", float64(30), float64(3.14), true, "2024-03-29T12:00:00Z"},
		{"bob", float64(25), float64(2.71), false, "2024-03-29T13:00:00Z"},
		{nil, nil, nil, nil, nil}, // null row
	}

	path, err := WriteParquet("compat_test", columns, types, data, dir)
	if err != nil {
		t.Fatalf("WriteParquet: %v", err)
	}

	// Verify file exists and is non-empty
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("parquet file is empty")
	}

	// --- DuckDB: check column types ---
	typeQuery := "SELECT column_name, column_type FROM (DESCRIBE SELECT * FROM read_parquet('" + path + "')) ORDER BY column_name;"
	typeOut := runDuckDB(t, duckdbBin, typeQuery)

	// Expected types (alphabetical by column name):
	// active -> BOOLEAN, age -> BIGINT, created_at -> TIMESTAMP, name -> VARCHAR, score -> DOUBLE
	assertContains(t, typeOut, "active", "BOOLEAN")
	assertContains(t, typeOut, "age", "BIGINT")
	assertContains(t, typeOut, "created_at", "TIMESTAMP") // may be "TIMESTAMP WITH TIME ZONE"
	assertContains(t, typeOut, "name", "VARCHAR")        // was BLOB before parquet.String() fix
	assertContains(t, typeOut, "score", "DOUBLE")

	// --- DuckDB: check values ---
	valueQuery := "SELECT * FROM read_parquet('" + path + "') WHERE name = 'alice' ORDER BY name;"
	valueOut := runDuckDB(t, duckdbBin, valueQuery)

	assertContains(t, valueOut, "alice")
	assertContains(t, valueOut, "30")
	assertContains(t, valueOut, "3.14")
	assertContains(t, valueOut, "true")
	assertContains(t, valueOut, "2024-03-29")

	// --- DuckDB: check nulls ---
	nullQuery := "SELECT count(*) AS null_count FROM read_parquet('" + path + "') WHERE name IS NULL;"
	nullOut := runDuckDB(t, duckdbBin, nullQuery)
	assertContains(t, nullOut, "1") // one null row

	// --- DuckDB: check row count ---
	countQuery := "SELECT count(*) AS total FROM read_parquet('" + path + "');"
	countOut := runDuckDB(t, duckdbBin, countQuery)
	assertContains(t, countOut, "3")

	// --- DuckDB: precise type verification via typeof() ---
	typeofQuery := "SELECT typeof(active), typeof(age), typeof(created_at), typeof(name), typeof(score) FROM read_parquet('" + path + "') WHERE name = 'alice';"
	typeofOut := runDuckDB(t, duckdbBin, typeofQuery)

	assertContains(t, typeofOut, "BOOLEAN")
	assertContains(t, typeofOut, "BIGINT")
	assertContains(t, typeofOut, "TIMESTAMP")
	assertContains(t, typeofOut, "VARCHAR")
	assertContains(t, typeofOut, "DOUBLE")

	t.Logf("DuckDB compatibility verified: all 5 types (VARCHAR, INT64, FLOAT64, BOOLEAN, TIMESTAMP) round-trip correctly")
}

// TestDuckDBStreamingWriterCompat verifies that StreamingParquetWriter output
// is also readable by DuckDB (multi-row-group files).
func TestDuckDBStreamingWriterCompat(t *testing.T) {
	duckdbBin, err := exec.LookPath("duckdb")
	if err != nil {
		t.Skip("duckdb not found on PATH")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "streaming.parquet")

	fields := []FieldMapping{
		{Source: "id", Column: "id", Type: "INT64"},
		{Source: "value", Column: "value", Type: "VARCHAR"},
	}
	schema, sortedFields := BuildSchema("streaming", fields)

	pw, err := NewStreamingParquetWriter(path, schema)
	if err != nil {
		t.Fatalf("NewStreamingParquetWriter: %v", err)
	}

	// Write 2 row groups
	for chunk := 0; chunk < 2; chunk++ {
		hits := []map[string]any{
			{"id": float64(chunk*10 + 1), "value": "a"},
			{"id": float64(chunk*10 + 2), "value": "b"},
		}
		rows := BuildRows(sortedFields, hits)
		if err := pw.WriteRows(rows); err != nil {
			t.Fatalf("WriteRows chunk %d: %v", chunk, err)
		}
		if err := pw.Flush(); err != nil {
			t.Fatalf("Flush chunk %d: %v", chunk, err)
		}
	}
	if err := pw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// DuckDB should read all 4 rows across 2 row groups
	countOut := runDuckDB(t, duckdbBin, "SELECT count(*) FROM read_parquet('"+path+"');")
	assertContains(t, countOut, "4")

	t.Logf("DuckDB streaming writer compatibility verified: %d rows across %d row groups", pw.Rows(), pw.RowGroups())
}

func runDuckDB(t *testing.T, bin, query string) string {
	t.Helper()
	cmd := exec.Command(bin, "-csv", "-noheader", "-c", query)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("duckdb query failed: %v\nquery: %s\noutput: %s", err, query, string(out))
	}
	return string(out)
}

func assertContains(t *testing.T, output string, substrs ...string) {
	t.Helper()
	for _, s := range substrs {
		if !strings.Contains(output, s) {
			t.Errorf("output does not contain %q:\n%s", s, output)
		}
	}
}
