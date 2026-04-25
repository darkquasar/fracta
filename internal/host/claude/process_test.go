package claude

import (
	"testing"
)

func TestBuildBatchCommand_Basic(t *testing.T) {
	spec := BuildBatchCommand("do stuff", "opus", "")
	if spec.Command != "claude" {
		t.Errorf("Command = %q, want claude", spec.Command)
	}
	found := map[string]bool{}
	for _, a := range spec.Args {
		found[a] = true
	}
	if !found["do stuff"] {
		t.Error("missing prompt in args")
	}
	if !found["--model"] {
		t.Error("missing --model flag")
	}
}

func TestBuildBatchCommand_WithSession(t *testing.T) {
	spec := BuildBatchCommand("msg", "", "sess-123")
	found := map[string]bool{}
	for _, a := range spec.Args {
		found[a] = true
	}
	if !found["sess-123"] {
		t.Error("missing session ID in args")
	}
}

func TestParseBatchOutput_Success(t *testing.T) {
	stdout := []byte(`{"session_id":"s1","result":"done","is_error":false}`)
	result, err := ParseBatchOutput(stdout, nil)
	if err != nil {
		t.Fatalf("ParseBatchOutput: %v", err)
	}
	if result.ResumeToken != "s1" {
		t.Errorf("SessionID = %q", result.ResumeToken)
	}
	if result.Output != "done" {
		t.Errorf("Output = %q", result.Output)
	}
	if result.IsError {
		t.Error("expected IsError=false")
	}
}

func TestParseBatchOutput_Error(t *testing.T) {
	stdout := []byte(`{"session_id":"s2","result":"oops","is_error":true}`)
	result, err := ParseBatchOutput(stdout, nil)
	if err != nil {
		t.Fatalf("ParseBatchOutput: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true")
	}
}

func TestParseBatchOutput_WaitError_WithOutput(t *testing.T) {
	stdout := []byte(`{"session_id":"s3","result":"partial","is_error":false}`)
	result, err := ParseBatchOutput(stdout, &testErr{})
	if err != nil {
		t.Fatalf("ParseBatchOutput should succeed with parseable output: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true when waitErr is non-nil")
	}
}

func TestParseBatchOutput_WaitError_NoOutput(t *testing.T) {
	_, err := ParseBatchOutput(nil, &testErr{})
	if err == nil {
		t.Fatal("expected error with no output and waitErr")
	}
}

func TestParseBatchOutput_MalformedJSON(t *testing.T) {
	_, err := ParseBatchOutput([]byte("not json"), nil)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

type testErr struct{}

func (e *testErr) Error() string { return "exit status 1" }
