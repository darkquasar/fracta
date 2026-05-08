package cpapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/state"
)

// watchHandler implements the SSE watch endpoint for live event streaming.
type watchHandler struct {
	hub           *events.SSEHub
	eventStore    *events.EventStore
	eventReader   state.EventReader
	snapshotStore *events.SnapshotStore
}

// handleWatch streams events for an agent via Server-Sent Events.
// GET /api/v1/agents/{name}/watch
// Query params:
//   - ?since=<event_id> — catch up from this event in the ring buffer
func (wh *watchHandler) handleWatch(w http.ResponseWriter, r *http.Request) {
	task := r.PathValue("name")
	if task == "" {
		writeError(w, http.StatusBadRequest, "missing agent name")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Subscribe FIRST so no events are lost between catch-up and live tail.
	ch := wh.hub.Subscribe(task)
	defer wh.hub.Unsubscribe(task, ch)

	// Resolve catch-up cursor: ?since= param takes precedence, then Last-Event-ID header.
	since := r.URL.Query().Get("since")
	if since == "" {
		since = r.Header.Get("Last-Event-ID")
	}

	// Catch-up: ring buffer first, DB fallback if evicted (spec S6 reconnect contract).
	seenIDs := make(map[string]struct{})
	if since != "" && wh.eventStore != nil {
		catchUp := wh.eventStore.Since(task, since)
		if catchUp == nil && wh.eventReader != nil {
			// Ring miss — fall back to DB for late reconnect / CP restart.
			// Use ring capacity as limit for DB catch-up (matches live buffer window).
			limit := events.DefaultRingSize
			if wh.eventStore != nil {
				limit = wh.eventStore.RingSize()
			}
			if dbEvents, err := wh.eventReader.EventsSince(r.Context(), task, since, limit); err == nil {
				catchUp = dbEvents
			}
		}
		for _, e := range catchUp {
			seenIDs[e.ID] = struct{}{}
			wh.writeSSEEvent(w, flusher, e)
		}
	}

	// Send current snapshot AFTER catch-up so chronology is preserved:
	// history first, then current state, then live events.
	if wh.snapshotStore != nil {
		if snap := wh.snapshotStore.Get(task); snap != nil {
			snapData, _ := json.Marshal(snap)
			fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", snapData)
			flusher.Flush()
		}
	}

	for {
		select {
		case e := <-ch:
			// Deduplicate events that arrived between subscribe and catch-up.
			if _, dup := seenIDs[e.ID]; dup {
				continue
			}
			// Stop tracking after initial dedup window.
			if len(seenIDs) > 0 {
				seenIDs = nil
			}
			wh.writeSSEEvent(w, flusher, e)
		case <-r.Context().Done():
			return
		}
	}
}

// writeSSEEvent writes a single event in SSE format and flushes.
func (wh *watchHandler) writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, e events.Event) {
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "id: %s\n", e.ID)
	fmt.Fprintf(w, "event: %s\n", e.Action)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}
