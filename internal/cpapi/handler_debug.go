package cpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/darkquasar/fracta/internal/fractalog"
)

// debugProxyHandler proxies operator debug requests from the CP API to the
// gateway pod. The CP API daemon is the only fracta surface a thin-client
// operator can reach; the gateway pod sits behind a cluster-internal Service
// that the operator's laptop has no DNS for. This handler fetches the
// gateway's /debug/policy on the operator's behalf.
//
// Each request hits the gateway fresh (no cache). On gateway timeout or
// connection failure, returns 502 Bad Gateway with the underlying error.
type debugProxyHandler struct {
	gatewayBaseURL string
	client         *http.Client
}

func newDebugProxyHandler(gatewayBaseURL string) *debugProxyHandler {
	return &debugProxyHandler{
		gatewayBaseURL: strings.TrimRight(gatewayBaseURL, "/"),
		client:         &http.Client{Timeout: 10 * time.Second},
	}
}

// handleGatewayPolicy proxies GET /api/v1/debug/gateway-policy →
// GET <gatewayBaseURL>/debug/policy, forwarding the ?verbose query if set.
func (h *debugProxyHandler) handleGatewayPolicy(w http.ResponseWriter, r *http.Request) {
	target, err := url.Parse(h.gatewayBaseURL + "/debug/policy")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid gateway URL %q: %v", h.gatewayBaseURL, err)
		return
	}
	if v := r.URL.Query().Get("verbose"); v != "" {
		q := target.Query()
		q.Set("verbose", v)
		target.RawQuery = q.Encode()
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "building gateway request: %v", err)
		return
	}

	resp, err := h.client.Do(req)
	if err != nil {
		fractalog.Component("cpapi").Warn("gateway debug proxy failed",
			"gateway", h.gatewayBaseURL, "error", err)
		writeError(w, http.StatusBadGateway,
			"gateway unreachable: %s: %v", h.gatewayBaseURL, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		writeError(w, http.StatusBadGateway,
			"gateway returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, resp.Body); err != nil {
		fractalog.Component("cpapi").Warn("gateway debug proxy copy failed", "error", err)
	}
}

// gatewayPolicyTargetURL returns the URL the proxy would call (for logging
// and tests).
func (h *debugProxyHandler) gatewayPolicyTargetURL() string {
	return fmt.Sprintf("%s/debug/policy", h.gatewayBaseURL)
}
