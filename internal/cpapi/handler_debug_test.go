package cpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDebugProxy_HappyPath(t *testing.T) {
	wantBody := `{"has_policies":true,"policies":[{"server":"elastic","deny":["esql"]}]}`
	var gotPath, gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, wantBody)
	}))
	defer upstream.Close()

	h := newDebugProxyHandler(upstream.URL)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/debug/gateway-policy?verbose=1", nil)
	w := httptest.NewRecorder()
	h.handleGatewayPolicy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := strings.TrimSpace(w.Body.String()); got != wantBody {
		t.Errorf("body = %q, want %q", got, wantBody)
	}
	if gotPath != "/debug/policy" {
		t.Errorf("upstream path = %q, want /debug/policy", gotPath)
	}
	if gotQuery != "verbose=1" {
		t.Errorf("upstream query = %q, want verbose=1", gotQuery)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
}

func TestDebugProxy_VerboseOmittedWhenNotSet(t *testing.T) {
	var gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	h := newDebugProxyHandler(upstream.URL)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/debug/gateway-policy", nil)
	w := httptest.NewRecorder()
	h.handleGatewayPolicy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if gotQuery != "" {
		t.Errorf("upstream query = %q, want empty (verbose not requested)", gotQuery)
	}
}

func TestDebugProxy_GatewayUnreachable(t *testing.T) {
	// 127.0.0.1:1 should always refuse connections.
	h := newDebugProxyHandler("http://127.0.0.1:1")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/debug/gateway-policy", nil)
	w := httptest.NewRecorder()
	h.handleGatewayPolicy(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "gateway unreachable") {
		t.Errorf("error message %q does not mention 'gateway unreachable'", msg)
	}
	if !strings.Contains(msg, "127.0.0.1:1") {
		t.Errorf("error message %q does not include target host", msg)
	}
}

func TestDebugProxy_GatewayReturnsNon200(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer upstream.Close()

	h := newDebugProxyHandler(upstream.URL)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/debug/gateway-policy", nil)
	w := httptest.NewRecorder()
	h.handleGatewayPolicy(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
	var body map[string]any
	_ = json.NewDecoder(w.Body).Decode(&body)
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "500") {
		t.Errorf("error %q should mention upstream status 500", msg)
	}
}
