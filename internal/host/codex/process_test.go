package codex

import (
	"fmt"
	"strings"
	"testing"
)

func TestBuildBatchCommand_Initial(t *testing.T) {
	spec := BuildBatchCommand("do something", "o4-mini", "")
	if spec.Command != "codex" {
		t.Errorf("Command = %q, want codex", spec.Command)
	}
	args := strings.Join(spec.Args, " ")
	if !strings.Contains(args, "exec") {
		t.Errorf("args should contain 'exec': %v", spec.Args)
	}
	if strings.Contains(args, "resume") {
		t.Error("initial run should not contain 'resume'")
	}
	if !strings.Contains(args, "--json") {
		t.Error("missing --json flag")
	}
	if !strings.Contains(args, "--full-auto") {
		t.Error("missing --full-auto flag")
	}
	if !strings.Contains(args, "--ephemeral") {
		t.Error("initial run should contain --ephemeral")
	}
	if !strings.Contains(args, "--skip-git-repo-check") {
		t.Error("initial run should contain --skip-git-repo-check")
	}
	if !strings.Contains(args, "--model o4-mini") {
		t.Errorf("missing model: %v", spec.Args)
	}
	// Prompt should be last arg
	if spec.Args[len(spec.Args)-1] != "do something" {
		t.Errorf("last arg = %q, want prompt", spec.Args[len(spec.Args)-1])
	}
}

func TestBuildBatchCommand_Resume(t *testing.T) {
	spec := BuildBatchCommand("follow up", "", "thread-123")
	args := strings.Join(spec.Args, " ")
	if !strings.Contains(args, "exec resume thread-123") {
		t.Errorf("resume args should contain 'exec resume thread-123': %v", spec.Args)
	}
	if strings.Contains(args, "--model") {
		t.Error("empty model should not produce --model flag")
	}
	if strings.Contains(args, "--ephemeral") {
		t.Error("resume should not contain --ephemeral")
	}
	if strings.Contains(args, "--skip-git-repo-check") {
		t.Error("resume should not contain --skip-git-repo-check")
	}
}

func TestBuildBatchCommand_NoModel(t *testing.T) {
	spec := BuildBatchCommand("test", "", "")
	for _, arg := range spec.Args {
		if arg == "--model" {
			t.Error("empty model should not produce --model flag")
		}
	}
}

func TestParseBatchOutput_Success(t *testing.T) {
	jsonl := `{"type":"thread.started","thread_id":"abc-123"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"First message"}}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"Final answer"}}
{"type":"turn.completed","usage":{"input_tokens":100,"output_tokens":50}}
`
	result, err := ParseBatchOutput([]byte(jsonl), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ResumeToken != "abc-123" {
		t.Errorf("ResumeToken = %q, want abc-123", result.ResumeToken)
	}
	if result.Output != "Final answer" {
		t.Errorf("Output = %q, want 'Final answer'", result.Output)
	}
	if result.IsError {
		t.Error("IsError should be false")
	}
}

func TestParseBatchOutput_WithFileChange(t *testing.T) {
	jsonl := `{"type":"thread.started","thread_id":"def-456"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"Creating file"}}
{"type":"item.completed","item":{"id":"item_1","type":"file_change","changes":[{"path":"test.txt","kind":"add"}],"status":"completed"}}
{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"Done creating test.txt"}}
{"type":"turn.completed","usage":{"input_tokens":200,"output_tokens":80}}
`
	result, err := ParseBatchOutput([]byte(jsonl), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "Done creating test.txt" {
		t.Errorf("Output = %q, want last agent message", result.Output)
	}
}

func TestParseBatchOutput_WaitError_WithOutput(t *testing.T) {
	jsonl := `{"type":"thread.started","thread_id":"err-789"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"Partial work"}}
`
	result, err := ParseBatchOutput([]byte(jsonl), fmt.Errorf("exit code 1"))
	if err != nil {
		t.Fatalf("should not return error when output is available: %v", err)
	}
	if !result.IsError {
		t.Error("IsError should be true when waitErr is non-nil")
	}
	if result.ResumeToken != "err-789" {
		t.Errorf("ResumeToken = %q, want err-789", result.ResumeToken)
	}
	if result.Output != "Partial work" {
		t.Errorf("Output = %q, want 'Partial work'", result.Output)
	}
}

func TestParseBatchOutput_WaitError_NoOutput(t *testing.T) {
	_, err := ParseBatchOutput([]byte(""), fmt.Errorf("exit code 1"))
	if err == nil {
		t.Fatal("expected error when no output and waitErr")
	}
	if !strings.Contains(err.Error(), "running codex") {
		t.Errorf("error should mention codex: %v", err)
	}
}

func TestParseBatchOutput_EmptyOutput(t *testing.T) {
	_, err := ParseBatchOutput([]byte(""), nil)
	if err == nil {
		t.Fatal("expected error for empty output")
	}
	if !strings.Contains(err.Error(), "no output") {
		t.Errorf("error should mention 'no output': %v", err)
	}
}

func TestParseBatchOutput_ErrorEvent(t *testing.T) {
	jsonl := `{"type":"thread.started","thread_id":"err-event-1"}
{"type":"error","error":{"message":"rate limit exceeded","willRetry":true}}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"partial"}}
`
	_, err := ParseBatchOutput([]byte(jsonl), nil)
	if err == nil {
		t.Fatal("expected error when no turn.completed with error event")
	}
	if !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Errorf("error should contain error message: %v", err)
	}
}

func TestParseBatchOutput_ErrorEvent_WaitErr(t *testing.T) {
	// Error event with waitErr — should return partial result with error message as output
	jsonl := `{"type":"thread.started","thread_id":"err-wait-1"}
{"type":"error","error":{"message":"connection lost"}}
`
	result, err := ParseBatchOutput([]byte(jsonl), fmt.Errorf("exit code 1"))
	if err != nil {
		t.Fatalf("should return partial result, not error: %v", err)
	}
	if !result.IsError {
		t.Error("IsError should be true")
	}
	if result.Output != "connection lost" {
		t.Errorf("Output should be error message when no agent message, got: %q", result.Output)
	}
	if result.ResumeToken != "err-wait-1" {
		t.Errorf("ResumeToken = %q, want err-wait-1", result.ResumeToken)
	}
}

func TestParseBatchOutput_ItemStarted(t *testing.T) {
	// item.started events should be handled without error
	jsonl := `{"type":"thread.started","thread_id":"item-started-1"}
{"type":"turn.started"}
{"type":"item.started","item":{"id":"item_0","type":"command_execution"}}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"done"}}
{"type":"turn.completed","usage":{"input_tokens":50,"output_tokens":25}}
`
	result, err := ParseBatchOutput([]byte(jsonl), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ResumeToken != "item-started-1" {
		t.Errorf("ResumeToken = %q, want item-started-1", result.ResumeToken)
	}
	if result.Output != "done" {
		t.Errorf("Output = %q, want 'done'", result.Output)
	}
}

func TestParseBatchOutput_NoThreadID(t *testing.T) {
	// turn.completed without thread.started should fail
	jsonl := `{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"output"}}
{"type":"turn.completed","usage":{}}
`
	_, err := ParseBatchOutput([]byte(jsonl), nil)
	if err == nil {
		t.Fatal("expected error when no thread_id")
	}
	if !strings.Contains(err.Error(), "thread_id") {
		t.Errorf("error should mention thread_id: %v", err)
	}
}

func TestParseBatchOutput_NoTurnCompleted(t *testing.T) {
	// thread_id present but no turn.completed
	jsonl := `{"type":"thread.started","thread_id":"abc"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"partial"}}
`
	_, err := ParseBatchOutput([]byte(jsonl), nil)
	if err == nil {
		t.Fatal("expected error when no turn.completed")
	}
	if !strings.Contains(err.Error(), "turn.completed") {
		t.Errorf("error should mention turn.completed: %v", err)
	}
}

func TestParseBatchOutput_MalformedJSON(t *testing.T) {
	// Non-JSON lines should be skipped, not cause errors
	jsonl := `not json
{"type":"thread.started","thread_id":"ok-111"}
also not json
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"works"}}
{"type":"turn.completed","usage":{}}
`
	result, err := ParseBatchOutput([]byte(jsonl), nil)
	if err != nil {
		t.Fatalf("should skip non-JSON lines: %v", err)
	}
	if result.ResumeToken != "ok-111" {
		t.Errorf("ResumeToken = %q, want ok-111", result.ResumeToken)
	}
	if result.Output != "works" {
		t.Errorf("Output = %q, want 'works'", result.Output)
	}
}
