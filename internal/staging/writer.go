package staging

import (
	"fmt"
	"os"

	"github.com/parquet-go/parquet-go"
)

// StreamingParquetWriter writes parquet.Row slices as Parquet row groups.
// Each WriteRows() call buffers data; Flush() writes a row group.
// Close() finalizes the file.
type StreamingParquetWriter struct {
	writer *parquet.Writer
	file   *os.File
	schema *parquet.Schema
	rows   int64
	groups int
}

// NewStreamingParquetWriter creates a Parquet file writer with Snappy compression.
// The caller must call Close() to finalize the file.
func NewStreamingParquetWriter(path string, schema *parquet.Schema) (*StreamingParquetWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create file: %w", err)
	}

	writer := parquet.NewWriter(f, schema,
		parquet.Compression(&parquet.Snappy),
	)

	return &StreamingParquetWriter{
		writer: writer,
		file:   f,
		schema: schema,
	}, nil
}

// WriteRows writes a batch of rows. Call Flush() after to create a row group,
// or let Close() flush the final group.
func (w *StreamingParquetWriter) WriteRows(rows []parquet.Row) error {
	n, err := w.writer.WriteRows(rows)
	if err != nil {
		return fmt.Errorf("write rows: %w", err)
	}
	w.rows += int64(n)
	return nil
}

// Flush writes the buffered rows as a new row group.
func (w *StreamingParquetWriter) Flush() error {
	if err := w.writer.Flush(); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	w.groups++
	return nil
}

// Close finalizes the Parquet file (writes any remaining rows as a final
// row group, then writes the footer). Also closes the underlying file.
func (w *StreamingParquetWriter) Close() error {
	if err := w.writer.Close(); err != nil {
		w.file.Close()
		return fmt.Errorf("close writer: %w", err)
	}
	return w.file.Close()
}

// Rows returns the total number of rows written so far.
func (w *StreamingParquetWriter) Rows() int64 { return w.rows }

// RowGroups returns the number of explicitly flushed row groups.
// Note: Close() may write one additional row group for unflushed data.
func (w *StreamingParquetWriter) RowGroups() int { return w.groups }

// Schema returns the parquet.Schema used by this writer.
func (w *StreamingParquetWriter) Schema() *parquet.Schema { return w.schema }
