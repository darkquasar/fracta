package events

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/darkquasar/fracta/internal/fractalog"
)

// RemoteBus implements Bus by POSTing event batches to the control plane's
// HTTP ingest endpoint. Used by K8s workers that cannot share an in-process bus.
type RemoteBus struct {
	baseURL    string
	task       string
	httpClient *http.Client
	log        *slog.Logger

	// Batch accumulator: events are collected and flushed periodically or
	// when the batch reaches maxBatchSize.
	mu           sync.Mutex
	pending      []Event
	maxBatchSize int
	flushTicker  *time.Ticker
	done         chan struct{}
	stopped      bool
}

// RemoteBusConfig configures a RemoteBus.
type RemoteBusConfig struct {
	// BaseURL is the control plane API base URL (e.g., "http://fracta-controlplane:9090").
	BaseURL string
	// Task is the agent task name used in the ingest URL path.
	Task string
	// MaxBatchSize is the max events per POST. Default 10.
	MaxBatchSize int
	// FlushInterval is how often to flush pending events. Default 5s.
	FlushInterval time.Duration
	// HTTPTimeout is the timeout for each POST request. Default 10s.
	HTTPTimeout time.Duration
}

// NewRemoteBus creates a RemoteBus that batches and POSTs events to the CP.
func NewRemoteBus(cfg RemoteBusConfig) *RemoteBus {
	if cfg.MaxBatchSize <= 0 {
		cfg.MaxBatchSize = 10
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 5 * time.Second
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 10 * time.Second
	}

	rb := &RemoteBus{
		baseURL: cfg.BaseURL,
		task:    cfg.Task,
		httpClient: &http.Client{
			Timeout: cfg.HTTPTimeout,
		},
		log:          fractalog.Component("remote_bus"),
		pending:      make([]Event, 0, cfg.MaxBatchSize),
		maxBatchSize: cfg.MaxBatchSize,
		flushTicker:  time.NewTicker(cfg.FlushInterval),
		done:         make(chan struct{}),
	}

	go rb.flushLoop()
	return rb
}

// Emit adds the event to the pending batch. If the batch is full, it triggers
// an immediate flush. Emit is non-blocking and fire-and-forget.
func (rb *RemoteBus) Emit(_ context.Context, e Event) {
	rb.mu.Lock()
	if rb.stopped {
		rb.mu.Unlock()
		return
	}
	rb.pending = append(rb.pending, e)
	shouldFlush := len(rb.pending) >= rb.maxBatchSize
	rb.mu.Unlock()

	if shouldFlush {
		rb.flush()
	}
}

// Close stops the flush loop and sends any remaining events.
func (rb *RemoteBus) Close() {
	rb.mu.Lock()
	if rb.stopped {
		rb.mu.Unlock()
		return
	}
	rb.stopped = true
	rb.mu.Unlock()

	rb.flushTicker.Stop()
	close(rb.done)

	// Final flush.
	rb.flush()
}

// flushLoop periodically flushes pending events.
func (rb *RemoteBus) flushLoop() {
	for {
		select {
		case <-rb.flushTicker.C:
			rb.flush()
		case <-rb.done:
			return
		}
	}
}

// flush sends all pending events to the CP in a single POST.
func (rb *RemoteBus) flush() {
	rb.mu.Lock()
	if len(rb.pending) == 0 {
		rb.mu.Unlock()
		return
	}
	batch := rb.pending
	rb.pending = make([]Event, 0, rb.maxBatchSize)
	rb.mu.Unlock()

	if err := rb.post(batch); err != nil {
		rb.log.Warn("failed to post events",
			"task", rb.task,
			"batch_size", len(batch),
			"error", err,
		)
		// Re-queue events on failure (best effort, may lose on repeated failures).
		rb.mu.Lock()
		if !rb.stopped {
			// Prepend failed batch to pending (cap at 2x maxBatchSize to bound memory).
			combined := append(batch, rb.pending...)
			if len(combined) > rb.maxBatchSize*2 {
				combined = combined[len(combined)-rb.maxBatchSize*2:]
			}
			rb.pending = combined
		}
		rb.mu.Unlock()
	}
}

// ingestRequest is the JSON body for POST /api/v1/agents/{task}/events.
type ingestRequest struct {
	Events []eventPayload `json:"events"`
}

// eventPayload is the JSON representation of an event for remote ingest.
type eventPayload struct {
	EventID     string            `json:"event_id"`
	Time        time.Time         `json:"time"`
	Component   string            `json:"component"`
	Category    string            `json:"category,omitempty"`
	Resource    string            `json:"resource,omitempty"`
	Action      string            `json:"action"`
	Outcome     string            `json:"outcome,omitempty"`
	Severity    string            `json:"severity,omitempty"`
	Task        string            `json:"task"`
	MissionID   int64             `json:"mission_id,omitempty"`
	ObjectiveID string            `json:"objective_id,omitempty"`
	Detail      string            `json:"detail,omitempty"`
	Attrs       map[string]string `json:"attrs,omitempty"`
}

func toPayload(e Event) eventPayload {
	return eventPayload{
		EventID:     e.ID,
		Time:        e.Time,
		Component:   e.Component,
		Category:    e.Category,
		Resource:    e.Resource,
		Action:      e.Action,
		Outcome:     e.Outcome,
		Severity:    e.Severity,
		Task:        e.Task,
		MissionID:   e.MissionID,
		ObjectiveID: e.ObjectiveID,
		Detail:      e.Detail,
		Attrs:       e.Attrs,
	}
}

// FromPayload converts an eventPayload back to an Event.
func FromPayload(p eventPayload) Event {
	return Event{
		ID:          p.EventID,
		Time:        p.Time,
		Component:   p.Component,
		Category:    p.Category,
		Resource:    p.Resource,
		Action:      p.Action,
		Outcome:     p.Outcome,
		Severity:    p.Severity,
		Task:        p.Task,
		MissionID:   p.MissionID,
		ObjectiveID: p.ObjectiveID,
		Detail:      p.Detail,
		Attrs:       p.Attrs,
	}
}

func (rb *RemoteBus) post(batch []Event) error {
	payloads := make([]eventPayload, len(batch))
	for i, e := range batch {
		payloads[i] = toPayload(e)
	}

	body, err := json.Marshal(ingestRequest{Events: payloads})
	if err != nil {
		return fmt.Errorf("marshal batch: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/agents/%s/events", rb.baseURL, rb.task)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := rb.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post events: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			rb.log.Warn("close response body", "err", err)
		}
	}()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	return nil
}
