package loaders

import "time"

// FieldMapping defines how to extract and type a column.
type FieldMapping struct {
	Source string `json:"source"` // source field path (e.g., "source.ip" for ES)
	Column string `json:"column"` // output column name in Parquet
	Type   string `json:"type"`   // type hint: VARCHAR, INT64, FLOAT64, BOOLEAN, TIMESTAMP
}

// LoadResult describes the output of a completed load.
type LoadResult struct {
	ParquetPath    string        // absolute path to written Parquet file
	RowCount       int64         // total rows written
	FileSize       int64         // bytes on disk
	Duration       time.Duration // wall-clock time for the load
	RowGroups      int           // number of Parquet row groups written
	Partial        bool          // true if staging ended early (pagination error)
	PartialError   string        // the error that stopped pagination (when Partial=true)
	PagesCompleted int           // last successful page number (when paginated)
}

// DefaultChunkSize is the default number of rows per Parquet row group.
const DefaultChunkSize = 10_000

// DefaultTimeout is the default timeout for a load operation.
const DefaultTimeout = 5 * time.Minute

// DefaultMaxRows is the default streaming cap when max_rows is not specified.
const DefaultMaxRows = 500_000
