package cpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/darkquasar/fracta/internal/auth/credentials"
)

const defaultStageTTL = 300 * time.Second

// stagingHandler wraps a CredentialStager with HTTP handlers.
type stagingHandler struct {
	stager credentials.CredentialStager
}

func (h *stagingHandler) handleStageCredential(w http.ResponseWriter, r *http.Request) {
	var req StageCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: %v", err)
		return
	}

	if req.SourceName == "" {
		writeError(w, http.StatusBadRequest, "missing required field: source_name")
		return
	}
	if req.Data == "" {
		writeError(w, http.StatusBadRequest, "missing required field: data")
		return
	}

	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "data is not valid base64: %v", err)
		return
	}

	ttl := defaultStageTTL
	if req.TTLSeconds > 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}

	ref, err := h.stager.Stage(r.Context(), req.SourceName, data, req.MountPath, ttl)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "staging failed: %v", err)
		return
	}

	writeJSON(w, http.StatusCreated, StageCredentialResponse{Ref: ref})
}

func (h *stagingHandler) handleFetchStagedCredential(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	if ref == "" {
		writeError(w, http.StatusBadRequest, "missing credential ref")
		return
	}

	staged, err := h.stager.Fetch(r.Context(), ref)
	if err != nil {
		writeError(w, http.StatusNotFound, "staged credential not found: %v", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"source_name": staged.SourceName,
		"data":        base64.StdEncoding.EncodeToString(staged.Data),
		"mount_path":  staged.MountPath,
	})
}
