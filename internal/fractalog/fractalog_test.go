package fractalog

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestInit_SetsDefaultHandler(t *testing.T) {
	Init()
	// After Init, slog.Default() should produce JSON to stderr.
	// Smoke test: calling Component doesn't panic.
	log := Component("test")
	log.Info("init test")
}

func TestAttachFile_WritesToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")

	if err := AttachFile(path, ""); err != nil {
		t.Fatalf("AttachFile: %v", err)
	}

	// Log a message through the global handler.
	slog.Info("hello from test", "key", "value")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("log file is empty")
	}

	// Verify it's valid JSON with expected fields.
	var entry map[string]any
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("log entry is not valid JSON: %v\nraw: %s", err, data)
	}
	if msg, _ := entry["msg"].(string); msg != "hello from test" {
		t.Errorf("msg = %q, want %q", msg, "hello from test")
	}
	if val, _ := entry["key"].(string); val != "value" {
		t.Errorf("key = %q, want %q", val, "value")
	}

	// Restore default for other tests.
	Init()
}

func TestAttachFile_ComponentTag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "component.log")

	if err := AttachFile(path, ""); err != nil {
		t.Fatalf("AttachFile: %v", err)
	}

	log := Component("mycomp")
	log.Info("tagged message")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}

	var entry map[string]any
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if comp, _ := entry["component"].(string); comp != "mycomp" {
		t.Errorf("component = %q, want %q", comp, "mycomp")
	}

	Init()
}

func TestAttachFile_RelativePath(t *testing.T) {
	dir := t.TempDir()
	// Change to temp dir so relative path resolves there.
	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	os.Chdir(dir)

	if err := AttachFile("relative.log", ""); err != nil {
		t.Fatalf("AttachFile: %v", err)
	}

	slog.Info("relative path test")

	abs := filepath.Join(dir, "relative.log")
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("reading log file at %s: %v", abs, err)
	}
	if len(data) == 0 {
		t.Fatal("log file is empty")
	}

	Init()
}

func TestAttachFile_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "fracta.log")

	if err := AttachFile(path, ""); err != nil {
		t.Fatalf("AttachFile: %v", err)
	}

	slog.Info("nested dir test")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty")
	}

	Init()
}

func TestAttachFile_LevelOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "level.log")

	if err := AttachFile(path, "error"); err != nil {
		t.Fatalf("AttachFile: %v", err)
	}

	// Info should be filtered out at error level.
	slog.Info("should not appear")
	// Error should pass.
	slog.Error("should appear")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}

	content := string(data)
	if len(content) == 0 {
		t.Fatal("log file is empty")
	}

	// Should contain the error message but not the info message.
	var entry map[string]any
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("not valid JSON: %v\nraw: %s", err, content)
	}
	if msg, _ := entry["msg"].(string); msg != "should appear" {
		t.Errorf("msg = %q, want %q", msg, "should appear")
	}

	Init()
}

func TestResolveLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"", slog.LevelInfo},
		{"unknown", slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := resolveLevel(tt.input)
			if got != tt.want {
				t.Errorf("resolveLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
