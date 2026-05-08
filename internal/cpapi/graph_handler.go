package cpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/darkquasar/fracta/internal/graph"
)

// writeGraphError classifies graph operation errors: ValidationError → 400, else → 500.
func writeGraphError(w http.ResponseWriter, err error) {
	var ve *graph.ValidationError
	if errors.As(err, &ve) {
		writeError(w, http.StatusBadRequest, "%v", err)
		return
	}
	writeError(w, http.StatusInternalServerError, "%v", err)
}

// handleGraphQuery handles POST /api/v1/graph/query.
func (h *handler) handleGraphQuery(w http.ResponseWriter, r *http.Request) {
	var req GraphQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: %v", err)
		return
	}
	if req.Cypher == "" {
		writeError(w, http.StatusBadRequest, "missing required field: cypher")
		return
	}

	resp, err := h.client.GraphQuery(r.Context(), req)
	if err != nil {
		writeGraphError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleGraphUpdate handles POST /api/v1/graph/update.
func (h *handler) handleGraphUpdate(w http.ResponseWriter, r *http.Request) {
	var req GraphUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: %v", err)
		return
	}
	if req.Cypher == "" {
		writeError(w, http.StatusBadRequest, "missing required field: cypher")
		return
	}

	resp, err := h.client.GraphUpdate(r.Context(), req)
	if err != nil {
		writeGraphError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleGraphSchema handles POST /api/v1/graph/schema.
// Tolerates empty/EOF body following the handleDryRunSpawn precedent.
func (h *handler) handleGraphSchema(w http.ResponseWriter, r *http.Request) {
	var req GraphSchemaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid request body: %v", err)
		return
	}

	resp, err := h.client.GraphSchema(r.Context(), req)
	if err != nil {
		writeGraphError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleGraphPath handles POST /api/v1/graph/path.
func (h *handler) handleGraphPath(w http.ResponseWriter, r *http.Request) {
	var req GraphPathRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: %v", err)
		return
	}

	resp, err := h.client.GraphPath(r.Context(), req)
	if err != nil {
		writeGraphError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleGraphNeighbors handles POST /api/v1/graph/neighbors.
func (h *handler) handleGraphNeighbors(w http.ResponseWriter, r *http.Request) {
	var req GraphNeighborsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: %v", err)
		return
	}

	resp, err := h.client.GraphNeighbors(r.Context(), req)
	if err != nil {
		writeGraphError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
