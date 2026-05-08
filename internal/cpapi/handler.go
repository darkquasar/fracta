package cpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// handler wraps a ControlPlaneClient with thin HTTP handlers.
// Each handler validates the request, calls the client method, and serializes the response.
type handler struct {
	client ControlPlaneClient
}

func (h *handler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *handler) handleSpawn(w http.ResponseWriter, r *http.Request) {
	var req SpawnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: %v", err)
		return
	}
	if req.Task == "" {
		writeError(w, http.StatusBadRequest, "missing required field: task")
		return
	}

	resp, err := h.client.Spawn(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *handler) handleListAgents(w http.ResponseWriter, r *http.Request) {
	resp, err := h.client.ListAgents(r.Context(), ListAgentsRequest{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing agent name")
		return
	}

	resp, err := h.client.GetAgent(r.Context(), GetAgentRequest{Name: name})
	if err != nil {
		writeError(w, http.StatusNotFound, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) handleGetMission(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing agent name")
		return
	}

	resp, err := h.client.GetMission(r.Context(), GetMissionRequest{Name: name})
	if err != nil {
		writeError(w, http.StatusNotFound, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) handlePeek(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing agent name")
		return
	}
	mode := r.URL.Query().Get("mode")

	resp, err := h.client.Peek(r.Context(), PeekRequest{Name: name, Mode: mode})
	if err != nil {
		writeError(w, http.StatusNotFound, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing agent name")
		return
	}

	var req GetLogsRequest
	req.Task = name
	// Lines from query param, default handled by client.
	if linesStr := r.URL.Query().Get("lines"); linesStr != "" {
		var lines int
		if _, err := fmt.Sscanf(linesStr, "%d", &lines); err == nil {
			req.Lines = lines
		}
	}

	resp, err := h.client.GetLogs(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) handleSay(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing agent name")
		return
	}

	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: %v", err)
		return
	}
	if body.Message == "" {
		writeError(w, http.StatusBadRequest, "missing required field: message")
		return
	}

	resp, err := h.client.Say(r.Context(), SayRequest{Name: name, Message: body.Message})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) handleKill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing agent name")
		return
	}

	var body struct {
		KeepFiles bool `json:"keep_files"`
	}
	// Body is optional for kill.
	json.NewDecoder(r.Body).Decode(&body)

	resp, err := h.client.Kill(r.Context(), KillRequest{Name: name, KeepFiles: body.KeepFiles})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) handleMerge(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing agent name")
		return
	}

	resp, err := h.client.Merge(r.Context(), MergeRequest{Name: name})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) handleDryRunSpawn(w http.ResponseWriter, r *http.Request) {
	var req DryRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Body is optional for dry-run (all fields have defaults).
		req = DryRunRequest{}
	}

	resp, err := h.client.DryRunSpawn(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) handleCreateObjective(w http.ResponseWriter, r *http.Request) {
	var req CreateObjectiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: %v", err)
		return
	}
	if req.Description == "" {
		writeError(w, http.StatusBadRequest, "missing required field: description")
		return
	}

	resp, err := h.client.CreateObjective(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *handler) handleListObjectives(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	resp, err := h.client.ListObjectives(r.Context(), ListObjectivesRequest{Status: status})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) handleGetObjective(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing objective id")
		return
	}

	resp, err := h.client.GetObjective(r.Context(), GetObjectiveRequest{ID: id})
	if err != nil {
		writeError(w, http.StatusNotFound, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) handleUnfreezeObjective(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing objective id")
		return
	}

	resp, err := h.client.UnfreezeObjective(r.Context(), UnfreezeObjectiveRequest{ID: id})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleIngestEvents accepts a batch of events from workers (K8s remote mode).
// POST /api/v1/agents/{name}/events
func (h *handler) handleIngestEvents(w http.ResponseWriter, r *http.Request) {
	task := r.PathValue("name")
	if task == "" {
		writeError(w, http.StatusBadRequest, "missing agent name")
		return
	}

	var req IngestEventsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: %v", err)
		return
	}
	resp, err := h.client.IngestEvents(r.Context(), req, task)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusAccepted, resp)
}

// handleQueryEvents returns recent events for an agent from the ring buffer.
// GET /api/v1/agents/{name}/events
func (h *handler) handleQueryEvents(w http.ResponseWriter, r *http.Request) {
	task := r.PathValue("name")
	if task == "" {
		writeError(w, http.StatusBadRequest, "missing agent name")
		return
	}

	var last int
	if lastStr := r.URL.Query().Get("last"); lastStr != "" {
		fmt.Sscanf(lastStr, "%d", &last)
	}
	since := r.URL.Query().Get("since")

	resp, err := h.client.QueryEvents(r.Context(), EventsQueryRequest{
		Task:  task,
		Last:  last,
		Since: since,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, format string, args ...interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"error": fmt.Sprintf(format, args...),
	})
}
