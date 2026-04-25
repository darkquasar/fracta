package claude

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/host"
)

func TestParseWireEvent_Result(t *testing.T) {
	line := `{"type":"result","session_id":"s1","result":"done","is_error":false}`
	we := parseWireEvent(line)
	if we.Type != "result" {
		t.Errorf("Type = %q", we.Type)
	}
	if we.SessionID != "s1" {
		t.Errorf("SessionID = %q", we.SessionID)
	}
	if we.Result != "done" {
		t.Errorf("Result = %q", we.Result)
	}
}

func TestParseWireEvent_Assistant(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"hello"},{"type":"tool_use","name":"grep"}]}}`
	we := parseWireEvent(line)
	if len(we.TextParts) != 1 || we.TextParts[0] != "hello" {
		t.Errorf("TextParts = %v", we.TextParts)
	}
	if we.ToolName != "grep" {
		t.Errorf("ToolName = %q", we.ToolName)
	}
}

func TestParseWireEvent_System(t *testing.T) {
	we := parseWireEvent(`{"type":"system","subtype":"init","session_id":"s2"}`)
	if we.semantic() != "" {
		t.Errorf("semantic should be empty for system, got %q", we.semantic())
	}
}

func TestParseWireEvent_MalformedJSON(t *testing.T) {
	we := parseWireEvent("not json")
	if we.Type != "" {
		t.Errorf("Type = %q, want empty", we.Type)
	}
}

func TestWireEvent_Semantic(t *testing.T) {
	tests := []struct {
		name string
		we   wireEvent
		want string
	}{
		{"system", wireEvent{Type: "system"}, ""},
		{"result", wireEvent{Type: "result", Result: "out"}, "out"},
		{"assistant text", wireEvent{Type: "assistant", TextParts: []string{"a", "b"}}, "a\nb"},
		{"assistant tool", wireEvent{Type: "assistant", ToolName: "grep"}, "[using tool: grep]"},
		{"unknown", wireEvent{Type: "foo"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.we.semantic(); got != tt.want {
				t.Errorf("semantic() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestByteBuffer_WriteTail(t *testing.T) {
	b := host.NewByteBuffer(10)
	b.Write("hello")
	if got := b.Tail(100); got != "hello" {
		t.Errorf("Tail(100) = %q", got)
	}
	b.Write(" world!!!")
	if got := b.Tail(10); got != "o world!!!" {
		t.Errorf("Tail(10) = %q", got)
	}
}

func TestByteBuffer_Empty(t *testing.T) {
	b := host.NewByteBuffer(100)
	if got := b.Tail(10); got != "" {
		t.Errorf("Tail of empty = %q", got)
	}
}

// startMockStream creates a StreamHandle using a mock bash script.
func startMockStream(t *testing.T, script, workdir, logPath string) host.StreamSession {
	t.Helper()
	cmd := exec.Command("bash", script)
	cmd.Dir = workdir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
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
		h.err = cmd.Wait()
		close(h.done)
	}()

	return h
}

func TestStreamHandle_SendParsesResult(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mock.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
read -r input
echo '{"type":"system","subtype":"init","session_id":"test-sess"}'
echo '{"type":"result","session_id":"test-sess","result":"mock response","is_error":false}'
`), 0755)

	logPath := filepath.Join(dir, "logs", "test.log")
	h := startMockStream(t, script, dir, logPath)
	defer h.Close()

	result, err := h.Send("hello")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.ResumeToken != "test-sess" {
		t.Errorf("ResumeToken = %q", result.ResumeToken)
	}
	if result.Output != "mock response" {
		t.Errorf("Output = %q", result.Output)
	}
	if result.IsError {
		t.Error("expected IsError=false")
	}

	// Log file should exist (MkdirAll creates parent)
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("log file was not created")
	}
}

func TestStreamHandle_DoneClosesOnExit(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "exit.sh")
	os.WriteFile(script, []byte("#!/bin/bash\nexit 0\n"), 0755)

	h := startMockStream(t, script, dir, "")

	select {
	case <-h.Done():
		// good — process exited, done closed
	case <-time.After(5 * time.Second):
		t.Fatal("Done() did not close after process exit")
	}
}

func TestStreamHandle_RecentOutput(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "output.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
read -r input
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"visible output"}]}}'
echo '{"type":"result","session_id":"s1","result":"final","is_error":false}'
`), 0755)

	h := startMockStream(t, script, dir, "")
	defer h.Close()

	_, err := h.Send("test")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	output := h.RecentOutput(1024)
	if output == "" {
		t.Error("RecentOutput should contain semantic text")
	}
}

func TestStreamHandle_Close(t *testing.T) {
	dir := t.TempDir()
	// Script that reads forever until stdin closes
	script := filepath.Join(dir, "wait.sh")
	os.WriteFile(script, []byte("#!/bin/bash\ncat\n"), 0755)

	h := startMockStream(t, script, dir, "")
	err := h.Close()
	// Should not hang — Close sends EOF via stdin, reader goroutine detects EOF
	if err != nil {
		// cat exits 0 when stdin closes, but the exit might show as signal
		t.Logf("Close error (expected for killed process): %v", err)
	}

	select {
	case <-h.Done():
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("Done() not closed after Close()")
	}
}
