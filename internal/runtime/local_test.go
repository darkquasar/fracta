package runtime

import (
	"context"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/model"
)

func TestLocalBackend_SpawnAndWait(t *testing.T) {
	b := NewLocalBackend()
	ctx := context.Background()

	h, err := b.Spawn(ctx, SpawnOpts{
		ID:      "test-echo",
		Command: "echo",
		Args:    []string{"hello world"},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if err := h.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	out, _ := io.ReadAll(h.Output())
	if got := string(out); got != "hello world\n" {
		t.Errorf("Output = %q, want %q", got, "hello world\n")
	}

	if h.ExitCode() != 0 {
		t.Errorf("ExitCode = %d, want 0", h.ExitCode())
	}

	if h.StartTime().IsZero() {
		t.Error("StartTime is zero")
	}
}

func TestLocalBackend_SpawnFailingCommand(t *testing.T) {
	b := NewLocalBackend()
	ctx := context.Background()

	h, err := b.Spawn(ctx, SpawnOpts{
		ID:      "test-fail",
		Command: "false",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if err := h.Wait(); err == nil {
		t.Fatal("Wait should return error for failing command")
	}

	if h.ExitCode() == 0 {
		t.Error("ExitCode should be non-zero for failing command")
	}
}

func TestLocalBackend_SpawnMissingCommand(t *testing.T) {
	b := NewLocalBackend()
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID: "test-missing",
	})
	if err == nil {
		t.Fatal("Spawn with empty command should fail")
	}
}

func TestLocalBackend_SpawnNonexistentBinary(t *testing.T) {
	b := NewLocalBackend()
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:      "test-nobin",
		Command: "this-binary-does-not-exist-12345",
	})
	if err == nil {
		t.Fatal("Spawn with nonexistent binary should fail")
	}
}

func TestLocalBackend_Status(t *testing.T) {
	b := NewLocalBackend()
	ctx := context.Background()

	// Spawn a short-lived process
	h, err := b.Spawn(ctx, SpawnOpts{
		ID:      "test-status",
		Command: "echo",
		Args:    []string{"done"},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	h.Wait()

	status, err := b.Status(ctx, "test-status")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != model.StatusCompleted {
		t.Errorf("Status = %q, want %q", status, model.StatusCompleted)
	}
}

func TestLocalBackend_StatusRunning(t *testing.T) {
	b := NewLocalBackend()
	ctx := context.Background()

	cmd := "sleep"
	if runtime.GOOS == "windows" {
		t.Skip("sleep not available on Windows")
	}

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:      "test-running",
		Command: cmd,
		Args:    []string{"10"},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Give it a moment to start
	time.Sleep(50 * time.Millisecond)

	status, err := b.Status(ctx, "test-running")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != model.StatusRunning {
		t.Errorf("Status = %q, want %q", status, model.StatusRunning)
	}

	// Clean up
	b.Kill(ctx, "test-running")
}

func TestLocalBackend_Kill(t *testing.T) {
	b := NewLocalBackend()
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:      "test-kill",
		Command: "sleep",
		Args:    []string{"60"},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	if err := b.Kill(ctx, "test-kill"); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	// Should be gone from handles now
	_, err = b.Status(ctx, "test-kill")
	if err == nil {
		t.Error("Status after Kill should return error (agent removed)")
	}
}

func TestLocalBackend_KillNotFound(t *testing.T) {
	b := NewLocalBackend()
	ctx := context.Background()

	err := b.Kill(ctx, "nonexistent")
	if err == nil {
		t.Error("Kill nonexistent agent should fail")
	}
}

func TestLocalBackend_StatusNotFound(t *testing.T) {
	b := NewLocalBackend()
	ctx := context.Background()

	_, err := b.Status(ctx, "nonexistent")
	if err == nil {
		t.Error("Status for nonexistent agent should fail")
	}
}

func TestLocalBackend_SpawnWithEnv(t *testing.T) {
	b := NewLocalBackend()
	ctx := context.Background()

	h, err := b.Spawn(ctx, SpawnOpts{
		ID:      "test-env",
		Command: "sh",
		Args:    []string{"-c", "echo $TEST_VAR"},
		Env:     []string{"TEST_VAR=hello_from_env"},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if err := h.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	out, _ := io.ReadAll(h.Output())
	if got := string(out); got != "hello_from_env\n" {
		t.Errorf("Output = %q, want %q", got, "hello_from_env\n")
	}
}

func TestLocalBackend_ExitCodeBeforeWait(t *testing.T) {
	b := NewLocalBackend()
	ctx := context.Background()

	h, err := b.Spawn(ctx, SpawnOpts{
		ID:      "test-exitcode-early",
		Command: "sleep",
		Args:    []string{"10"},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// ExitCode before Wait should return -1
	if h.ExitCode() != -1 {
		t.Errorf("ExitCode before Wait = %d, want -1", h.ExitCode())
	}

	b.Kill(ctx, "test-exitcode-early")
}

func TestLocalBackend_StatusFailed(t *testing.T) {
	b := NewLocalBackend()
	ctx := context.Background()

	h, err := b.Spawn(ctx, SpawnOpts{
		ID:      "test-fail-status",
		Command: "false",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	h.Wait()

	status, err := b.Status(ctx, "test-fail-status")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != model.StatusFailed {
		t.Errorf("Status = %q, want %q", status, model.StatusFailed)
	}
}

func TestLocalBackend_RejectsSecretRef(t *testing.T) {
	b := NewLocalBackend()
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:      "test-secret-reject",
		Command: "echo",
		Args:    []string{"should not run"},
		HostEnv: []EnvEntry{
			{Name: "TEST_PLAIN", Value: "ok"},
			{
				Name:      "TEST_SECRET",
				SecretRef: &SecretRef{Name: "vault-secret", Key: "api-key"},
			},
		},
	})
	if err == nil {
		t.Fatal("Spawn with SecretRef should fail for local backend")
	}
	if !strings.Contains(err.Error(), "secret_ref") {
		t.Errorf("error = %q, should mention secret_ref", err.Error())
	}
	if !strings.Contains(err.Error(), "TEST_SECRET") {
		t.Errorf("error = %q, should mention the env var name", err.Error())
	}
}

func TestLocalBackend_AcceptsPlainHostEnv(t *testing.T) {
	b := NewLocalBackend()
	ctx := context.Background()

	h, err := b.Spawn(ctx, SpawnOpts{
		ID:      "test-hostenv",
		Command: "sh",
		Args:    []string{"-c", "echo $TEST_HOST_VAR"},
		HostEnv: []EnvEntry{
			{Name: "TEST_HOST_VAR", Value: "from-host-config"},
		},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if err := h.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	out, _ := io.ReadAll(h.Output())
	if got := string(out); got != "from-host-config\n" {
		t.Errorf("Output = %q, want %q", got, "from-host-config\n")
	}
}
