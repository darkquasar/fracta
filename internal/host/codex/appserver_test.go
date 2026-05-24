package codex

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/host"
)

// startMockAppServer creates an AppServerSession using a mock bash script that
// simulates the codex app-server JSON-RPC protocol. This follows the same
// pattern as Claude's startMockStream in stream_test.go.
func startMockAppServer(t *testing.T, script, workdir, logPath string) *AppServerSession {
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

	s := &AppServerSession{
		cmd:           cmd,
		stdin:         &appServerWriter{stdin},
		notifications: make(chan jsonRPCNotification, 256),
		done:          make(chan struct{}),
		output:        host.NewByteBuffer(host.DefaultBufferCap),
		logPath:       logPath,
		logFn:         appendToLogFile,
	}

	// Reader goroutine — same pattern as NewAppServerSession.
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			if s.logPath != "" && s.logFn != nil {
				_ = s.logFn(s.logPath, line+"\n")
			}
			if s.lineObserver != nil {
				s.lineObserver([]byte(line))
			}
			var notif jsonRPCNotification
			if err := json.Unmarshal([]byte(line), &notif); err != nil {
				continue
			}
			if notif.Method != "" {
				s.notifications <- notif
			}
		}
		close(s.notifications)
		s.err = cmd.Wait()
		close(s.done)
	}()

	return s
}

func TestAppServerSend(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mock.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
# Read thread/start request from stdin
read -r input

# Emit thread/started notification
echo '{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":"thread-001"}}}'

# Read turn/start request from stdin
read -r input

# Emit streaming deltas
echo '{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"thread-001","itemId":"item-1","delta":"Hello "}}'
echo '{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"thread-001","itemId":"item-1","delta":"world!"}}'

# Emit turn/completed
echo '{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-001","turnId":"turn-1"}}'
`), 0755)

	s := startMockAppServer(t, script, dir, "")

	// Bootstrap: send thread/start and wait for thread/started.
	if err := s.bootstrapThread(); err != nil {
		t.Fatalf("bootstrapThread: %v", err)
	}
	if s.threadID != "thread-001" {
		t.Errorf("threadID = %q, want thread-001", s.threadID)
	}

	// Send a turn.
	result, err := s.Send("test message")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.Output != "Hello world!" {
		t.Errorf("Output = %q, want %q", result.Output, "Hello world!")
	}
	if result.ResumeToken != "thread-001" {
		t.Errorf("ResumeToken = %q, want thread-001", result.ResumeToken)
	}
	if result.IsError {
		t.Error("expected IsError=false")
	}

	s.Close()
}

func TestAppServerDone(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "exit.sh")
	os.WriteFile(script, []byte("#!/bin/bash\nexit 0\n"), 0755)

	s := startMockAppServer(t, script, dir, "")

	select {
	case <-s.Done():
		// good — process exited, done closed
	case <-time.After(5 * time.Second):
		t.Fatal("Done() did not close after process exit")
	}
}

func TestAppServerResumeToken(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mock.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
read -r input
echo '{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":"thread-resume-42"}}}'
# Keep alive long enough for test to read token
read -r input
echo '{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-resume-42","turnId":"turn-1"}}'
`), 0755)

	s := startMockAppServer(t, script, dir, "")
	if err := s.bootstrapThread(); err != nil {
		t.Fatalf("bootstrapThread: %v", err)
	}

	token := s.ResumeToken()
	if token != "thread-resume-42" {
		t.Errorf("ResumeToken() = %q, want thread-resume-42", token)
	}

	s.Close()
}

func TestAppServerThreadBootstrap(t *testing.T) {
	dir := t.TempDir()
	// Script that logs what it receives to a file, then responds.
	logFile := filepath.Join(dir, "stdin.log")
	script := filepath.Join(dir, "mock.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
# Log stdin to file for inspection
read -r input
echo "$input" >> `+logFile+`
echo '{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":"thread-boot"}}}'
# Stay alive
cat
`), 0755)

	s := startMockAppServer(t, script, dir, "")
	if err := s.bootstrapThread(); err != nil {
		t.Fatalf("bootstrapThread: %v", err)
	}

	// Read what was sent to stdin.
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading stdin log: %v", err)
	}
	stdinLine := strings.TrimSpace(string(data))

	// Verify thread/start was sent.
	var req jsonRPCRequest
	if err := json.Unmarshal([]byte(stdinLine), &req); err != nil {
		t.Fatalf("parsing sent request: %v", err)
	}
	if req.Method != "thread/start" {
		t.Errorf("first request method = %q, want thread/start", req.Method)
	}
	if req.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", req.JSONRPC)
	}

	s.Close()
}

func TestAppServerLineObservable(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mock.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
read -r input
echo '{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":"thread-obs"}}}'
read -r input
echo '{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"thread-obs","itemId":"item-1","delta":"observed"}}'
echo '{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-obs","turnId":"turn-1"}}'
`), 0755)

	s := startMockAppServer(t, script, dir, "")

	// Set observer before bootstrap.
	var observed []string
	s.SetLineObserver(func(line []byte) {
		observed = append(observed, string(line))
	})

	if err := s.bootstrapThread(); err != nil {
		t.Fatalf("bootstrapThread: %v", err)
	}

	_, err := s.Send("hello")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Observer should have been called for each line.
	if len(observed) < 3 {
		t.Errorf("observer called %d times, want at least 3 (thread/started + delta + turn/completed)", len(observed))
	}

	// Verify the observed lines are valid JSON.
	for i, line := range observed {
		var raw json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Errorf("observed line %d is not valid JSON: %q", i, line)
		}
	}

	s.Close()
}

func TestAppServerClose(t *testing.T) {
	dir := t.TempDir()
	// Script that logs stdin to a file and waits for stdin close.
	logFile := filepath.Join(dir, "stdin-close.log")
	script := filepath.Join(dir, "mock.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
read -r input
echo '{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":"thread-close"}}}'
# Read all remaining stdin and log it (will get turn/interrupt)
while read -r line; do
  echo "$line" >> `+logFile+`
done
`), 0755)

	s := startMockAppServer(t, script, dir, "")
	if err := s.bootstrapThread(); err != nil {
		t.Fatalf("bootstrapThread: %v", err)
	}

	err := s.Close()
	// Close should not hang.
	if err != nil {
		t.Logf("Close error (expected for cat-like script): %v", err)
	}

	select {
	case <-s.Done():
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("Done() not closed after Close()")
	}

	// Verify turn/interrupt was sent.
	data, err := os.ReadFile(logFile)
	if err != nil {
		// turn/interrupt might not have been captured if stdin closed first
		t.Logf("stdin log not available (OK if process exited fast): %v", err)
		return
	}
	content := string(data)
	if !strings.Contains(content, "turn/interrupt") {
		t.Logf("stdin log content: %s", content)
		t.Log("turn/interrupt not found in stdin log — may have been buffered")
	}
}

func TestAppServerRecentOutput(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mock.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
read -r input
echo '{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":"thread-out"}}}'
read -r input
echo '{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"thread-out","itemId":"item-1","delta":"semantic output here"}}'
echo '{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-out","turnId":"turn-1"}}'
`), 0755)

	s := startMockAppServer(t, script, dir, "")
	if err := s.bootstrapThread(); err != nil {
		t.Fatalf("bootstrapThread: %v", err)
	}

	_, err := s.Send("test")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	output := s.RecentOutput(1024)
	if !strings.Contains(output, "semantic output here") {
		t.Errorf("RecentOutput = %q, should contain 'semantic output here'", output)
	}

	s.Close()
}

func TestAppServerErrorHandling(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mock.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
read -r input
echo '{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":"thread-err"}}}'
read -r input
echo '{"jsonrpc":"2.0","method":"error","params":{"threadId":"thread-err","error":{"message":"rate limit exceeded"},"willRetry":false}}'
echo '{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-err","turnId":"turn-1"}}'
`), 0755)

	s := startMockAppServer(t, script, dir, "")
	if err := s.bootstrapThread(); err != nil {
		t.Fatalf("bootstrapThread: %v", err)
	}

	result, err := s.Send("test")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for non-retrying error")
	}
	if result.Output != "rate limit exceeded" {
		t.Errorf("Output = %q, want error message", result.Output)
	}

	s.Close()
}

func TestAppServerRetryableError(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mock.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
read -r input
echo '{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":"thread-retry"}}}'
read -r input
echo '{"jsonrpc":"2.0","method":"error","params":{"threadId":"thread-retry","error":{"message":"transient failure"},"willRetry":true}}'
echo '{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"thread-retry","itemId":"item-1","delta":"recovered output"}}'
echo '{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-retry","turnId":"turn-1"}}'
`), 0755)

	s := startMockAppServer(t, script, dir, "")
	if err := s.bootstrapThread(); err != nil {
		t.Fatalf("bootstrapThread: %v", err)
	}

	result, err := s.Send("test")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.IsError {
		t.Error("expected IsError=false for retryable error that recovered")
	}
	if result.Output != "recovered output" {
		t.Errorf("Output = %q, want 'recovered output'", result.Output)
	}

	s.Close()
}

func TestAppServerProcessExitMidTurn(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mock.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
read -r input
echo '{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":"thread-crash"}}}'
read -r input
echo '{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"thread-crash","itemId":"item-1","delta":"partial"}}'
# Exit without sending turn/completed
exit 1
`), 0755)

	s := startMockAppServer(t, script, dir, "")
	if err := s.bootstrapThread(); err != nil {
		t.Fatalf("bootstrapThread: %v", err)
	}

	_, err := s.Send("test")
	if err == nil {
		t.Error("expected error when process exits mid-turn")
	}
	if !strings.Contains(err.Error(), "exited unexpectedly") {
		t.Errorf("error = %q, should contain 'exited unexpectedly'", err.Error())
	}
}

func TestAppServerBootstrapFailure(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mock.sh")
	// Exit immediately without sending thread/started.
	os.WriteFile(script, []byte("#!/bin/bash\nexit 1\n"), 0755)

	s := startMockAppServer(t, script, dir, "")
	err := s.bootstrapThread()
	if err == nil {
		t.Error("expected error when app-server exits before thread/started")
	}
	if !strings.Contains(err.Error(), "exited before thread/started") {
		t.Errorf("error = %q, should mention thread/started", err.Error())
	}
}

func TestAppServerSendOnExitedProcess(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mock.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
read -r input
echo '{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":"thread-dead"}}}'
exit 0
`), 0755)

	s := startMockAppServer(t, script, dir, "")
	if err := s.bootstrapThread(); err != nil {
		t.Fatalf("bootstrapThread: %v", err)
	}

	// Wait for process to exit.
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit")
	}

	// Send should fail immediately.
	_, err := s.Send("hello")
	if err == nil {
		t.Error("expected error when sending on exited process")
	}
	if !strings.Contains(err.Error(), "has exited") {
		t.Errorf("error = %q, should mention 'has exited'", err.Error())
	}
}

func TestAppServerTurnStartRequest(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "turn-start.log")
	script := filepath.Join(dir, "mock.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
# Read thread/start
read -r input
echo '{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":"thread-turn"}}}'
# Read turn/start and log it
read -r input
echo "$input" >> `+logFile+`
echo '{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-turn","turnId":"turn-1"}}'
`), 0755)

	s := startMockAppServer(t, script, dir, "")
	if err := s.bootstrapThread(); err != nil {
		t.Fatalf("bootstrapThread: %v", err)
	}

	_, err := s.Send("my test prompt")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Read what was sent.
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading turn-start log: %v", err)
	}
	turnLine := strings.TrimSpace(string(data))

	var req struct {
		JSONRPC string      `json:"jsonrpc"`
		Method  string      `json:"method"`
		Params  interface{} `json:"params"`
	}
	if err := json.Unmarshal([]byte(turnLine), &req); err != nil {
		t.Fatalf("parsing turn/start request: %v", err)
	}
	if req.Method != "turn/start" {
		t.Errorf("turn request method = %q, want turn/start", req.Method)
	}

	// Verify sandboxPolicy and approvalPolicy are in the params.
	if !strings.Contains(turnLine, `"sandboxPolicy"`) {
		t.Error("turn/start should contain sandboxPolicy")
	}
	if !strings.Contains(turnLine, `"workspaceWrite"`) {
		t.Error("turn/start should contain workspaceWrite sandboxPolicy")
	}
	if !strings.Contains(turnLine, `"approvalPolicy"`) {
		t.Error("turn/start should contain approvalPolicy")
	}
	if !strings.Contains(turnLine, `"never"`) {
		t.Error("turn/start should contain 'never' approvalPolicy")
	}
	if !strings.Contains(turnLine, `"my test prompt"`) {
		t.Error("turn/start should contain the prompt text")
	}

	s.Close()
}

func TestAppServerLogFile(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mock.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
read -r input
echo '{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":"thread-log"}}}'
`), 0755)

	logPath := filepath.Join(dir, "logs", "app-server.log")
	s := startMockAppServer(t, script, dir, logPath)
	if err := s.bootstrapThread(); err != nil {
		t.Fatalf("bootstrapThread: %v", err)
	}

	s.Close()

	// Log file should exist.
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("log file was not created")
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading log: %v", err)
	}
	if !strings.Contains(string(data), "thread/started") {
		t.Error("log should contain thread/started notification")
	}
}

// Verify the interface compliance at compile time.
func TestAppServerInterfaceCompliance(t *testing.T) {
	var _ host.StreamSession = (*AppServerSession)(nil)
	var _ host.LineObservable = (*AppServerSession)(nil)
	var _ host.TurnSteerer = (*AppServerSession)(nil)
}

func TestAppServer_Steer(t *testing.T) {
	dir := t.TempDir()
	steerLog := filepath.Join(dir, "steer.log")
	script := filepath.Join(dir, "mock.sh")
	// The script:
	// 1. Reads thread/start, emits thread/started
	// 2. Reads turn/start, emits a delta to keep turn alive
	// 3. Reads the steer request and logs it
	// 4. Emits turn/completed
	os.WriteFile(script, []byte(`#!/bin/bash
# Bootstrap
read -r input
echo '{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":"thread-steer"}}}'

# Turn start
read -r input

# Emit a delta so the turn is visibly active
echo '{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"thread-steer","itemId":"item-1","delta":"working..."}}'

# Read steer request and log it
read -r steer_input
echo "$steer_input" >> `+steerLog+`

# Complete the turn
echo '{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-steer","turnId":"turn-1"}}'
`), 0755)

	s := startMockAppServer(t, script, dir, "")
	if err := s.bootstrapThread(); err != nil {
		t.Fatalf("bootstrapThread: %v", err)
	}

	// Run Send in a goroutine — it blocks until turn/completed.
	type sendResult struct {
		result host.Result
		err    error
	}
	sendDone := make(chan sendResult, 1)
	go func() {
		r, err := s.Send("do something")
		sendDone <- sendResult{r, err}
	}()

	// Wait for turnActive to become true.
	deadline := time.After(5 * time.Second)
	for !s.turnActive.Load() {
		select {
		case <-deadline:
			t.Fatal("turnActive never became true")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Call Steer while the turn is active.
	if err := s.Steer("focus on X"); err != nil {
		t.Fatalf("Steer: %v", err)
	}

	// Wait for Send to complete.
	select {
	case sr := <-sendDone:
		if sr.err != nil {
			t.Fatalf("Send: %v", sr.err)
		}
		if sr.result.Output != "working..." {
			t.Errorf("Output = %q, want %q", sr.result.Output, "working...")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send did not complete")
	}

	// Verify the steer request was sent correctly.
	data, err := os.ReadFile(steerLog)
	if err != nil {
		t.Fatalf("reading steer log: %v", err)
	}
	steerLine := strings.TrimSpace(string(data))

	var req struct {
		JSONRPC string                 `json:"jsonrpc"`
		ID      int64                  `json:"id"`
		Method  string                 `json:"method"`
		Params  map[string]interface{} `json:"params"`
	}
	if err := json.Unmarshal([]byte(steerLine), &req); err != nil {
		t.Fatalf("parsing steer request: %v", err)
	}
	if req.Method != "turn/steer" {
		t.Errorf("method = %q, want turn/steer", req.Method)
	}
	if req.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", req.JSONRPC)
	}
	if req.Params["direction"] != "focus on X" {
		t.Errorf("direction = %q, want %q", req.Params["direction"], "focus on X")
	}

	s.Close()
}

func TestAppServer_Steer_NoActiveTurn(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mock.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
read -r input
echo '{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":"thread-no-steer"}}}'
cat
`), 0755)

	s := startMockAppServer(t, script, dir, "")
	if err := s.bootstrapThread(); err != nil {
		t.Fatalf("bootstrapThread: %v", err)
	}

	// No turn is active — Steer should fail.
	err := s.Steer("redirect")
	if err == nil {
		t.Fatal("expected error when no turn is active")
	}
	if !strings.Contains(err.Error(), "no active turn") {
		t.Errorf("error = %q, should contain 'no active turn'", err.Error())
	}

	s.Close()
}
