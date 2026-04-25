package opencode

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/host"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServeSession_InterfaceCompliance(t *testing.T) {
	var _ host.StreamSession = (*ServeSession)(nil)
	var _ host.EventObservable = (*ServeSession)(nil)
}

func TestServeSession_ResumeToken(t *testing.T) {
	s := &ServeSession{
		sessionID: "ses_abc123",
		done:      make(chan struct{}),
	}
	assert.Equal(t, "ses_abc123", s.ResumeToken())
}

func TestServeSession_RecentOutput(t *testing.T) {
	s := &ServeSession{
		output: host.NewByteBuffer(host.DefaultBufferCap),
		done:   make(chan struct{}),
	}
	s.output.Write("hello world")
	assert.Equal(t, "hello world", s.RecentOutput(100))
	assert.Equal(t, "world", s.RecentOutput(5))
}

func TestServeSession_Done(t *testing.T) {
	done := make(chan struct{})
	s := &ServeSession{done: done}

	select {
	case <-s.Done():
		t.Fatal("Done() should not be closed yet")
	default:
		// expected
	}

	close(done)

	select {
	case <-s.Done():
		// expected
	case <-time.After(time.Second):
		t.Fatal("Done() should be closed")
	}
}

func TestServeSession_Send(t *testing.T) {
	promptReceived := make(chan string, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/session/ses_test/prompt_async", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		promptReceived <- body["content"]
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	sseEvents := make(chan sseEvent, 10)
	s := &ServeSession{
		sessionID: "ses_test",
		baseURL:   ts.URL,
		password:  "test",
		done:      make(chan struct{}),
		output:    host.NewByteBuffer(host.DefaultBufferCap),
		sseEvents: sseEvents,
	}

	// Feed SSE events that simulate a turn.
	go func() {
		// Busy status
		sseEvents <- sseEvent{Data: `{"type":"session.status","info":{"session":{"id":"ses_test"},"status":{"type":"busy"}}}`}
		// Message part
		sseEvents <- sseEvent{Data: `{"type":"message.part.updated","info":{"part":{"type":"text","text":"Hello from OpenCode"}}}`}
		// Idle status (turn complete)
		sseEvents <- sseEvent{Data: `{"type":"session.status","info":{"session":{"id":"ses_test"},"status":{"type":"idle"}}}`}
	}()

	result, err := s.Send("What is 2+2?")
	require.NoError(t, err)

	assert.Equal(t, "ses_test", result.ResumeToken)
	assert.Equal(t, "Hello from OpenCode", result.Output)
	assert.False(t, result.IsError)

	select {
	case prompt := <-promptReceived:
		assert.Equal(t, "What is 2+2?", prompt)
	case <-time.After(time.Second):
		t.Fatal("prompt_async not called")
	}
}

func TestServeSession_Send_Error(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/session/ses_err/prompt_async", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	sseEvents := make(chan sseEvent, 10)
	s := &ServeSession{
		sessionID: "ses_err",
		baseURL:   ts.URL,
		password:  "test",
		done:      make(chan struct{}),
		output:    host.NewByteBuffer(host.DefaultBufferCap),
		sseEvents: sseEvents,
	}

	go func() {
		sseEvents <- sseEvent{Data: `{"type":"session.error","info":{"error":"model quota exceeded"}}`}
	}()

	result, err := s.Send("test")
	require.NoError(t, err) // Send returns Result with IsError, not an error
	assert.True(t, result.IsError)
	assert.Equal(t, "model quota exceeded", result.Output)
}

func TestServeSession_Send_ProcessExited(t *testing.T) {
	done := make(chan struct{})
	close(done) // process already exited

	s := &ServeSession{
		sessionID: "ses_dead",
		done:      done,
		err:       fmt.Errorf("exit status 1"),
	}

	_, err := s.Send("test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exited")
}

func TestServeSession_EventObservable(t *testing.T) {
	var observed []string
	var mu sync.Mutex

	mux := http.NewServeMux()
	mux.HandleFunc("/session/ses_obs/prompt_async", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	sseEvents := make(chan sseEvent, 10)
	s := &ServeSession{
		sessionID: "ses_obs",
		baseURL:   ts.URL,
		password:  "test",
		done:      make(chan struct{}),
		output:    host.NewByteBuffer(host.DefaultBufferCap),
		sseEvents: sseEvents,
	}

	s.SetEventObserver(func(event []byte) {
		mu.Lock()
		observed = append(observed, string(event))
		mu.Unlock()
	})

	go func() {
		sseEvents <- sseEvent{Data: `{"type":"session.status","info":{"session":{"id":"ses_obs"},"status":{"type":"busy"}}}`}
		sseEvents <- sseEvent{Data: `{"type":"message.part.updated","info":{"part":{"type":"text","text":"observed text"}}}`}
		sseEvents <- sseEvent{Data: `{"type":"session.status","info":{"session":{"id":"ses_obs"},"status":{"type":"idle"}}}`}
	}()

	_, err := s.Send("test")
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, observed, 3)
	assert.Contains(t, observed[0], "session.status")
	assert.Contains(t, observed[1], "message.part.updated")
	assert.Contains(t, observed[2], "session.status")
}

func TestServeSession_Send_MultipleTextParts(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/session/ses_multi/prompt_async", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	sseEvents := make(chan sseEvent, 10)
	s := &ServeSession{
		sessionID: "ses_multi",
		baseURL:   ts.URL,
		password:  "test",
		done:      make(chan struct{}),
		output:    host.NewByteBuffer(host.DefaultBufferCap),
		sseEvents: sseEvents,
	}

	go func() {
		sseEvents <- sseEvent{Data: `{"type":"session.status","info":{"session":{"id":"ses_multi"},"status":{"type":"busy"}}}`}
		sseEvents <- sseEvent{Data: `{"type":"message.part.updated","info":{"part":{"type":"text","text":"Part 1. "}}}`}
		sseEvents <- sseEvent{Data: `{"type":"message.part.updated","info":{"part":{"type":"text","text":"Part 2."}}}`}
		sseEvents <- sseEvent{Data: `{"type":"session.status","info":{"session":{"id":"ses_multi"},"status":{"type":"idle"}}}`}
	}()

	result, err := s.Send("test")
	require.NoError(t, err)
	assert.Equal(t, "Part 1. Part 2.", result.Output)
}

func TestServeSession_Send_IgnoresStaleIdle(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/session/ses_stale/prompt_async", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	sseEvents := make(chan sseEvent, 10)
	s := &ServeSession{
		sessionID: "ses_stale",
		baseURL:   ts.URL,
		password:  "test",
		done:      make(chan struct{}),
		output:    host.NewByteBuffer(host.DefaultBufferCap),
		sseEvents: sseEvents,
	}

	go func() {
		// Stale idle from previous turn — must be ignored.
		sseEvents <- sseEvent{Data: `{"type":"session.status","info":{"session":{"id":"ses_stale"},"status":{"type":"idle"}}}`}
		// Current turn: busy → text → idle.
		sseEvents <- sseEvent{Data: `{"type":"session.status","info":{"session":{"id":"ses_stale"},"status":{"type":"busy"}}}`}
		sseEvents <- sseEvent{Data: `{"type":"message.part.updated","info":{"part":{"type":"text","text":"current turn output"}}}`}
		sseEvents <- sseEvent{Data: `{"type":"session.status","info":{"session":{"id":"ses_stale"},"status":{"type":"idle"}}}`}
	}()

	result, err := s.Send("test")
	require.NoError(t, err)
	// Must see the current turn's output, not return empty from the stale idle.
	assert.Equal(t, "current turn output", result.Output)
	assert.False(t, result.IsError)
}

func TestServeSession_SSEParsing(t *testing.T) {
	sseEvents := make(chan sseEvent, 10)

	go func() {
		sseEvents <- sseEvent{
			Event: "message",
			Data:  `{"type":"session.status","info":{"session":{"id":"ses_sse"},"status":{"type":"idle"}}}`,
		}
	}()

	evt := <-sseEvents
	assert.Equal(t, "message", evt.Event)
	assert.Contains(t, evt.Data, "session.status")
}

func TestPermissionRule_JSON(t *testing.T) {
	rules := []PermissionRule{
		{Permission: "task", Action: "deny", Pattern: "*"},
		{Permission: "read", Action: "allow", Pattern: "*"},
	}

	data, err := json.Marshal(rules)
	require.NoError(t, err)

	var decoded []PermissionRule
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, rules, decoded)
}

func TestFreePort(t *testing.T) {
	port, err := freePort()
	require.NoError(t, err)
	assert.Greater(t, port, 0)
	assert.Less(t, port, 65536)
}

func TestServeSession_PromptAsyncResponseFormat(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/session/ses_fmt/prompt_async", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	sseEvents := make(chan sseEvent, 10)
	s := &ServeSession{
		sessionID: "ses_fmt",
		baseURL:   ts.URL,
		password:  "test",
		done:      make(chan struct{}),
		output:    host.NewByteBuffer(host.DefaultBufferCap),
		sseEvents: sseEvents,
	}

	go func() {
		sseEvents <- sseEvent{Data: `{"type":"session.status","info":{"session":{"id":"ses_fmt"},"status":{"type":"busy"}}}`}
		sseEvents <- sseEvent{Data: `{"type":"session.status","info":{"session":{"id":"ses_fmt"},"status":{"type":"idle"}}}`}
	}()

	result, err := s.Send("test")
	require.NoError(t, err)
	assert.Equal(t, "ses_fmt", result.ResumeToken)
	assert.False(t, result.IsError)
}

func TestServeSession_BasicAuth(t *testing.T) {
	var authHeader string
	mux := http.NewServeMux()
	mux.HandleFunc("/session/ses_auth/prompt_async", func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	sseEvents := make(chan sseEvent, 10)
	s := &ServeSession{
		sessionID: "ses_auth",
		baseURL:   ts.URL,
		password:  "my-secret-password",
		done:      make(chan struct{}),
		output:    host.NewByteBuffer(host.DefaultBufferCap),
		sseEvents: sseEvents,
	}

	go func() {
		sseEvents <- sseEvent{Data: `{"type":"session.status","info":{"session":{"id":"ses_auth"},"status":{"type":"busy"}}}`}
		sseEvents <- sseEvent{Data: `{"type":"session.status","info":{"session":{"id":"ses_auth"},"status":{"type":"idle"}}}`}
	}()

	_, err := s.Send("test")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(authHeader, "Basic "), "expected basic auth header")
}

func TestServeSession_Close_NoProcess(t *testing.T) {
	s := &ServeSession{
		sessionID: "ses_close",
		baseURL:   "http://localhost:0",
		password:  "test",
		done:      make(chan struct{}),
	}
	s.signalDone() // simulate already-closed done channel

	err := s.Close()
	_ = err // No panic is the assertion.
}

func TestServeSession_WaitForHealth(t *testing.T) {
	// Test that health probe retries until success.
	attempts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/global/health", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	s := &ServeSession{
		baseURL:  ts.URL,
		password: "test",
		done:     make(chan struct{}),
	}

	err := s.waitForHealth()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, attempts, 3)
}

func TestServeSession_CreateSession(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "ses_new123"})
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	s := &ServeSession{
		baseURL:  ts.URL,
		password: "test",
		done:     make(chan struct{}),
	}

	id, err := s.createSession(nil)
	require.NoError(t, err)
	assert.Equal(t, "ses_new123", id)
}

// --- Remote session tests ---

func TestRemoteServeSession_Send(t *testing.T) {
	mux := http.NewServeMux()

	// Health endpoint.
	mux.HandleFunc("/global/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Session create.
	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "remote-ses-1"})
	})

	// SSE stream.
	mux.HandleFunc("/global/event", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected ResponseWriter to be Flusher")
		}

		// Wait a bit for Send to call prompt_async, then send events.
		time.Sleep(50 * time.Millisecond)
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", `{"type":"session.status","info":{"session":{"id":"remote-ses-1"},"status":{"type":"busy"}}}`)
		flusher.Flush()
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", `{"type":"message.part.updated","info":{"part":{"type":"text","text":"Remote response"}}}`)
		flusher.Flush()
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", `{"type":"session.status","info":{"session":{"id":"remote-ses-1"},"status":{"type":"idle"}}}`)
		flusher.Flush()
		// Hold the SSE stream open — the session's done channel drives teardown.
		<-r.Context().Done()
	})

	// Prompt async.
	mux.HandleFunc("/session/remote-ses-1/prompt_async", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	session, err := NewRemoteServeSession(ts.URL, "test-password", nil)
	if err != nil {
		t.Fatalf("NewRemoteServeSession: %v", err)
	}
	defer session.Close()

	assert.Equal(t, "remote-ses-1", session.ResumeToken())

	result, err := session.Send("hello remote")
	require.NoError(t, err)
	assert.Equal(t, "Remote response", result.Output)
	assert.Equal(t, "remote-ses-1", result.ResumeToken)
	assert.False(t, result.IsError)
}

func TestRemoteServeSession_HealthProbeFailure(t *testing.T) {
	// Server that never returns 200 for health.
	mux := http.NewServeMux()
	mux.HandleFunc("/global/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	// Test directly with a short timeout to avoid slow test.
	s := &ServeSession{
		baseURL:               ts.URL,
		password:              "test",
		done:                  make(chan struct{}),
		healthTimeoutOverride: 1 * time.Second,
	}

	err := s.waitForHealth()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "health probe")
}

func TestRemoteServeSession_NoSubprocess(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/global/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "remote-ses-nosub"})
	})
	mux.HandleFunc("/session/remote-ses-nosub/abort", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/global/event", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		flusher.Flush()
		// Keep connection open until client disconnects.
		<-r.Context().Done()
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	session, err := NewRemoteServeSession(ts.URL, "test", nil)
	require.NoError(t, err)

	// Verify no subprocess is running.
	assert.Nil(t, session.cmd, "remote session should not have a subprocess")
	session.Close()
}

func TestServeSession_CreateSessionWithPermissions(t *testing.T) {
	var receivedBody map[string]interface{}
	mux := http.NewServeMux()
	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "ses_perm"})
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	s := &ServeSession{
		baseURL:  ts.URL,
		password: "test",
		done:     make(chan struct{}),
	}

	rules := []PermissionRule{
		{Permission: "task", Action: "deny", Pattern: "*"},
	}

	id, err := s.createSession(rules)
	require.NoError(t, err)
	assert.Equal(t, "ses_perm", id)
	assert.NotNil(t, receivedBody["permission"])
}

func TestServeSession_StepMonitoring_Warn(t *testing.T) {
	// Feed step_start events below the default limit (20).
	// Verify no abort and successful idle return.
	mux := http.NewServeMux()
	mux.HandleFunc("/session/ses_warn/prompt_async", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	sseEvents := make(chan sseEvent, 30)
	s := &ServeSession{
		sessionID: "ses_warn",
		baseURL:   ts.URL,
		password:  "test",
		done:      make(chan struct{}),
		output:    host.NewByteBuffer(host.DefaultBufferCap),
		sseEvents: sseEvents,
		stepLimit: 20,
	}

	go func() {
		// Real OpenCode order: busy → step_start × N → idle.
		sseEvents <- sseEvent{Data: `{"type":"session.status","info":{"session":{"id":"ses_warn"},"status":{"type":"busy"}}}`}
		for i := 0; i < 10; i++ {
			sseEvents <- sseEvent{Data: `{"type":"step_start"}`}
		}
		sseEvents <- sseEvent{Data: `{"type":"session.status","info":{"session":{"id":"ses_warn"},"status":{"type":"idle"}}}`}
	}()

	result, err := s.Send("test")
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, 10, s.stepCount)
}

func TestServeSession_StepMonitoring_Abort(t *testing.T) {
	// Feed >20 step_start events and verify abort is called with IsError result.
	var abortCalled bool
	var mu sync.Mutex

	mux := http.NewServeMux()
	mux.HandleFunc("/session/ses_abort/prompt_async", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/session/ses_abort/abort", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		abortCalled = true
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	sseEvents := make(chan sseEvent, 30)
	s := &ServeSession{
		sessionID: "ses_abort",
		baseURL:   ts.URL,
		password:  "test",
		done:      make(chan struct{}),
		output:    host.NewByteBuffer(host.DefaultBufferCap),
		sseEvents: sseEvents,
		stepLimit: 20,
	}

	go func() {
		// Real OpenCode order: busy first, then step_start events.
		sseEvents <- sseEvent{Data: `{"type":"session.status","info":{"session":{"id":"ses_abort"},"status":{"type":"busy"}}}`}
		for i := 0; i < 21; i++ {
			sseEvents <- sseEvent{Data: `{"type":"step_start"}`}
		}
	}()

	result, err := s.Send("test")
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Equal(t, "step limit exceeded", result.Output)
	assert.Equal(t, "ses_abort", result.ResumeToken)

	mu.Lock()
	assert.True(t, abortCalled, "abort endpoint should have been called")
	mu.Unlock()
}

func TestServeSession_StepMonitoring_ConfigurableThreshold(t *testing.T) {
	// WithStepLimit(3): feed 4 step_start events, verify abort at 4.
	var abortCalled bool
	var mu sync.Mutex

	mux := http.NewServeMux()
	mux.HandleFunc("/session/ses_custom/prompt_async", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/session/ses_custom/abort", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		abortCalled = true
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	sseEvents := make(chan sseEvent, 10)
	s := &ServeSession{
		sessionID: "ses_custom",
		baseURL:   ts.URL,
		password:  "test",
		done:      make(chan struct{}),
		output:    host.NewByteBuffer(host.DefaultBufferCap),
		sseEvents: sseEvents,
		stepLimit: 20, // default
	}
	// Apply WithStepLimit option.
	WithStepLimit(3)(s)

	go func() {
		// Real OpenCode order: busy first, then step_start events.
		sseEvents <- sseEvent{Data: `{"type":"session.status","info":{"session":{"id":"ses_custom"},"status":{"type":"busy"}}}`}
		for i := 0; i < 4; i++ {
			sseEvents <- sseEvent{Data: `{"type":"step_start"}`}
		}
	}()

	result, err := s.Send("test")
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Equal(t, "step limit exceeded", result.Output)
	assert.Equal(t, 4, s.stepCount)

	mu.Lock()
	assert.True(t, abortCalled, "abort endpoint should have been called at custom threshold")
	mu.Unlock()
}

func TestServeSession_StepMonitoring_BelowThreshold(t *testing.T) {
	// Feed 19 step_start events then idle — verify no abort and IsError=false.
	var abortCalled bool
	var mu sync.Mutex

	mux := http.NewServeMux()
	mux.HandleFunc("/session/ses_below/prompt_async", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/session/ses_below/abort", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		abortCalled = true
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	sseEvents := make(chan sseEvent, 30)
	s := &ServeSession{
		sessionID: "ses_below",
		baseURL:   ts.URL,
		password:  "test",
		done:      make(chan struct{}),
		output:    host.NewByteBuffer(host.DefaultBufferCap),
		sseEvents: sseEvents,
		stepLimit: 20,
	}

	go func() {
		// Real OpenCode order: busy → step_start × N → idle.
		sseEvents <- sseEvent{Data: `{"type":"session.status","info":{"session":{"id":"ses_below"},"status":{"type":"busy"}}}`}
		for i := 0; i < 19; i++ {
			sseEvents <- sseEvent{Data: `{"type":"step_start"}`}
		}
		sseEvents <- sseEvent{Data: `{"type":"session.status","info":{"session":{"id":"ses_below"},"status":{"type":"idle"}}}`}
	}()

	result, err := s.Send("test")
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, 19, s.stepCount)

	mu.Lock()
	assert.False(t, abortCalled, "abort should not have been called when below threshold")
	mu.Unlock()
}
