package graph

import (
	"context"
	"errors"
	"testing"
)

// stubGraphClient is a minimal GraphClient for testing the read-only wrapper.
type stubGraphClient struct {
	queryCalled  bool
	updateCalled bool
	closeCalled  bool
}

func (s *stubGraphClient) Query(_ context.Context, _ string, _ map[string]any) ([]Record, error) {
	s.queryCalled = true
	return []Record{{"name": "test"}}, nil
}

func (s *stubGraphClient) Update(_ context.Context, _ string, _ map[string]any) error {
	s.updateCalled = true
	return nil
}

func (s *stubGraphClient) Close() error {
	s.closeCalled = true
	return nil
}

func TestReadOnlyGraphClient_QueryPassesThrough(t *testing.T) {
	stub := &stubGraphClient{}
	ro := NewReadOnlyGraphClient(stub)

	records, err := ro.Query(context.Background(), "MATCH (n) RETURN n.name", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stub.queryCalled {
		t.Error("expected inner Query to be called")
	}
	if len(records) != 1 || records[0]["name"] != "test" {
		t.Errorf("unexpected records: %v", records)
	}
}

func TestReadOnlyGraphClient_UpdateReturnsError(t *testing.T) {
	stub := &stubGraphClient{}
	ro := NewReadOnlyGraphClient(stub)

	err := ro.Update(context.Background(), "CREATE (n:Test {name: 'bad'})", nil)
	if err == nil {
		t.Fatal("expected error from Update on read-only client")
	}
	if !errors.Is(err, ErrReadOnly) {
		t.Errorf("expected ErrReadOnly, got: %v", err)
	}
	if stub.updateCalled {
		t.Error("inner Update should not have been called")
	}
}

func TestReadOnlyGraphClient_ClosePassesThrough(t *testing.T) {
	stub := &stubGraphClient{}
	ro := NewReadOnlyGraphClient(stub)

	err := ro.Close()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stub.closeCalled {
		t.Error("expected inner Close to be called")
	}
}

func TestReadOnlyGraphClient_ImplementsInterface(t *testing.T) {
	stub := &stubGraphClient{}
	ro := NewReadOnlyGraphClient(stub)

	// Compile-time check: ReadOnlyGraphClient satisfies GraphClient.
	var _ GraphClient = ro
}
