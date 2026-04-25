package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/darkquasar/fracta/internal/host"
)

// Verify StreamHandle implements host.StreamSession at compile time.
var _ host.StreamSession = (*StreamHandle)(nil)

// Verify StreamHandle implements host.LineObservable at compile time.
var _ host.LineObservable = (*StreamHandle)(nil)

// --- WireEvent: normalized stream-JSON protocol event ---

// wireEvent is a normalized representation of a Claude stream-JSON protocol event.
type wireEvent struct {
	Type      string   // "system", "assistant", "result", "tool_result", "error"
	Subtype   string   // e.g. "init" for system:init
	SessionID string   // Claude wire protocol field — mapped to ResumeToken at boundary
	TextParts []string // extracted human-readable text fragments
	ToolName  string   // tool being used (from tool_use content blocks)
	Result    string   // final result text (from result events)
	IsError   bool
	RawLine   string   // original JSON line (for debug/raw log)
}

func parseWireEvent(line string) wireEvent {
	we := wireEvent{RawLine: line}

	var raw struct {
		Type      string          `json:"type"`
		Subtype   string          `json:"subtype"`
		SessionID string          `json:"session_id"`
		Result    string          `json:"result"`
		IsError   bool            `json:"is_error"`
		Message   json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return we
	}

	we.Type = raw.Type
	we.Subtype = raw.Subtype
	we.SessionID = raw.SessionID
	we.Result = raw.Result
	we.IsError = raw.IsError

	if raw.Type == "assistant" && len(raw.Message) > 0 {
		var msg struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
				Name string `json:"name"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw.Message, &msg); err == nil {
			for _, block := range msg.Content {
				switch block.Type {
				case "text":
					if block.Text != "" {
						we.TextParts = append(we.TextParts, block.Text)
					}
				case "tool_use":
					we.ToolName = block.Name
				}
			}
		}
	}

	if raw.Type == "tool_result" && len(raw.Message) > 0 {
		size := len(raw.Message)
		if size > 1024 {
			we.TextParts = append(we.TextParts, fmt.Sprintf("[tool result: %.1fKB]", float64(size)/1024))
		}
	}

	return we
}

func (we *wireEvent) semantic() string {
	switch we.Type {
	case "system":
		return ""
	case "assistant":
		var parts []string
		parts = append(parts, we.TextParts...)
		if we.ToolName != "" {
			parts = append(parts, fmt.Sprintf("[using tool: %s]", we.ToolName))
		}
		return strings.Join(parts, "\n")
	case "result":
		return we.Result
	case "tool_result":
		return strings.Join(we.TextParts, "\n")
	default:
		return ""
	}
}


// --- StreamHandle: implements host.StreamSession ---
//
// Architecture: a dedicated reader goroutine owns all stdout reads.
// This satisfies both constraints:
//   - cmd.Wait() is only called after all pipe reads complete (os/exec contract)
//   - Done() closes on process exit even when idle (no concurrent Send/Close needed)
//
// The reader goroutine reads lines from stdout and pushes them into a channel.
// Send() consumes from that channel. When stdout hits EOF (process exit), the
// reader drains, calls cmd.Wait(), and closes done.

// StreamHandle wraps a long-lived Claude process using the stream-json protocol.
type StreamHandle struct {
	cmd          *exec.Cmd
	stdin        *writerCloser
	lines        chan string    // buffered channel of stdout lines from reader goroutine
	mu           sync.Mutex    // serializes Send calls
	done         chan struct{}  // closed when process exits and Wait() completes
	err          error         // exit error from cmd.Wait()
	resumeToken  string
	output       *host.ByteBuffer
	logPath      string
	logFn        func(path, content string) error
	lineObserver func([]byte)  // optional external line observer (set before first Send)
}

// writerCloser wraps io.WriteCloser.
type writerCloser struct{ wc interface{ Write([]byte) (int, error); Close() error } }

func (w *writerCloser) Write(p []byte) (int, error) { return w.wc.Write(p) }
func (w *writerCloser) Close() error                 { return w.wc.Close() }

// StartStream launches a Claude CLI process in streaming mode and returns
// a StreamSession. This is the Claude implementation of host.Host.StartStream.
func StartStream(workdir, model, logPath string) (host.StreamSession, error) {
	args := BuildStreamArgs(model)
	cmd := exec.Command("claude", args...)
	cmd.Dir = workdir

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
		return nil, fmt.Errorf("starting claude stream: %w", err)
	}

	h := &StreamHandle{
		cmd:     cmd,
		stdin:   &writerCloser{stdin},
		lines:   make(chan string, 256),
		done:    make(chan struct{}),
		output:  host.NewByteBuffer(host.DefaultBufferCap),
		logPath: logPath,
		logFn:   appendToLogFile,
	}

	// Reader goroutine: owns all stdout reads. When stdout hits EOF
	// (process exited), it closes the lines channel, calls cmd.Wait()
	// (safe — all reads are done), and closes done.
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line != "" {
				h.lines <- line
			}
		}
		close(h.lines)
		h.err = cmd.Wait() // safe: all pipe reads are complete
		close(h.done)
	}()

	return h, nil
}

// Send writes a user message and blocks until a result event is received.
func (h *StreamHandle) Send(message string) (host.Result, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	select {
	case <-h.done:
		return host.Result{}, fmt.Errorf("stream process has exited: %v", h.err)
	default:
	}

	input := map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"role":    "user",
			"content": message,
		},
	}
	data, err := json.Marshal(input)
	if err != nil {
		return host.Result{}, fmt.Errorf("marshaling message: %w", err)
	}
	data = append(data, '\n')

	if _, err := h.stdin.Write(data); err != nil {
		return host.Result{}, fmt.Errorf("writing to stdin: %w", err)
	}

	// Read events from the reader goroutine's channel until we get a result.
	for line := range h.lines {
		if h.logPath != "" && h.logFn != nil {
			_ = h.logFn(h.logPath, line+"\n")
		}

		// Notify external observer (e.g., hostadapter for event emission).
		if h.lineObserver != nil {
			h.lineObserver([]byte(line))
		}

		we := parseWireEvent(line)
		if we.SessionID != "" {
			h.resumeToken = we.SessionID
		}
		if semantic := we.semantic(); semantic != "" {
			h.output.Write(semantic + "\n")
		}

		if we.Type == "result" {
			return host.Result{
				ResumeToken: we.SessionID,
				Output:      we.Result,
				IsError:     we.IsError,
			}, nil
		}
	}

	// Channel closed — process exited mid-conversation.
	return host.Result{}, fmt.Errorf("stream ended unexpectedly: %v", h.err)
}

// SetLineObserver registers a callback invoked for each raw protocol line.
// Must be called before the first Send(). Implements host.LineObservable.
func (h *StreamHandle) SetLineObserver(fn func([]byte)) {
	h.lineObserver = fn
}

func (h *StreamHandle) ResumeToken() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.resumeToken
}

func (h *StreamHandle) RecentOutput(maxBytes int) string {
	return h.output.Tail(maxBytes)
}

func (h *StreamHandle) Close() error {
	h.stdin.Close()
	<-h.done // reader goroutine handles Wait() after EOF
	return h.err
}

func (h *StreamHandle) Done() <-chan struct{} {
	return h.done
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
