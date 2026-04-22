package graph

import (
	"context"
	"errors"
)

// ErrReadOnly is returned when a write operation is attempted on a read-only graph client.
var ErrReadOnly = errors.New("graph client is read-only: strategy contract does not declare graph_write: true")

// ReadOnlyGraphClient wraps a GraphClient and rejects all write operations.
// Used when a strategy's contract does not declare graph_write: true.
type ReadOnlyGraphClient struct {
	inner GraphClient
}

// NewReadOnlyGraphClient wraps the given GraphClient in a read-only proxy.
// Query calls pass through; Update calls return ErrReadOnly.
func NewReadOnlyGraphClient(inner GraphClient) *ReadOnlyGraphClient {
	return &ReadOnlyGraphClient{inner: inner}
}

// Query passes through to the underlying client.
func (r *ReadOnlyGraphClient) Query(ctx context.Context, cypher string, params map[string]any) ([]Record, error) {
	return r.inner.Query(ctx, cypher, params)
}

// Update always returns ErrReadOnly.
func (r *ReadOnlyGraphClient) Update(_ context.Context, _ string, _ map[string]any) error {
	return ErrReadOnly
}

// Close passes through to the underlying client.
func (r *ReadOnlyGraphClient) Close() error {
	return r.inner.Close()
}
