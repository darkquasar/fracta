//go:build integration

package mcpclient

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/config"
)

// buildEchoServer compiles the test MCP server binary and returns its path.
func buildEchoServer(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file path")
	}
	serverDir := filepath.Join(filepath.Dir(thisFile), "testdata", "echoserver")
	if _, err := os.Stat(filepath.Join(serverDir, "main.go")); err != nil {
		t.Skipf("echoserver source not found: %v", err)
	}

	binPath := filepath.Join(t.TempDir(), "echoserver")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = serverDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building echoserver: %v\n%s", err, out)
	}
	return binPath
}

func TestPoolIntegration_StdioRoundTrip(t *testing.T) {
	binPath := buildEchoServer(t)

	pool := NewPool(config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"echo": {
				Local: config.MCPServerLocal{
					Command: binPath,
				},
			},
		},
	}, "local")
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// First call: connects, initializes, caches tools, then calls "echo"
	result, err := pool.CallTool(ctx, "echo", "echo", map[string]any{
		"message": "hello from integration test",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Text)
	}
	if result.Text == "" {
		t.Fatal("expected non-empty response")
	}
	t.Logf("echo response: %s", result.Text)

	// Verify it's valid JSON (the echo server returns a JSON array)
	if result.Text[0] != '[' {
		t.Errorf("expected JSON array, got: %.50s", result.Text)
	}

	// Second call: reuses existing connection (no re-init)
	result2, err := pool.CallTool(ctx, "echo", "echo", map[string]any{
		"message": "second call",
	})
	if err != nil {
		t.Fatalf("second CallTool: %v", err)
	}
	if result2.IsError {
		t.Fatalf("second call error: %s", result2.Text)
	}
	t.Logf("second response: %s", result2.Text)
}

func TestPoolIntegration_UnknownTool(t *testing.T) {
	binPath := buildEchoServer(t)

	pool := NewPool(config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"echo": {
				Local: config.MCPServerLocal{
					Command: binPath,
				},
			},
		},
	}, "local")
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := pool.CallTool(ctx, "echo", "nonexistent_tool", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	t.Logf("expected error: %v", err)
}

func TestPoolIntegration_ConcurrentCalls(t *testing.T) {
	binPath := buildEchoServer(t)

	pool := NewPool(config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"echo": {
				Local: config.MCPServerLocal{
					Command: binPath,
				},
			},
		},
	}, "local")
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 5 concurrent calls — tests concurrent first-use with real subprocess
	const n = 5
	errs := make(chan error, n)
	for i := range n {
		go func(i int) {
			_, err := pool.CallTool(ctx, "echo", "echo", map[string]any{
				"message": "concurrent " + string(rune('A'+i)),
			})
			errs <- err
		}(i)
	}

	for range n {
		if err := <-errs; err != nil {
			t.Errorf("concurrent CallTool failed: %v", err)
		}
	}
}
