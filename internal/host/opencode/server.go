package opencode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/google/uuid"
)

// K8s Serve-Mode Pod Spec
//
// OpenCode serve mode runs as a long-lived pod with HTTP API + SSE events.
// Recommended pod spec:
//
//	containers:
//	- name: opencode
//	  image: opencode:1.14.18
//	  command: ["opencode", "serve", "--port", "4096", "--hostname", "0.0.0.0"]
//	  ports:
//	  - containerPort: 4096
//	  env:
//	  - name: OPENCODE_CONFIG_CONTENT
//	    valueFrom:
//	      configMapKeyRef: {name: opencode-config, key: config.json}
//	  - name: OPENCODE_DB
//	    value: /data/opencode.db
//	  - name: OPENCODE_SERVER_PASSWORD
//	    valueFrom:
//	      secretKeyRef: {name: opencode-secrets, key: server-password}
//	  - name: AWS_BEARER_TOKEN_BEDROCK
//	    valueFrom:
//	      secretKeyRef: {name: bedrock-token, key: token}
//	  - name: AWS_REGION
//	    value: ap-southeast-2
//	  livenessProbe:
//	    httpGet: {path: /global/health, port: 4096}
//	    initialDelaySeconds: 5
//	    periodSeconds: 10
//	  readinessProbe:
//	    httpGet: {path: /global/health, port: 4096}
//	    initialDelaySeconds: 3
//	    periodSeconds: 5
//	  volumeMounts:
//	  - name: data
//	    mountPath: /data
//	  volumes:
//	  - name: data
//	    emptyDir: {}
//
// Image: self-contained Bun binary (~100MB arm64), no runtime dependencies.
// OPENCODE_CONFIG_CONTENT: full opencode.json as env var (from ConfigMap).
// OPENCODE_DB: SQLite path on emptyDir volume. Session state is ephemeral;
// fracta tracks ResumeToken (session ID) in its own state store.
// OPENCODE_SERVER_PASSWORD: basic auth password for HTTP API (from Secret).
// Auth: AWS_BEARER_TOKEN_BEDROCK + AWS_REGION for Bedrock via corporate proxy.

// Compile-time interface checks.
var _ host.StreamSession = (*ServeSession)(nil)
var _ host.EventObservable = (*ServeSession)(nil)

const (
	healthTimeout   = 30 * time.Second
	healthPollDelay = 200 * time.Millisecond
	httpTimeout     = 30 * time.Second
	sseBufferSize   = 256
)

// Option is a functional option for StartServeSession.
type Option func(*ServeSession)

// WithStepLimit sets the maximum number of step_start events per Send() call
// before the session is aborted. Default is 20.
func WithStepLimit(n int) Option {
	return func(s *ServeSession) {
		s.stepLimit = n
	}
}

// ServeSession implements host.StreamSession via opencode serve + HTTP API.
// It manages a long-lived opencode serve subprocess and communicates via REST
// + SSE for streaming events.
type ServeSession struct {
	cmd      *exec.Cmd
	port     int
	password string // basic auth password
	baseURL  string

	sessionID     string
	mu            sync.Mutex // serializes Send calls
	done      chan struct{}
	closeDone sync.Once
	err       error
	output    *host.ByteBuffer
	eventObserver func([]byte) // optional external event observer

	// SSE reader state
	sseEvents chan sseEvent // buffered channel from SSE reader goroutine
	sseCancel func()       // closes the SSE response body to stop reader

	// Step monitoring — guards against subagent overuse.
	stepLimit int // max step_start events per Send() call (default 20)
	stepCount int // current step_start count within the active Send() call

	healthTimeoutOverride time.Duration // 0 = use default healthTimeout
}

func (s *ServeSession) signalDone() {
	s.closeDone.Do(func() { close(s.done) })
}

// sseEvent is a parsed SSE event from the /global/event stream.
type sseEvent struct {
	Event string // SSE event type (e.g., "message")
	Data  string // raw JSON data payload
}

// sessionCreateResponse is the response from POST /session.
type sessionCreateResponse struct {
	ID string `json:"id"`
}

// sessionStatusPayload represents the session.status SSE event payload.
type sessionStatusPayload struct {
	Type   string `json:"type"` // "session.status"
	Info   *sessionStatusInfo `json:"info,omitempty"`
}

// sessionStatusInfo is the inner status object within session events.
type sessionStatusInfo struct {
	Session struct {
		ID string `json:"id"`
	} `json:"session"`
	Status struct {
		Type string `json:"type"` // "idle", "busy"
	} `json:"status"`
}

// messagePartPayload represents a message.part.updated SSE event.
type messagePartPayload struct {
	Type string              `json:"type"` // "message.part.updated"
	Info *messagePartInfo    `json:"info,omitempty"`
}

type messagePartInfo struct {
	Part messagePart `json:"part"`
}

type messagePart struct {
	Type string `json:"type"` // "text", "tool"
	Text string `json:"text,omitempty"`
}

// StartServeSession launches an opencode serve subprocess and returns a
// ServeSession ready for Send() calls. This is the OpenCode implementation of
// host.Host.StartStream.
func StartServeSession(workdir, model, logPath string, permissionRules []PermissionRule, opts ...Option) (*ServeSession, error) {
	log := fractalog.Component("opencode")

	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("finding free port: %w", err)
	}

	password := uuid.NewString()

	args := []string{"serve", "--port", fmt.Sprintf("%d", port), "--hostname", "127.0.0.1"}
	cmd := exec.Command("opencode", args...)
	cmd.Dir = workdir
	cmd.Env = append(cmd.Environ(),
		fmt.Sprintf("OPENCODE_SERVER_PASSWORD=%s", password),
	)
	if model != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("OPENCODE_MODEL=%s", model))
	}

	// Discard stdout/stderr — all communication is via HTTP.
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting opencode serve: %w", err)
	}

	s := &ServeSession{
		cmd:       cmd,
		port:      port,
		password:  password,
		baseURL:   fmt.Sprintf("http://127.0.0.1:%d", port),
		done:      make(chan struct{}),
		output:    host.NewByteBuffer(host.DefaultBufferCap), // 32KB
		sseEvents: make(chan sseEvent, sseBufferSize),
		stepLimit: 20,
	}
	for _, opt := range opts {
		opt(s)
	}

	// Monitor process exit in background.
	go func() {
		s.err = cmd.Wait()
		s.signalDone()
	}()

	// Wait for health probe.
	if err := s.waitForHealth(); err != nil {
		s.Close()
		return nil, fmt.Errorf("opencode serve health probe failed: %w", err)
	}

	// Create session.
	sessionID, err := s.createSession(permissionRules)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("creating opencode session: %w", err)
	}
	s.sessionID = sessionID
	log.Info("opencode serve session created", "port", port, "session_id", sessionID)

	// Subscribe to SSE events.
	if err := s.subscribeSSE(); err != nil {
		s.Close()
		return nil, fmt.Errorf("subscribing to SSE events: %w", err)
	}

	return s, nil
}

// Send sends a prompt and blocks until the session becomes idle.
func (s *ServeSession) Send(message string) (host.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.stepCount = 0 // reset per-turn step counter

	select {
	case <-s.done:
		return host.Result{}, fmt.Errorf("opencode serve process has exited: %v", s.err)
	default:
	}

	// POST /session/:id/prompt_async
	body := map[string]string{"content": message}
	data, err := json.Marshal(body)
	if err != nil {
		return host.Result{}, fmt.Errorf("marshaling prompt: %w", err)
	}

	resp, err := s.doRequest("POST", fmt.Sprintf("/session/%s/prompt_async", s.sessionID), data)
	if err != nil {
		return host.Result{}, fmt.Errorf("sending prompt_async: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode >= 300 {
		return host.Result{}, fmt.Errorf("prompt_async returned status %d", resp.StatusCode)
	}

	// Two-phase wait: first wait for busy (prompt accepted), then wait for idle (turn complete).
	// This prevents stale idle events from a previous turn causing an early return.
	sawBusy := false
	var textParts []string
	for {
		select {
		case evt, ok := <-s.sseEvents:
			if !ok {
				// SSE stream closed — process likely exited.
				return host.Result{}, fmt.Errorf("SSE stream closed unexpectedly: %v", s.err)
			}

			// Notify external observer.
			if s.eventObserver != nil {
				s.eventObserver([]byte(evt.Data))
			}

			// Parse the SSE data to check for completion.
			var generic struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal([]byte(evt.Data), &generic); err != nil {
				continue
			}

			switch generic.Type {
			case "message.part.updated":
				var mp messagePartPayload
				if err := json.Unmarshal([]byte(evt.Data), &mp); err == nil && mp.Info != nil {
					if mp.Info.Part.Type == "text" && mp.Info.Part.Text != "" {
						textParts = append(textParts, mp.Info.Part.Text)
						s.output.Write(mp.Info.Part.Text)
					}
				}

			case "step_start":
				// Only count steps after the turn boundary (busy event) is established.
				// Pre-busy step_start events are either setup noise or stale events
				// from a previous aborted turn draining from the buffered channel.
				if sawBusy {
					log := fractalog.Component("opencode")
					s.stepCount++
					switch s.stepCount {
					case 5, 10, 15, 20:
						log.Warn("opencode step count milestone",
							"step_count", s.stepCount,
							"step_limit", s.stepLimit,
							"session_id", s.sessionID,
						)
					}
					if s.stepCount > s.stepLimit {
						log.Warn("opencode step limit exceeded, aborting session",
							"step_count", s.stepCount,
							"step_limit", s.stepLimit,
							"session_id", s.sessionID,
						)
						resp, abortErr := s.doRequest("POST", fmt.Sprintf("/session/%s/abort", s.sessionID), nil)
						if abortErr == nil {
							resp.Body.Close()
						}
						return host.Result{
							ResumeToken: s.sessionID,
							Output:      "step limit exceeded",
							IsError:     true,
						}, nil
					}
				}

			case "session.status":
				var ss sessionStatusPayload
				if err := json.Unmarshal([]byte(evt.Data), &ss); err == nil && ss.Info != nil {
					if ss.Info.Status.Type == "busy" {
						sawBusy = true
					} else if ss.Info.Status.Type == "idle" && sawBusy {
						// Only complete when we've seen busy→idle for THIS turn.
						output := strings.Join(textParts, "")
						return host.Result{
							ResumeToken: s.sessionID,
							Output:      output,
							IsError:     false,
						}, nil
					}
					// idle without prior busy = stale event from previous turn, skip.
				}

			case "session.error":
				var errPayload struct {
					Info struct {
						Error string `json:"error"`
					} `json:"info"`
				}
				if err := json.Unmarshal([]byte(evt.Data), &errPayload); err == nil {
					output := strings.Join(textParts, "")
					if output == "" {
						output = errPayload.Info.Error
					}
					return host.Result{
						ResumeToken: s.sessionID,
						Output:      output,
						IsError:     true,
					}, nil
				}
			}

		case <-s.done:
			return host.Result{}, fmt.Errorf("opencode serve process exited during Send: %v", s.err)
		}
	}
}

// SetEventObserver registers a callback invoked for each raw SSE event.
// Must be called before the first Send(). Implements host.EventObservable.
func (s *ServeSession) SetEventObserver(fn func([]byte)) {
	s.eventObserver = fn
}

// ResumeToken returns the session ID.
func (s *ServeSession) ResumeToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

// RecentOutput returns the last maxBytes of accumulated text output.
func (s *ServeSession) RecentOutput(maxBytes int) string {
	return s.output.Tail(maxBytes)
}

// Done returns a channel that closes when the serve process exits.
func (s *ServeSession) Done() <-chan struct{} {
	return s.done
}

// Close aborts the session and shuts down the subprocess.
func (s *ServeSession) Close() error {
	// Best-effort abort the session.
	if s.sessionID != "" {
		resp, err := s.doRequest("POST", fmt.Sprintf("/session/%s/abort", s.sessionID), nil)
		if err == nil {
			resp.Body.Close()
		}
	}

	// Stop SSE reader.
	if s.sseCancel != nil {
		s.sseCancel()
	}

	if s.cmd != nil && s.cmd.Process != nil {
		// Local subprocess — kill and wait for exit.
		s.cmd.Process.Kill()
		<-s.done
	} else {
		// Remote session — no subprocess to wait on; close done directly.
		s.signalDone()
	}

	return s.err
}

// --- Internal helpers ---

// waitForHealth polls GET /global/health until it returns 200 or timeout.
func (s *ServeSession) waitForHealth() error {
	timeout := healthTimeout
	if s.healthTimeoutOverride > 0 {
		timeout = s.healthTimeoutOverride
	}
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}

	for time.Now().Before(deadline) {
		select {
		case <-s.done:
			return fmt.Errorf("process exited before health probe succeeded: %v", s.err)
		default:
		}

		req, _ := http.NewRequest("GET", s.baseURL+"/global/health", nil)
		req.SetBasicAuth("opencode", s.password)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(healthPollDelay)
	}
	return fmt.Errorf("health probe timed out after %v", timeout)
}

// createSession creates a new session via POST /session.
func (s *ServeSession) createSession(permissionRules []PermissionRule) (string, error) {
	var body []byte
	if len(permissionRules) > 0 {
		payload := map[string]interface{}{
			"permission": permissionRules,
		}
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("marshaling session create: %w", err)
		}
	}

	resp, err := s.doRequest("POST", "/session", body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("POST /session returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result sessionCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding session create response: %w", err)
	}

	if result.ID == "" {
		return "", fmt.Errorf("session create returned empty ID")
	}

	return result.ID, nil
}

// subscribeSSE connects to GET /global/event and starts a goroutine that
// reads SSE events into the sseEvents channel.
func (s *ServeSession) subscribeSSE() error {
	req, err := http.NewRequest("GET", s.baseURL+"/global/event", nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth("opencode", s.password)
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 0} // no timeout for SSE
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("SSE subscribe: %w", err)
	}

	if resp.StatusCode != 200 {
		resp.Body.Close()
		return fmt.Errorf("SSE subscribe returned status %d", resp.StatusCode)
	}

	s.sseCancel = func() { resp.Body.Close() }

	// SSE reader goroutine.
	go func() {
		defer close(s.sseEvents)
		defer func() {
			// For remote sessions (no subprocess), Done() must fire when the SSE
			// stream dies — otherwise the orchestrator's exit watcher never triggers.
			if s.cmd == nil {
				s.signalDone()
			}
		}()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		var currentEvent sseEvent
		for scanner.Scan() {
			line := scanner.Text()

			if line == "" {
				// Empty line = end of event.
				if currentEvent.Data != "" {
					select {
					case s.sseEvents <- currentEvent:
					case <-s.done:
						return
					}
				}
				currentEvent = sseEvent{}
				continue
			}

			if strings.HasPrefix(line, "event:") {
				currentEvent.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			} else if strings.HasPrefix(line, "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if currentEvent.Data != "" {
					currentEvent.Data += "\n" + data
				} else {
					currentEvent.Data = data
				}
			}
			// Ignore other SSE fields (id:, retry:, comments).
		}
	}()

	return nil
}

// doRequest executes an HTTP request with basic auth to the serve instance.
func (s *ServeSession) doRequest(method, path string, body []byte) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = strings.NewReader(string(body))
	}

	req, err := http.NewRequest(method, s.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", s.password)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: httpTimeout}
	return client.Do(req)
}

// PermissionRule represents a session-level permission rule for opencode.
type PermissionRule struct {
	Permission string `json:"permission"`
	Action     string `json:"action"` // "allow", "ask", "deny"
	Pattern    string `json:"pattern"`
}

// NewRemoteServeSession creates a ServeSession connected to an already-running
// remote opencode serve instance (e.g., a K8s pod). Unlike StartServeSession,
// this does not launch a subprocess — it connects to the given base URL directly.
// The same HTTP+SSE protocol is used; only the transport endpoint differs.
func NewRemoteServeSession(baseURL, password string, permissionRules []PermissionRule) (*ServeSession, error) {
	log := fractalog.Component("opencode")

	s := &ServeSession{
		// No cmd — remote process managed externally (K8s pod).
		password:  password,
		baseURL:   baseURL,
		done:      make(chan struct{}),
		output:    host.NewByteBuffer(host.DefaultBufferCap),
		sseEvents: make(chan sseEvent, sseBufferSize),
		stepLimit: 20,
	}

	// Wait for health probe.
	if err := s.waitForHealth(); err != nil {
		return nil, fmt.Errorf("remote opencode serve health probe failed: %w", err)
	}

	// Create session.
	sessionID, err := s.createSession(permissionRules)
	if err != nil {
		return nil, fmt.Errorf("creating remote opencode session: %w", err)
	}
	s.sessionID = sessionID
	log.Info("remote opencode serve session created", "base_url", baseURL, "session_id", sessionID)

	// Subscribe to SSE events.
	if err := s.subscribeSSE(); err != nil {
		s.Close()
		return nil, fmt.Errorf("subscribing to remote SSE events: %w", err)
	}

	return s, nil
}

// freePort finds an available TCP port on localhost.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
