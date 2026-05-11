package mcpcatalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// installShim writes a small shell-script "docker" or "podman" into a
// temp dir, prepends that dir to PATH for the duration of the test, and
// returns the dir path. The script's body comes from caller — it should
// produce the desired exit code and (optionally) stdout/stderr.
func installShim(t *testing.T, name, scriptBody string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell shims not supported on windows")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" + scriptBody + "\n"
	bin := filepath.Join(dir, name)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+prev)
	return dir
}

func TestCLIInspector_Present(t *testing.T) {
	installShim(t, "docker", `echo '[{"Id":"sha256:abc"}]'; exit 0`)
	insp := NewDockerCLIInspector()
	state, err := insp.HasImage(context.Background(), "docker.elastic.co/mcp/elasticsearch:latest")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if state != ImageStatePresent {
		t.Errorf("state = %v, want present", state)
	}
	if insp.Name() != "docker" {
		t.Errorf("Name() = %q", insp.Name())
	}
}

func TestCLIInspector_Absent(t *testing.T) {
	installShim(t, "docker", `exit 1`)
	insp := NewDockerCLIInspector()
	state, err := insp.HasImage(context.Background(), "no/such:image")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if state != ImageStateAbsent {
		t.Errorf("state = %v, want absent", state)
	}
}

func TestCLIInspector_AbsentWithLocalizedStderr(t *testing.T) {
	// Localised stderr text must NOT affect classification.
	installShim(t, "docker", `echo "イメージが見つかりません" >&2; exit 1`)
	insp := NewDockerCLIInspector()
	state, err := insp.HasImage(context.Background(), "no/such:image")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if state != ImageStateAbsent {
		t.Errorf("state = %v, want absent (exit 1 with empty stdout regardless of stderr)", state)
	}
}

func TestCLIInspector_NonAbsentNonZeroExit(t *testing.T) {
	// exit code other than 1 → unknown, not absent.
	installShim(t, "docker", `exit 125`)
	insp := NewDockerCLIInspector()
	state, _ := insp.HasImage(context.Background(), "x")
	if state != ImageStateUnknown {
		t.Errorf("state = %v, want unknown for exit 125", state)
	}
}

func TestCLIInspector_BinaryMissing(t *testing.T) {
	// Clear PATH so the docker binary cannot be found.
	t.Setenv("PATH", "/nonexistent-dir-for-fracta-tests-only")
	insp := NewDockerCLIInspector()
	state, err := insp.HasImage(context.Background(), "x")
	if state != ImageStateUnknown {
		t.Errorf("state = %v, want unknown when binary missing", state)
	}
	if err == nil {
		t.Errorf("err should not be nil for missing binary")
	}
}

func TestCLIInspector_Timeout(t *testing.T) {
	// Sleep longer than the test timeout.
	installShim(t, "docker", `sleep 10`)
	insp := &cliInspector{bin: "docker", timeout: 50 * time.Millisecond}
	state, err := insp.HasImage(context.Background(), "x")
	if state != ImageStateUnknown {
		t.Errorf("state = %v, want unknown on timeout", state)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want DeadlineExceeded", err)
	}
}

func TestNoopInspector(t *testing.T) {
	insp := NewNoopInspector()
	state, err := insp.HasImage(context.Background(), "x")
	if err != nil {
		t.Errorf("err = %v", err)
	}
	if state != ImageStateUnknown {
		t.Errorf("state = %v, want unknown", state)
	}
	if insp.Name() != "noop" {
		t.Errorf("name = %q", insp.Name())
	}
}

func TestDetectImageInspector_PrefersDocker(t *testing.T) {
	// Install both shims; docker should be picked first.
	dir := t.TempDir()
	for _, name := range []string{"docker", "podman"} {
		bin := filepath.Join(dir, name)
		if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	insp := DetectImageInspector()
	if insp.Name() != "docker" {
		t.Errorf("Detect returned %q, want docker (preference order)", insp.Name())
	}
}

func TestDetectImageInspector_FallsBackToPodman(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "podman")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	insp := DetectImageInspector()
	if insp.Name() != "podman" {
		t.Errorf("Detect returned %q, want podman", insp.Name())
	}
}

func TestDetectImageInspector_Noop(t *testing.T) {
	t.Setenv("PATH", "/nonexistent-dir-for-fracta-tests-only")
	insp := DetectImageInspector()
	if insp.Name() != "noop" {
		t.Errorf("Detect returned %q, want noop", insp.Name())
	}
}

func TestImageState_String(t *testing.T) {
	if got := ImageStatePresent.String(); got != "present" {
		t.Errorf("present = %q", got)
	}
	if got := ImageStateAbsent.String(); got != "absent" {
		t.Errorf("absent = %q", got)
	}
	if got := ImageStateUnknown.String(); got != "unknown" {
		t.Errorf("unknown = %q", got)
	}
	// Confirm the shim file is reachable to silence shell-injection paranoia.
	_ = strings.ToLower("ok")
}
