package events

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRemoteBus_PostsBatchOnFull(t *testing.T) {
	var mu sync.Mutex
	var received []ingestRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ingestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode error: %v", err)
			http.Error(w, "bad request", 400)
			return
		}
		mu.Lock()
		received = append(received, req)
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"accepted":` + fmt.Sprintf("%d", len(req.Events)) + `,"dropped":0}`))
	}))
	defer srv.Close()

	rb := NewRemoteBus(RemoteBusConfig{
		BaseURL:       srv.URL,
		Task:          "agent-1",
		MaxBatchSize:  3,
		FlushInterval: 1 * time.Hour, // won't trigger automatically in test
	})
	defer rb.Close()

	// Emit 3 events — triggers flush at maxBatchSize.
	for i := 0; i < 3; i++ {
		rb.Emit(context.Background(), Event{
			ID:        fmt.Sprintf("e%d", i+1),
			Time:      time.Now(),
			Task:      "agent-1",
			Component: "worker",
			Action:    "heartbeat",
		})
	}

	// Wait briefly for async flush.
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("expected at least one batch POST")
	}
	if len(received[0].Events) != 3 {
		t.Errorf("batch size = %d, want 3", len(received[0].Events))
	}
	if received[0].Events[0].EventID != "e1" {
		t.Errorf("first event ID = %q, want %q", received[0].Events[0].EventID, "e1")
	}
}

func TestRemoteBus_FlushOnClose(t *testing.T) {
	var mu sync.Mutex
	var received []ingestRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ingestRequest
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		received = append(received, req)
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	rb := NewRemoteBus(RemoteBusConfig{
		BaseURL:       srv.URL,
		Task:          "agent-1",
		MaxBatchSize:  100, // won't trigger on size
		FlushInterval: 1 * time.Hour,
	})

	rb.Emit(context.Background(), Event{
		ID: "e1", Time: time.Now(), Task: "agent-1", Action: "heartbeat",
	})

	// Close triggers final flush.
	rb.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("Close should flush remaining events")
	}
	if len(received[0].Events) != 1 {
		t.Errorf("batch size = %d, want 1", len(received[0].Events))
	}
}

func TestRemoteBus_RequeuesOnFailure(t *testing.T) {
	callCount := 0
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		count := callCount
		mu.Unlock()

		if count == 1 {
			// First call fails.
			http.Error(w, "server error", 500)
			return
		}
		// Subsequent calls succeed.
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	rb := NewRemoteBus(RemoteBusConfig{
		BaseURL:       srv.URL,
		Task:          "agent-1",
		MaxBatchSize:  2,
		FlushInterval: 50 * time.Millisecond,
	})
	defer rb.Close()

	// Emit 2 events — triggers flush, which will fail.
	rb.Emit(context.Background(), Event{ID: "e1", Time: time.Now(), Task: "agent-1", Action: "heartbeat"})
	rb.Emit(context.Background(), Event{ID: "e2", Time: time.Now(), Task: "agent-1", Action: "heartbeat"})

	// Wait for retry via ticker.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if callCount < 2 {
		t.Errorf("expected at least 2 POST attempts (initial + retry), got %d", callCount)
	}
}

func TestRemoteBus_EmitAfterClose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	rb := NewRemoteBus(RemoteBusConfig{
		BaseURL:       srv.URL,
		Task:          "agent-1",
		MaxBatchSize:  10,
		FlushInterval: 1 * time.Hour,
	})

	rb.Close()

	// Should not panic.
	rb.Emit(context.Background(), Event{ID: "e1", Time: time.Now(), Task: "agent-1", Action: "heartbeat"})
}

func TestRemoteBus_CorrectURL(t *testing.T) {
	var receivedPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	rb := NewRemoteBus(RemoteBusConfig{
		BaseURL:       srv.URL,
		Task:          "research-foo",
		MaxBatchSize:  1,
		FlushInterval: 1 * time.Hour,
	})
	defer rb.Close()

	rb.Emit(context.Background(), Event{ID: "e1", Time: time.Now(), Task: "research-foo", Action: "heartbeat"})

	time.Sleep(50 * time.Millisecond)

	if receivedPath != "/api/v1/agents/research-foo/events" {
		t.Errorf("path = %q, want %q", receivedPath, "/api/v1/agents/research-foo/events")
	}
}

func TestRemoteBus_PayloadRoundTrip(t *testing.T) {
	original := Event{
		ID:          "test-id",
		Time:        time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
		Component:   "worker",
		Category:    "agent_activity",
		Resource:    "task:research-foo",
		Action:      "heartbeat",
		Outcome:     "success",
		Severity:    "debug",
		Task:        "research-foo",
		MissionID:   42,
		ObjectiveID: "obj-abc",
		Detail:      "heartbeat check",
		Attrs:       map[string]string{"phase": "executing", "tool": "Bash"},
	}

	payload := toPayload(original)
	roundTripped := FromPayload(payload)

	if roundTripped.ID != original.ID {
		t.Errorf("ID mismatch: %q vs %q", roundTripped.ID, original.ID)
	}
	if roundTripped.Component != original.Component {
		t.Errorf("Component mismatch")
	}
	if roundTripped.Action != original.Action {
		t.Errorf("Action mismatch")
	}
	if roundTripped.MissionID != original.MissionID {
		t.Errorf("MissionID mismatch: %d vs %d", roundTripped.MissionID, original.MissionID)
	}
	if roundTripped.Attrs["phase"] != "executing" {
		t.Errorf("Attrs[phase] = %q", roundTripped.Attrs["phase"])
	}
}
