package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/darkquasar/fracta/internal/host"
)

// Verify AppServerSession implements host.StreamSession at compile time.
var _ host.StreamSession = (*AppServerSession)(nil)

// Verify AppServerSession implements host.LineObservable at compile time.
var _ host.LineObservable = (*AppServerSession)(nil)

// Verify AppServerSession implements host.TurnSteerer at compile time.
var _ host.TurnSteerer = (*AppServerSession)(nil)

// --- JSON-RPC types for the codex app-server protocol ---

// jsonRPCRequest is a JSON-RPC 2.0 request sent to the app-server via stdin.
type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// jsonRPCNotification is a JSON-RPC 2.0 notification received from the app-server
// via stdout. Notifications have a method but no id.
type jsonRPCNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// threadStartedParams is the params for a thread/started notification.
type threadStartedParams struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

// agentMessageDeltaParams is the params for an item/agentMessage/delta notification.
type agentMessageDeltaParams struct {
	ThreadID string `json:"threadId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
}

// errorParams is the params for an error notification.
type errorParams struct {
	ThreadID string `json:"threadId"`
	Error    struct {
		Message           string `json:"message"`
		AdditionalDetails string `json:"additionalDetails"`
	} `json:"error"`
	WillRetry bool `json:"willRetry"`
}

// --- AppServerSession: implements host.StreamSession ---
//
// Architecture follows Claude's StreamHandle pattern:
//   - A dedicated reader goroutine owns all stdout reads
//   - cmd.Wait() is only called after all pipe reads complete (os/exec contract)
//   - Done() closes on process exit (reader goroutine signals)
//
// The reader goroutine reads newline-delimited JSON-RPC notifications from stdout
// and pushes them into a channel. Send() consumes from that channel.

// AppServerSession wraps a long-lived codex app-server process using the
// JSON-RPC protocol over stdio.
type AppServerSession struct {
	cmd           *exec.Cmd
	stdin         *appServerWriter
	notifications chan jsonRPCNotification // buffered channel from reader goroutine
	mu            sync.Mutex               // serializes Send calls
	writeMu       sync.Mutex               // serializes stdin writes (allows Steer during Send)
	done          chan struct{}            // closed when process exits and Wait() completes
	err           error                    // exit error from cmd.Wait()
	requestID     atomic.Int64             // monotonic JSON-RPC request ID counter
	turnActive    atomic.Bool              // true while a turn is executing (between turn/start and turn/completed)
	threadID      string                   // from thread/started notification
	output        *host.ByteBuffer
	logPath       string
	logFn         func(path, content string) error
	lineObserver  func([]byte) // optional external line observer (Spec-35 seam)
}

// appServerWriter wraps stdin for the app-server process.
type appServerWriter struct {
	wc interface {
		Write([]byte) (int, error)
		Close() error
	}
}

func (w *appServerWriter) Write(p []byte) (int, error) { return w.wc.Write(p) }
func (w *appServerWriter) Close() error                { return w.wc.Close() }

// NewAppServerSession launches a codex app-server subprocess and performs the
// thread/start bootstrap handshake. Returns a ready-to-use StreamSession.
//
// The bootstrap sequence:
//  1. Launch `codex app-server` with stdio transport
//  2. Send thread/start JSON-RPC request
//  3. Wait for thread/started notification to extract threadId
func NewAppServerSession(workdir, model, logPath string) (host.StreamSession, error) {
	args := []string{"app-server"}

	cmd := exec.Command("codex", args...)
	cmd.Dir = workdir

	// Pass model via environment if specified.
	if model != "" {
		cmd.Env = append(os.Environ(), "CODEX_MODEL="+model)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting codex app-server: %w", err)
	}

	s := &AppServerSession{
		cmd:           cmd,
		stdin:         &appServerWriter{stdin},
		notifications: make(chan jsonRPCNotification, 256),
		done:          make(chan struct{}),
		output:        host.NewByteBuffer(host.DefaultBufferCap),
		logPath:       logPath,
		logFn:         appendToLogFile,
	}

	// Reader goroutine: owns all stdout reads. When stdout hits EOF
	// (process exited), it closes the notifications channel, calls cmd.Wait()
	// (safe — all reads are done), and closes done.
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			// Log raw line if configured.
			if s.logPath != "" && s.logFn != nil {
				_ = s.logFn(s.logPath, line+"\n")
			}

			// Notify external observer (Spec-35 seam).
			if s.lineObserver != nil {
				s.lineObserver([]byte(line))
			}

			var notif jsonRPCNotification
			if err := json.Unmarshal([]byte(line), &notif); err != nil {
				continue // skip non-JSON lines
			}
			if notif.Method != "" {
				s.notifications <- notif
			}
		}
		close(s.notifications)
		s.err = cmd.Wait() // safe: all pipe reads are complete
		close(s.done)
	}()

	// Bootstrap: send thread/start request and wait for thread/started.
	if err := s.bootstrapThread(); err != nil {
		// Clean up on bootstrap failure.
		s.stdin.Close()
		<-s.done
		return nil, fmt.Errorf("thread bootstrap: %w", err)
	}

	return s, nil
}

// bootstrapThread sends thread/start and waits for thread/started notification.
//
// Returns "app-server exited before thread/started" deterministically if the
// subprocess dies before bootstrap completes — regardless of whether the death
// happens before or after the stdin write. The race is resolved by checking
// s.done when the write fails: a write failure on a dead subprocess is the
// same condition as a clean exit before thread/started, and the caller
// shouldn't have to distinguish them.
func (s *AppServerSession) bootstrapThread() error {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      s.nextRequestID(),
		Method:  "thread/start",
		Params:  map[string]interface{}{},
	}

	if err := s.writeRequest(req); err != nil {
		// If the subprocess has already exited or is exiting, surface the
		// canonical "exited before thread/started" error so callers and tests
		// don't need to distinguish between two timing-equivalent failure
		// modes. We wait briefly for s.done because the reader goroutine
		// closes it via cmd.Wait(), which can lag the write failure by a
		// few milliseconds even when the subprocess has already died.
		select {
		case <-s.done:
			return fmt.Errorf("app-server exited before thread/started")
		case <-time.After(500 * time.Millisecond):
			return fmt.Errorf("sending thread/start: %w", err)
		}
	}

	// Wait for thread/started notification.
	for notif := range s.notifications {
		if notif.Method == "thread/started" {
			var params threadStartedParams
			if err := json.Unmarshal(notif.Params, &params); err != nil {
				return fmt.Errorf("parsing thread/started params: %w", err)
			}
			s.threadID = params.Thread.ID
			return nil
		}
		// Consume non-thread/started notifications during bootstrap (e.g., initialize responses).
	}

	return fmt.Errorf("app-server exited before thread/started")
}

// Send writes a turn/start JSON-RPC request and blocks until turn/completed.
// Extracts text from item/agentMessage/delta events. Returns the accumulated
// response as a Result.
func (s *AppServerSession) Send(message string) (host.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	select {
	case <-s.done:
		return host.Result{}, fmt.Errorf("app-server process has exited: %v", s.err)
	default:
	}

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      s.nextRequestID(),
		Method:  "turn/start",
		Params: map[string]interface{}{
			"threadId": s.threadID,
			"input":    []map[string]string{{"type": "text", "text": message}},
			// Fixed autonomous mode — same as --full-auto in batch (task A4).
			"sandboxPolicy":  map[string]interface{}{"type": "workspaceWrite"},
			"approvalPolicy": "never",
		},
	}

	if err := s.writeRequest(req); err != nil {
		return host.Result{}, fmt.Errorf("writing turn/start: %w", err)
	}

	s.turnActive.Store(true)
	defer s.turnActive.Store(false)

	// Accumulate text from streaming deltas until turn/completed.
	var accumulated string
	var isError bool

	for notif := range s.notifications {
		switch notif.Method {
		case "item/agentMessage/delta":
			var params agentMessageDeltaParams
			if err := json.Unmarshal(notif.Params, &params); err == nil {
				accumulated += params.Delta
				s.output.Write(params.Delta)
			}

		case "turn/completed":
			return host.Result{
				ResumeToken: s.threadID,
				Output:      accumulated,
				IsError:     isError,
			}, nil

		case "error":
			var params errorParams
			if err := json.Unmarshal(notif.Params, &params); err == nil {
				if !params.WillRetry {
					isError = true
					if accumulated == "" {
						accumulated = params.Error.Message
					}
				}
				// If willRetry, keep reading — the turn will continue.
			}

		case "thread/started":
			// May occur if thread was recreated; update threadID.
			var params threadStartedParams
			if err := json.Unmarshal(notif.Params, &params); err == nil {
				s.threadID = params.Thread.ID
			}

		default:
			// Other notifications (item/started, item/completed, command deltas, etc.)
			// are consumed but not processed for the Result. The lineObserver
			// already forwarded them for Spec-35 observability.
		}
	}

	// Channel closed — process exited mid-turn.
	return host.Result{}, fmt.Errorf("app-server exited unexpectedly during turn: %v", s.err)
}

// SetLineObserver registers a callback invoked for each raw protocol line.
// Must be called before the first Send(). Implements host.LineObservable.
func (s *AppServerSession) SetLineObserver(fn func([]byte)) {
	s.lineObserver = fn
}

// ResumeToken returns the threadId from the thread/started notification.
func (s *AppServerSession) ResumeToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.threadID
}

// RecentOutput returns the last maxBytes of semantic output.
func (s *AppServerSession) RecentOutput(maxBytes int) string {
	return s.output.Tail(maxBytes)
}

// Close sends turn/interrupt, closes stdin, and waits for the process to exit.
func (s *AppServerSession) Close() error {
	// Best-effort: send turn/interrupt to gracefully cancel any running turn.
	interruptReq := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      s.nextRequestID(),
		Method:  "turn/interrupt",
		Params: map[string]interface{}{
			"threadId": s.threadID,
		},
	}
	_ = s.writeRequest(interruptReq)

	s.stdin.Close()
	<-s.done // reader goroutine handles Wait() after EOF
	return s.err
}

// Done returns a channel that closes when the app-server process exits.
func (s *AppServerSession) Done() <-chan struct{} {
	return s.done
}

// writeRequest marshals and writes a JSON-RPC request to stdin with a newline delimiter.
// Protected by writeMu so Steer() can safely write concurrently with Send().
func (s *AppServerSession) writeRequest(req jsonRPCRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}
	data = append(data, '\n')

	s.writeMu.Lock()
	_, writeErr := s.stdin.Write(data)
	s.writeMu.Unlock()

	if writeErr != nil {
		return fmt.Errorf("writing to stdin: %w", writeErr)
	}
	return nil
}

// Steer sends a turn/steer JSON-RPC request to redirect the active turn.
// Returns an error if no turn is currently active.
func (s *AppServerSession) Steer(newDirection string) error {
	if !s.turnActive.Load() {
		return fmt.Errorf("no active turn")
	}

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      s.nextRequestID(),
		Method:  "turn/steer",
		Params: map[string]interface{}{
			"direction": newDirection,
		},
	}

	return s.writeRequest(req)
}

// nextRequestID returns the next monotonic JSON-RPC request ID.
func (s *AppServerSession) nextRequestID() int64 {
	return s.requestID.Add(1)
}

// appendToLogFile writes content to a log file, creating parent dirs if needed.
func appendToLogFile(path, content string) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(content)
	f.Close()
	return writeErr
}

// --- K8s WebSocket Transport (A7: host-side only) ---
//
// For K8s long-lived pods, codex app-server can listen on WebSocket instead of
// stdio. The host launches:
//
//   codex app-server --listen ws://0.0.0.0:<port> --ws-auth capability-token
//
// The fracta orchestrator connects to the pod's WebSocket endpoint using the
// same JSON-RPC protocol. The wire format is identical — only the transport
// layer changes (WebSocket frames instead of newline-delimited stdio lines).
//
// K8s Pod Spec for Codex app-server streaming:
//
//   containers:
//   - name: codex
//     image: openai/codex:<version>    # Single Rust binary (~50MB)
//     command: ["codex", "app-server"]
//     args:
//       - "--listen"
//       - "ws://0.0.0.0:8080"
//       - "--ws-auth"
//       - "capability-token"
//     env:
//       - name: OPENAI_API_KEY
//         valueFrom:
//           secretKeyRef:
//             name: codex-secrets
//             key: openai-api-key
//       - name: FRACTA_GATEWAY_TOKEN
//         valueFrom:
//           secretKeyRef:
//             name: fracta-secrets
//             key: gateway-token
//     ports:
//       - containerPort: 8080
//         name: ws-rpc
//     readinessProbe:
//       tcpSocket:
//         port: 8080
//       initialDelaySeconds: 5
//       periodSeconds: 10
//     resources:
//       requests:
//         memory: "256Mi"
//         cpu: "100m"
//       limits:
//         memory: "1Gi"
//
// Notes:
// - No liveness HTTP probe — app-server has no health endpoint. Use TCP socket.
// - --ws-auth capability-token requires a token file or env var for authentication.
// - --ephemeral and --skip-git-repo-check are NOT needed for app-server mode
//   (those are codex exec flags).
// - The pod runs as a long-lived service, not a batch job.
// - The orchestrator→backend wiring for K8s stream pod dispatch is a separate
//   concern (Phase 3). This code provides the host-side transport only.

// WebSocketConfig holds configuration for connecting to a codex app-server
// via WebSocket instead of stdio. Used for K8s long-lived pod deployments.
type WebSocketConfig struct {
	// URL is the WebSocket endpoint, e.g., "ws://pod-ip:8080".
	URL string

	// AuthToken is the capability token for WebSocket authentication.
	// Passed via --ws-auth capability-token on the server side.
	AuthToken string
}

// wsAppServerSession wraps a WebSocket connection to a remote codex app-server.
// It implements the same host.StreamSession interface as AppServerSession but
// communicates over WebSocket instead of stdio. The JSON-RPC protocol is identical.
type wsAppServerSession struct {
	conn          *websocket.Conn
	ctx           context.Context
	cancel        context.CancelFunc
	notifications chan jsonRPCNotification
	mu            sync.Mutex
	done          chan struct{}
	err           error
	requestID     atomic.Int64
	threadID      string
	output        *host.ByteBuffer
	logPath       string
	logFn         func(path, content string) error
	lineObserver  func([]byte)
}

// Compile-time checks for wsAppServerSession.
var _ host.StreamSession = (*wsAppServerSession)(nil)
var _ host.LineObservable = (*wsAppServerSession)(nil)

// NewWebSocketAppServerSession creates a StreamSession connected to a remote
// codex app-server via WebSocket. This is the K8s alternative to
// NewAppServerSession (which uses stdio). The JSON-RPC protocol is identical —
// only the transport changes.
func NewWebSocketAppServerSession(cfg WebSocketConfig, logPath string) (host.StreamSession, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Build dial options with auth token header if provided.
	dialOpts := &websocket.DialOptions{}
	if cfg.AuthToken != "" {
		dialOpts.HTTPHeader = http.Header{
			"Authorization": []string{"Bearer " + cfg.AuthToken},
		}
	}

	conn, _, err := websocket.Dial(ctx, cfg.URL, dialOpts)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("dialing codex app-server WebSocket at %s: %w", cfg.URL, err)
	}

	// Set a generous read limit for large JSON-RPC messages.
	conn.SetReadLimit(10 * 1024 * 1024) // 10MB

	s := &wsAppServerSession{
		conn:          conn,
		ctx:           ctx,
		cancel:        cancel,
		notifications: make(chan jsonRPCNotification, 256),
		done:          make(chan struct{}),
		output:        host.NewByteBuffer(host.DefaultBufferCap),
		logPath:       logPath,
		logFn:         appendToLogFile,
	}

	// Reader goroutine: reads WebSocket text messages (each = one JSON-RPC notification).
	go func() {
		defer close(s.notifications)
		defer func() {
			s.err = s.conn.Close(websocket.StatusNormalClosure, "")
			close(s.done)
		}()

		for {
			_, message, err := s.conn.Read(s.ctx)
			if err != nil {
				s.err = err
				return
			}

			line := string(message)
			if line == "" {
				continue
			}

			if s.logPath != "" && s.logFn != nil {
				_ = s.logFn(s.logPath, line+"\n")
			}

			if s.lineObserver != nil {
				s.lineObserver(message)
			}

			var notif jsonRPCNotification
			if err := json.Unmarshal(message, &notif); err != nil {
				continue
			}
			if notif.Method != "" {
				s.notifications <- notif
			}
		}
	}()

	// Bootstrap: thread/start handshake.
	if err := s.bootstrapThread(); err != nil {
		s.cancel()
		<-s.done
		return nil, fmt.Errorf("thread bootstrap: %w", err)
	}

	return s, nil
}

func (s *wsAppServerSession) bootstrapThread() error {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      s.requestID.Add(1),
		Method:  "thread/start",
		Params:  map[string]interface{}{},
	}

	if err := s.writeRequest(req); err != nil {
		return fmt.Errorf("sending thread/start: %w", err)
	}

	for notif := range s.notifications {
		if notif.Method == "thread/started" {
			var params threadStartedParams
			if err := json.Unmarshal(notif.Params, &params); err != nil {
				return fmt.Errorf("parsing thread/started params: %w", err)
			}
			s.threadID = params.Thread.ID
			return nil
		}
	}

	return fmt.Errorf("app-server WebSocket closed before thread/started")
}

func (s *wsAppServerSession) Send(message string) (host.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	select {
	case <-s.done:
		return host.Result{}, fmt.Errorf("app-server WebSocket has closed: %v", s.err)
	default:
	}

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      s.requestID.Add(1),
		Method:  "turn/start",
		Params: map[string]interface{}{
			"threadId":       s.threadID,
			"input":          []map[string]string{{"type": "text", "text": message}},
			"sandboxPolicy":  map[string]interface{}{"type": "workspaceWrite"},
			"approvalPolicy": "never",
		},
	}

	if err := s.writeRequest(req); err != nil {
		return host.Result{}, fmt.Errorf("writing turn/start: %w", err)
	}

	var accumulated string
	var isError bool

	for notif := range s.notifications {
		switch notif.Method {
		case "item/agentMessage/delta":
			var params agentMessageDeltaParams
			if err := json.Unmarshal(notif.Params, &params); err == nil {
				accumulated += params.Delta
				s.output.Write(params.Delta)
			}

		case "turn/completed":
			return host.Result{
				ResumeToken: s.threadID,
				Output:      accumulated,
				IsError:     isError,
			}, nil

		case "error":
			var params errorParams
			if err := json.Unmarshal(notif.Params, &params); err == nil {
				if !params.WillRetry {
					isError = true
					if accumulated == "" {
						accumulated = params.Error.Message
					}
				}
			}

		case "thread/started":
			var params threadStartedParams
			if err := json.Unmarshal(notif.Params, &params); err == nil {
				s.threadID = params.Thread.ID
			}
		}
	}

	return host.Result{}, fmt.Errorf("app-server WebSocket closed during turn: %v", s.err)
}

func (s *wsAppServerSession) SetLineObserver(fn func([]byte)) {
	s.lineObserver = fn
}

func (s *wsAppServerSession) ResumeToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.threadID
}

func (s *wsAppServerSession) RecentOutput(maxBytes int) string {
	return s.output.Tail(maxBytes)
}

func (s *wsAppServerSession) Close() error {
	// Best-effort: send turn/interrupt.
	interruptReq := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      s.requestID.Add(1),
		Method:  "turn/interrupt",
		Params:  map[string]interface{}{"threadId": s.threadID},
	}
	_ = s.writeRequest(interruptReq)

	s.cancel()
	<-s.done
	return s.err
}

func (s *wsAppServerSession) Done() <-chan struct{} {
	return s.done
}

func (s *wsAppServerSession) writeRequest(req jsonRPCRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}
	return s.conn.Write(s.ctx, websocket.MessageText, data)
}
