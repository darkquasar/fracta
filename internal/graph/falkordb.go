package graph

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/darkquasar/fracta/internal/fractalog"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// FalkorDBClient implements GraphClient using FalkorDB (Redis module).
type FalkorDBClient struct {
	rdb       *redis.Client
	graphName string
	logger    *slog.Logger
}

// FalkorDBOption configures a FalkorDBClient.
type FalkorDBOption func(*FalkorDBClient)

// WithGraphName overrides the default graph name.
func WithGraphName(name string) FalkorDBOption {
	return func(c *FalkorDBClient) { c.graphName = name }
}

// NewFalkorDBClient creates a FalkorDB-backed GraphClient.
func NewFalkorDBClient(addr string, opts ...FalkorDBOption) *FalkorDBClient {
	c := &FalkorDBClient{
		rdb:       redis.NewClient(&redis.Options{Addr: addr}),
		graphName: DefaultGraphName,
		logger:    fractalog.Component("graph"),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Ping verifies connectivity to the FalkorDB server.
func (c *FalkorDBClient) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// Query executes a read Cypher query and returns rows as Records.
func (c *FalkorDBClient) Query(ctx context.Context, cypher string, params map[string]any) ([]Record, error) {
	start := time.Now()
	res, err := c.rdb.Do(ctx, "GRAPH.RO_QUERY", c.graphName, buildCypherPrefix(params)+cypher).Result()
	if err != nil {
		return nil, fmt.Errorf("graph query: %w", err)
	}
	c.logger.Debug("query", "duration_ms", time.Since(start).Milliseconds(), "graph", c.graphName)
	return parseResult(res)
}

// Update executes a write Cypher query.
func (c *FalkorDBClient) Update(ctx context.Context, cypher string, params map[string]any) error {
	_, err := c.rdb.Do(ctx, "GRAPH.QUERY", c.graphName, buildCypherPrefix(params)+cypher).Result()
	if err != nil {
		return fmt.Errorf("graph update: %w", err)
	}
	return nil
}

// buildCypherPrefix serializes a Go map to FalkorDB's CYPHER key=value prefix syntax.
// Example: map[string]any{"name": "CloudTrail", "count": 42} → "CYPHER name='CloudTrail' count=42 "
func buildCypherPrefix(params map[string]any) string {
	if len(params) == 0 {
		return ""
	}

	// Sort keys for deterministic output.
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("CYPHER ")
	for _, k := range keys {
		v := params[k]
		switch val := v.(type) {
		case string:
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteByte('\'')
			b.WriteString(strings.ReplaceAll(val, "'", "\\'"))
			b.WriteByte('\'')
		case int:
			fmt.Fprintf(&b, "%s=%d", k, val)
		case int64:
			fmt.Fprintf(&b, "%s=%d", k, val)
		case float64:
			fmt.Fprintf(&b, "%s=%v", k, val)
		case bool:
			fmt.Fprintf(&b, "%s=%t", k, val)
		default:
			if v == nil {
				continue
			}
			fmt.Fprintf(&b, "%s=%v", k, val)
		}
		b.WriteByte(' ')
	}

	return b.String()
}

// CreateUniqueConstraint issues a FalkorDB GRAPH.CONSTRAINT CREATE command for
// a single-property uniqueness rule. FalkorDB rejects the Neo4j-style
// CREATE CONSTRAINT ... ASSERT Cypher syntax — it expects a top-level Redis
// command instead. The constraint requires a matching index to already exist
// (callers create the index first via GRAPH.QUERY CREATE INDEX).
//
// FalkorDB returns "PENDING" while the constraint is enforced asynchronously;
// repeated creates against an existing constraint return an error containing
// "already exists" which the caller may safely ignore.
func (c *FalkorDBClient) CreateUniqueConstraint(ctx context.Context, label, property string) error {
	_, err := c.rdb.Do(ctx,
		"GRAPH.CONSTRAINT", "CREATE", c.graphName,
		"UNIQUE", "NODE", label,
		"PROPERTIES", "1", property,
	).Result()
	if err != nil {
		return fmt.Errorf("graph.constraint create %s.%s: %w", label, property, err)
	}
	return nil
}

// Close releases the Redis connection.
func (c *FalkorDBClient) Close() error {
	return c.rdb.Close()
}

// DeleteGraph drops the entire graph. Used for testing.
func (c *FalkorDBClient) DeleteGraph(ctx context.Context) error {
	_, err := c.rdb.Do(ctx, "GRAPH.DELETE", c.graphName).Result()
	return err
}

// parseResult converts FalkorDB's [headers, rows, stats] response into Records.
// FalkorDB GRAPH.QUERY returns: []interface{} with 3 elements:
//   - [0] headers: []interface{} of column names (strings)
//   - [1] rows:    []interface{} of []interface{} row values
//   - [2] stats:   []interface{} of stat strings (ignored)
func parseResult(res interface{}) ([]Record, error) {
	arr, ok := res.([]interface{})
	if !ok || len(arr) < 2 {
		return nil, nil
	}

	// Parse headers.
	rawHeaders, ok := arr[0].([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected header type: %T", arr[0])
	}
	headers := make([]string, len(rawHeaders))
	for i, h := range rawHeaders {
		s, ok := h.(string)
		if !ok {
			return nil, fmt.Errorf("header %d: expected string, got %T", i, h)
		}
		headers[i] = s
	}

	// Parse rows.
	rawRows, ok := arr[1].([]interface{})
	if !ok {
		return nil, nil
	}

	records := make([]Record, 0, len(rawRows))
	for _, rr := range rawRows {
		rowVals, ok := rr.([]interface{})
		if !ok {
			continue
		}
		rec := make(Record, len(headers))
		for i, h := range headers {
			if i < len(rowVals) {
				rec[h] = rowVals[i]
			}
		}
		records = append(records, rec)
	}
	return records, nil
}
