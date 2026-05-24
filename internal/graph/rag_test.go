package graph

import (
	"context"
	"testing"
)

// mockGraphClient is a minimal GraphClient for testing.
type mockGraphClient struct{}

func (m *mockGraphClient) Query(_ context.Context, _ string, _ map[string]any) ([]Record, error) {
	return nil, nil
}
func (m *mockGraphClient) Update(_ context.Context, _ string, _ map[string]any) error { return nil }
func (m *mockGraphClient) Close() error                                               { return nil }

func TestGraphRAGContextAcceptsAnySemantics(t *testing.T) {
	ctx := context.Background()
	client := &mockGraphClient{}

	_, err := GraphRAGContext(ctx, client, []string{"ip_address", "hostname"})
	if err != nil {
		t.Fatalf("unexpected error for semantics: %v", err)
	}

	// Unknown semantics are no longer rejected — graph queries return empty results.
	_, err = GraphRAGContext(ctx, client, []string{"ip_address", "unknown_type"})
	if err != nil {
		t.Fatalf("unexpected error for unknown semantics: %v", err)
	}
}

func TestGraphRAGContextEmptySemantics(t *testing.T) {
	ctx := context.Background()
	client := &mockGraphClient{}

	rc, err := GraphRAGContext(ctx, client, nil)
	if err != nil {
		t.Fatalf("unexpected error for nil semantics: %v", err)
	}
	if rc == nil {
		t.Fatal("expected non-nil RAGContext")
	}
}
