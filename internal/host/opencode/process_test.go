package opencode

import (
	"fmt"
	"strings"
	"testing"
)

func TestBuildBatchCommand_Initial(t *testing.T) {
	spec := BuildBatchCommand("do something", "amazon-bedrock/anthropic.claude-sonnet-4-6", "")
	if spec.Command != "opencode" {
		t.Errorf("Command = %q, want opencode", spec.Command)
	}
	args := strings.Join(spec.Args, " ")
	if !strings.Contains(args, "run") {
		t.Errorf("args should contain 'run': %v", spec.Args)
	}
	if !strings.Contains(args, "--format json") {
		t.Error("missing --format json flag")
	}
	if !strings.Contains(args, "--model amazon-bedrock/anthropic.claude-sonnet-4-6") {
		t.Errorf("missing model: %v", spec.Args)
	}
	// Prompt should be last arg.
	if spec.Args[len(spec.Args)-1] != "do something" {
		t.Errorf("last arg = %q, want prompt", spec.Args[len(spec.Args)-1])
	}
	// Must NOT contain --dangerously-skip-permissions.
	if strings.Contains(args, "--dangerously-skip-permissions") {
		t.Error("must NOT include --dangerously-skip-permissions by default (spec Risk 2)")
	}
}

func TestBuildBatchCommand_Resume(t *testing.T) {
	spec := BuildBatchCommand("follow up", "", "ses_abc123")
	args := strings.Join(spec.Args, " ")
	if !strings.Contains(args, "--session ses_abc123") {
		t.Errorf("resume args should contain '--session ses_abc123': %v", spec.Args)
	}
	if strings.Contains(args, "--model") {
		t.Error("empty model should not produce --model flag")
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

func TestBuildBatchCommand_NoDangerousPermissions(t *testing.T) {
	// Verify --dangerously-skip-permissions is never present by default.
	spec := BuildBatchCommand("task", "model", "")
	for _, arg := range spec.Args {
		if strings.Contains(arg, "dangerously") {
			t.Errorf("must not contain dangerous flag: %s", arg)
		}
	}
}

func TestParseBatchOutput_Success(t *testing.T) {
	ndjson := `{"type":"text","timestamp":1713700000000,"sessionID":"ses_abc123","part":{"type":"text","text":"First response"}}
{"type":"text","timestamp":1713700001000,"sessionID":"ses_abc123","part":{"type":"text","text":"Second response"}}
`
	result, err := ParseBatchOutput([]byte(ndjson), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ResumeToken != "ses_abc123" {
		t.Errorf("ResumeToken = %q, want ses_abc123", result.ResumeToken)
	}
	if !strings.Contains(result.Output, "First response") {
		t.Errorf("Output should contain first response: %q", result.Output)
	}
	if !strings.Contains(result.Output, "Second response") {
		t.Errorf("Output should contain second response: %q", result.Output)
	}
	if result.IsError {
		t.Error("IsError should be false")
	}
}

func TestParseBatchOutput_WithToolUse(t *testing.T) {
	ndjson := `{"type":"text","sessionID":"ses_def456","part":{"type":"text","text":"Let me check"}}
{"type":"tool_use","sessionID":"ses_def456","part":{"type":"tool","tool":"bash","state":{"status":"completed","input":"ls","output":"file.txt"}}}
{"type":"text","sessionID":"ses_def456","part":{"type":"text","text":"Found the file"}}
`
	result, err := ParseBatchOutput([]byte(ndjson), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ResumeToken != "ses_def456" {
		t.Errorf("ResumeToken = %q, want ses_def456", result.ResumeToken)
	}
	// Output should concatenate text parts, not tool_use.
	if !strings.Contains(result.Output, "Found the file") {
		t.Errorf("Output = %q, want text containing 'Found the file'", result.Output)
	}
}

func TestParseBatchOutput_Error(t *testing.T) {
	ndjson := `{"type":"text","sessionID":"ses_err","part":{"type":"text","text":"Starting..."}}
{"type":"error","sessionID":"ses_err","error":{"name":"ProviderError","data":{"message":"Rate limit exceeded"}}}
`
	result, err := ParseBatchOutput([]byte(ndjson), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("IsError should be true when error event present")
	}
	if result.ResumeToken != "ses_err" {
		t.Errorf("ResumeToken = %q, want ses_err", result.ResumeToken)
	}
}

func TestParseBatchOutput_ErrorOnly(t *testing.T) {
	ndjson := `{"type":"error","sessionID":"ses_err2","error":{"name":"ConfigError","data":{"message":"Invalid model"}}}
`
	result, err := ParseBatchOutput([]byte(ndjson), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("IsError should be true")
	}
	if result.Output != "Invalid model" {
		t.Errorf("Output = %q, want error message 'Invalid model'", result.Output)
	}
}

func TestParseBatchOutput_WaitError_WithOutput(t *testing.T) {
	ndjson := `{"type":"text","sessionID":"ses_partial","part":{"type":"text","text":"Partial work"}}
`
	result, err := ParseBatchOutput([]byte(ndjson), fmt.Errorf("exit code 1"))
	if err != nil {
		t.Fatalf("should not return error when output is available: %v", err)
	}
	if !result.IsError {
		t.Error("IsError should be true when waitErr is non-nil")
	}
	if result.ResumeToken != "ses_partial" {
		t.Errorf("ResumeToken = %q, want ses_partial", result.ResumeToken)
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
	if !strings.Contains(err.Error(), "running opencode") {
		t.Errorf("error should mention opencode: %v", err)
	}
}

func TestParseBatchOutput_EmptyOutput(t *testing.T) {
	result, err := ParseBatchOutput([]byte(""), nil)
	if err != nil {
		t.Fatalf("empty output with no waitErr should not error: %v", err)
	}
	// Empty output is valid — session may have done tool-only work.
	if result.IsError {
		t.Error("empty output should not be an error")
	}
}

func TestParseBatchOutput_MalformedJSON(t *testing.T) {
	ndjson := `not json
{"type":"text","sessionID":"ses_ok","part":{"type":"text","text":"works"}}
also not json
`
	result, err := ParseBatchOutput([]byte(ndjson), nil)
	if err != nil {
		t.Fatalf("should skip non-JSON lines: %v", err)
	}
	if result.ResumeToken != "ses_ok" {
		t.Errorf("ResumeToken = %q, want ses_ok", result.ResumeToken)
	}
	if result.Output != "works" {
		t.Errorf("Output = %q, want 'works'", result.Output)
	}
}

func TestParseBatchOutput_SessionIDFromFirstEvent(t *testing.T) {
	// sessionID should come from the first event that contains it.
	ndjson := `{"type":"step_start","sessionID":"ses_first","part":{"type":"step-start"}}
{"type":"text","sessionID":"ses_second","part":{"type":"text","text":"output"}}
`
	result, err := ParseBatchOutput([]byte(ndjson), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ResumeToken != "ses_first" {
		t.Errorf("ResumeToken = %q, want ses_first (from first event)", result.ResumeToken)
	}
}

func TestParseBatchOutput_StepEvents(t *testing.T) {
	// step_start and step_finish should not affect output or error state.
	ndjson := `{"type":"step_start","sessionID":"ses_steps","part":{"type":"step-start"}}
{"type":"text","sessionID":"ses_steps","part":{"type":"text","text":"thinking..."}}
{"type":"step_finish","sessionID":"ses_steps","part":{"type":"step-finish"}}
`
	result, err := ParseBatchOutput([]byte(ndjson), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("step events should not cause error")
	}
	if result.Output != "thinking..." {
		t.Errorf("Output = %q, want 'thinking...'", result.Output)
	}
}
