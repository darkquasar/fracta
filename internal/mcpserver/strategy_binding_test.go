package mcpserver

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkquasar/fracta/internal/contract"
	"github.com/darkquasar/fracta/internal/strategy"
)

// stubRunner implements strategy.Runner with only the methods loadEffectiveBinding needs.
type stubRunner struct {
	dir string
}

func (s *stubRunner) List(tags ...string) ([]strategy.StrategyInfo, error) { return nil, nil }
func (s *stubRunner) Describe(name string) (*strategy.StrategyInfo, error) { return nil, nil }
func (s *stubRunner) Run(name string, params map[string]any, manifest strategy.StagingManifest, opts *strategy.RunOptions) (*strategy.RunResult, error) {
	return nil, nil
}
func (s *stubRunner) Create(name, code, metadata string, force bool) error { return nil }
func (s *stubRunner) CreateWithContract(name, code, contractYAML string, force bool) error {
	return nil
}
func (s *stubRunner) StagingDir() string  { return "" }
func (s *stubRunner) StrategyDir() string { return s.dir }
func (s *stubRunner) Close() error        { return nil }

// Bug 2 (surfacing): loadEffectiveBinding must classify ParseBindingFile errors.
// Missing file → return globalBinding silently (Debug). Any other error → return
// globalBinding but log at Warn so operators see it in kubectl logs.
func TestLoadEffectiveBinding_ClassifiesErrors(t *testing.T) {
	dir := t.TempDir()

	globalBinding := &contract.BindingSpec{
		SourceBindings: map[string]contract.SourceBinding{
			"global_tbl": {Backend: "test"},
		},
	}

	// Capture slog output so we can assert level dispatch.
	prevDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prevDefault) })

	t.Run("missing per-strategy binding falls back silently (Debug only)", func(t *testing.T) {
		var buf bytes.Buffer
		slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

		strategySubdir := filepath.Join(dir, "missing")
		if err := os.MkdirAll(strategySubdir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		info := &strategy.StrategyInfo{Name: "missing-strat", ContractPath: "missing/contract.yaml"}
		sr := &stubRunner{dir: dir}

		got := loadEffectiveBinding(sr, info, globalBinding)
		if got != globalBinding {
			t.Errorf("expected globalBinding to be returned when binding is missing, got %+v", got)
		}
		out := buf.String()
		if !strings.Contains(out, "\"level\":\"DEBUG\"") {
			t.Errorf("expected DEBUG log line for missing binding, got: %s", out)
		}
		if strings.Contains(out, "\"level\":\"WARN\"") {
			t.Errorf("missing binding should NOT log WARN (regression of Bug 2 surfacing), got: %s", out)
		}
	})

	t.Run("corrupt per-strategy binding falls back and logs WARN", func(t *testing.T) {
		var buf bytes.Buffer
		slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

		strategySubdir := filepath.Join(dir, "broken")
		if err := os.MkdirAll(strategySubdir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		bindingPath := filepath.Join(strategySubdir, "binding.yaml")
		if err := os.WriteFile(bindingPath, []byte(":\n\tthis is: not valid: yaml ::::"), 0o644); err != nil {
			t.Fatalf("write corrupt binding: %v", err)
		}
		info := &strategy.StrategyInfo{Name: "broken-strat", ContractPath: "broken/contract.yaml"}
		sr := &stubRunner{dir: dir}

		got := loadEffectiveBinding(sr, info, globalBinding)
		if got != globalBinding {
			t.Errorf("expected globalBinding to be returned on parse error, got %+v", got)
		}
		out := buf.String()
		if !strings.Contains(out, "\"level\":\"WARN\"") {
			t.Errorf("expected WARN log for corrupt binding (Bug 2 surfacing), got: %s", out)
		}
		if !strings.Contains(out, "broken-strat") {
			t.Errorf("expected strategy name in log output, got: %s", out)
		}
	})

	t.Run("nil runner returns global", func(t *testing.T) {
		got := loadEffectiveBinding(nil, &strategy.StrategyInfo{Name: "x", ContractPath: "x/contract.yaml"}, globalBinding)
		if got != globalBinding {
			t.Errorf("expected globalBinding for nil runner, got %+v", got)
		}
	})

	t.Run("nil info returns global", func(t *testing.T) {
		got := loadEffectiveBinding(&stubRunner{dir: dir}, nil, globalBinding)
		if got != globalBinding {
			t.Errorf("expected globalBinding for nil info, got %+v", got)
		}
	})
}
