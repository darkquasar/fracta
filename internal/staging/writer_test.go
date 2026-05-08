package staging

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/parquet-go/parquet-go"
)

func TestStreamingParquetWriter_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.parquet")

	fields := []FieldMapping{
		{Source: "name", Column: "name", Type: "VARCHAR"},
		{Source: "age", Column: "age", Type: "INT64"},
	}
	schema, sortedFields := BuildSchema("test", fields)

	pw, err := NewStreamingParquetWriter(path, schema)
	if err != nil {
		t.Fatalf("create writer: %v", err)
	}

	// Write two batches (two row groups)
	for batch := 0; batch < 2; batch++ {
		hits := make([]map[string]any, 5)
		for i := range hits {
			hits[i] = map[string]any{"name": "alice", "age": float64(30 + i)}
		}
		rows := BuildRows(sortedFields, hits)
		if err := pw.WriteRows(rows); err != nil {
			t.Fatalf("write batch %d: %v", batch, err)
		}
		if err := pw.Flush(); err != nil {
			t.Fatalf("flush batch %d: %v", batch, err)
		}
	}

	if pw.Rows() != 10 {
		t.Errorf("expected 10 rows, got %d", pw.Rows())
	}
	if pw.RowGroups() != 2 {
		t.Errorf("expected 2 row groups, got %d", pw.RowGroups())
	}

	if err := pw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Verify file exists and is non-empty
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() == 0 {
		t.Error("expected non-empty parquet file")
	}

	// Verify we can read back the right number of rows
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer f.Close()

	pf, err := parquet.OpenFile(f, info.Size())
	if err != nil {
		t.Fatalf("open parquet: %v", err)
	}

	if pf.NumRows() != 10 {
		t.Errorf("expected 10 rows in file, got %d", pf.NumRows())
	}
}

func TestStreamingParquetWriter_EmptyClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.parquet")

	fields := []FieldMapping{
		{Source: "col", Column: "col", Type: "VARCHAR"},
	}
	schema, _ := BuildSchema("test", fields)

	pw, err := NewStreamingParquetWriter(path, schema)
	if err != nil {
		t.Fatalf("create writer: %v", err)
	}

	if pw.Rows() != 0 {
		t.Errorf("expected 0 rows, got %d", pw.Rows())
	}

	if err := pw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// File should exist (valid Parquet with 0 rows)
	_, err = os.Stat(path)
	if err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}
}

func TestStreamingParquetWriter_MultipleTypes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "typed.parquet")

	fields := []FieldMapping{
		{Source: "str", Column: "str", Type: "VARCHAR"},
		{Source: "num", Column: "num", Type: "INT64"},
		{Source: "dbl", Column: "dbl", Type: "DOUBLE"},
		{Source: "flag", Column: "flag", Type: "BOOLEAN"},
	}
	schema, sortedFields := BuildSchema("test", fields)

	pw, err := NewStreamingParquetWriter(path, schema)
	if err != nil {
		t.Fatalf("create writer: %v", err)
	}

	hits := []map[string]any{
		{"str": "hello", "num": float64(42), "dbl": float64(3.14), "flag": true},
	}
	rows := BuildRows(sortedFields, hits)
	if err := pw.WriteRows(rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := pw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if pw.Rows() != 1 {
		t.Errorf("expected 1 row, got %d", pw.Rows())
	}
}
