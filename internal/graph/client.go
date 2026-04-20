package graph

import "context"

// Record maps column names to values from a graph query result.
type Record map[string]interface{}

// DefaultGraphName is the FalkorDB graph key.
const DefaultGraphName = "fracta_knowledge"

// GraphClient abstracts the graph database backend.
// Local dev uses FalkorDB (Redis module); production uses Neptune (HTTPS).
type GraphClient interface {
	// Query executes a read-only Cypher query and returns rows as Records.
	// Each Record maps the RETURN column name to its value.
	Query(ctx context.Context, cypher string, params map[string]any) ([]Record, error)

	// Update executes a write Cypher query (CREATE, MERGE, SET, DELETE).
	Update(ctx context.Context, cypher string, params map[string]any) error

	// Close releases the underlying connection.
	Close() error
}
