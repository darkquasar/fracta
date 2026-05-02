package events

import (
	"context"
	"sync"
	"testing"
)

// fakeRecorder captures K8s events for testing.
type fakeRecorder struct {
	mu     sync.Mutex
	events []k8sRecord
}

type k8sRecord struct {
	EventType string
	Reason    string
	Message   string
}

func (r *fakeRecorder) Record(eventType, reason, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, k8sRecord{eventType, reason, message})
}

func (r *fakeRecorder) Events() []k8sRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]k8sRecord, len(r.events))
	copy(cp, r.events)
	return cp
}

func TestK8sEventSink_GatewayReady(t *testing.T) {
	rec := &fakeRecorder{}
	sink := NewK8sEventSink(rec)

	e := Info("gateway", "status_change")
	e.Attrs = map[string]string{"status": "ready"}

	if err := sink.Handle(context.Background(), e); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	evts := rec.Events()
	if len(evts) != 1 {
		t.Fatalf("got %d k8s events, want 1", len(evts))
	}
	if evts[0].Reason != "GatewayStatusChange" {
		t.Errorf("Reason = %q, want GatewayStatusChange", evts[0].Reason)
	}
	if evts[0].EventType != "Normal" {
		t.Errorf("EventType = %q, want Normal", evts[0].EventType)
	}
}

func TestK8sEventSink_BackendConnectFailure(t *testing.T) {
	rec := &fakeRecorder{}
	sink := NewK8sEventSink(rec)

	e := Error("mcpclient", "connect_attempt", "connection refused")
	e.Outcome = "failure"
	e.Resource = "mcp_server:vendor"

	if err := sink.Handle(context.Background(), e); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	evts := rec.Events()
	if len(evts) != 1 {
		t.Fatalf("got %d k8s events, want 1", len(evts))
	}
	if evts[0].Reason != "BackendConnectFailed" {
		t.Errorf("Reason = %q, want BackendConnectFailed", evts[0].Reason)
	}
	if evts[0].EventType != "Warning" {
		t.Errorf("EventType = %q, want Warning", evts[0].EventType)
	}
}

func TestK8sEventSink_FiltersNonInfraEvents(t *testing.T) {
	rec := &fakeRecorder{}
	sink := NewK8sEventSink(rec)

	// An orchestrator agent create event should be filtered out.
	e := Info("orchestrator", "create")
	e.Category = "agent"
	_ = sink.Handle(context.Background(), e)

	if len(rec.Events()) != 0 {
		t.Errorf("non-infra event should be filtered, got %d k8s events", len(rec.Events()))
	}
}

func TestK8sEventSink_JobCreate(t *testing.T) {
	rec := &fakeRecorder{}
	sink := NewK8sEventSink(rec)

	e := Info("runtime.k8s", "job_create")
	e.Task = "agent-1"
	_ = sink.Handle(context.Background(), e)

	evts := rec.Events()
	if len(evts) != 1 {
		t.Fatalf("got %d k8s events, want 1", len(evts))
	}
	if evts[0].Reason != "JobCreated" {
		t.Errorf("Reason = %q, want JobCreated", evts[0].Reason)
	}
}
