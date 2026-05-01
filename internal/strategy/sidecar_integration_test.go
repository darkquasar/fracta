//go:build integration

package strategy

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// findProjectRoot walks up from cwd to find go.mod
func findProjectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root (go.mod)")
		}
		dir = parent
	}
}

func TestSidecarIntegration(t *testing.T) {
	// Prefer uv (manages deps automatically), fall back to python3
	var sidecarOpts []SidecarOption
	uvBin, uvErr := exec.LookPath("uv")
	if uvErr == nil {
		sidecarOpts = append(sidecarOpts, WithUVBin(uvBin))
	}

	pythonBin, pyErr := exec.LookPath("python3")
	if uvErr != nil && pyErr != nil {
		t.Skip("neither uv nor python3 found in PATH")
	}

	root := findProjectRoot(t)
	strategyDir := filepath.Join(root, "strategies")
	runnerPath := filepath.Join(strategyDir, "runner.py")

	if _, err := os.Stat(runnerPath); os.IsNotExist(err) {
		t.Skipf("runner.py not found at %s", runnerPath)
	}

	sockPath := "/tmp/fracta-strategy-test.sock"
	defer os.Remove(sockPath)

	sc, err := NewSidecar(pythonBin, runnerPath, sockPath, strategyDir, sidecarOpts...)
	if err != nil {
		t.Fatalf("NewSidecar: %v", err)
	}
	defer sc.Close()

	// Test List
	t.Run("List", func(t *testing.T) {
		strategies, err := sc.List()
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(strategies) == 0 {
			t.Fatal("expected at least 1 strategy, got 0")
		}
		// Verify each has name and description
		for _, s := range strategies {
			if s.Name == "" {
				t.Error("strategy has empty name")
			}
			if s.Description == "" {
				t.Errorf("strategy %q has empty description", s.Name)
			}
		}
		t.Logf("discovered %d strategies", len(strategies))
	})

	// Test Describe
	t.Run("Describe", func(t *testing.T) {
		// First get the list to find a real strategy name
		strategies, err := sc.List()
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(strategies) == 0 {
			t.Skip("no strategies to describe")
		}

		name := strategies[0].Name
		info, err := sc.Describe(name)
		if err != nil {
			t.Fatalf("Describe(%q): %v", name, err)
		}
		if info.Name != name {
			t.Errorf("name = %q, want %q", info.Name, name)
		}
		t.Logf("described %q: %s", info.Name, info.Description)
	})

	// Test Describe non-existent
	t.Run("DescribeNotFound", func(t *testing.T) {
		_, err := sc.Describe("nonexistent-strategy-xyz")
		if err == nil {
			t.Error("expected error for nonexistent strategy, got nil")
		}
	})

	// Test Run (requires duckdb and strategy dependencies)
	t.Run("Run", func(t *testing.T) {
		strategies, err := sc.List()
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(strategies) == 0 {
			t.Skip("no strategies to run")
		}

		// Try running the first strategy with empty params
		name := strategies[0].Name
		result, err := sc.Run(name, map[string]any{}, nil)
		if err != nil {
			t.Fatalf("Run(%q): %v", name, err)
		}
		// Result might be "error" if params are required, but we should still get a response
		t.Logf("run %q: status=%s, steps=%d", name, result.Status, len(result.Trace.Steps))
	})

	// Test sidecar stays alive across multiple calls
	t.Run("StaysAlive", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			_, err := sc.List()
			if err != nil {
				t.Fatalf("List (call %d): %v", i+1, err)
			}
		}
	})
}
