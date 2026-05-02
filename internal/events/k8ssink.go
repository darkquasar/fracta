package events

import (
	"context"
	"log/slog"

	"github.com/darkquasar/fracta/internal/fractalog"
)

// K8sEventRecorder is the minimal interface for writing Kubernetes Events.
// The concrete implementation lives in the runtime/k8s package and wraps
// the real client-go EventRecorder. Local-process mode never constructs
// the concrete type, so client-go is never imported.
type K8sEventRecorder interface {
	Record(eventType, reason, message string)
}

// K8sEventSink writes selected infra-facing events as Kubernetes Events.
// Best-effort: failures are logged, never block core flows.
type K8sEventSink struct {
	recorder K8sEventRecorder
	log      *slog.Logger
}

// NewK8sEventSink creates a K8sEventSink backed by the given recorder.
func NewK8sEventSink(recorder K8sEventRecorder) *K8sEventSink {
	return &K8sEventSink{
		recorder: recorder,
		log:      fractalog.Component("events"),
	}
}

// Handle writes the event as a Kubernetes Event if it matches the infra
// event filter. Non-matching events are silently dropped.
func (s *K8sEventSink) Handle(_ context.Context, e Event) error {
	reason, message, ok := k8sEventFields(e)
	if !ok {
		return nil
	}

	eventType := "Normal"
	if e.Severity == "warn" || e.Severity == "error" {
		eventType = "Warning"
	}

	s.recorder.Record(eventType, reason, message)
	return nil
}

// String returns the sink name for logging.
func (s *K8sEventSink) String() string { return "K8sEventSink" }

// k8sEventFields maps a fracta event to Kubernetes Event fields.
// Returns false if the event should not be forwarded to K8s.
func k8sEventFields(e Event) (reason, message string, ok bool) {
	switch {
	// Gateway status changes: e.g., gateway became ready.
	case e.Component == "gateway" && e.Action == "status_change":
		status := e.Attrs["status"]
		if status == "" {
			status = "unknown"
		}
		return "GatewayStatusChange", "Gateway status: " + status, true

	// Runtime K8s job creation.
	case e.Component == "runtime.k8s" && e.Action == "job_create":
		return "JobCreated", detailOrDefault(e, "K8s job created for task "+e.Task), true

	// Runtime K8s pod scheduled.
	case e.Component == "runtime.k8s" && e.Action == "pod_schedule":
		return "PodScheduled", detailOrDefault(e, "Pod scheduled for task "+e.Task), true

	// Backend connection failures.
	case e.Component == "mcpclient" && e.Action == "connect_attempt" && (e.Outcome == "failure" || e.Outcome == "timeout"):
		return "BackendConnectFailed", detailOrDefault(e, "Backend connection "+e.Outcome+": "+e.Resource), true

	default:
		return "", "", false
	}
}

func detailOrDefault(e Event, fallback string) string {
	if e.Detail != "" {
		return e.Detail
	}
	return fallback
}
