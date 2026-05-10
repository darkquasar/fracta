//go:build e2e

package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/darkquasar/fracta/internal/project/scaffolds"
)

// TestInit_E2E_DockerComposeParse: scaffolds the docker-compose tree, then
// invokes `docker compose config -f deployment/docker-compose.yml` to verify the
// emitted YAML parses cleanly. Excluded from default `go test` via the e2e
// build tag.
func TestInit_E2E_DockerComposeParse(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose plugin/daemon unavailable")
	}
	root := setupGitRepo(t)
	if _, err := Init(root, InitOpts{Scaffold: scaffolds.KindDockerCompose}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cmd := exec.Command("docker", "compose", "-f", "deployment/docker-compose.yml", "config")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("`docker compose config` failed: %v\noutput: %s", err, out)
	}
}

// TestInit_E2E_K8sDryRunApply: scaffolds the k8s tree, then invokes
// `kubectl --dry-run=client apply -f <each manifest>` to verify each parses.
// Skipped if kubectl is unavailable.
//
// Per-file invocation rather than -k is used because kustomize would require
// a kustomization.yaml the scaffold doesn't ship; spec-43 may add one.
func TestInit_E2E_K8sDryRunApply(t *testing.T) {
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl not installed")
	}
	root := setupGitRepo(t)
	if _, err := Init(root, InitOpts{Scaffold: scaffolds.KindK8s}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	manifestsDir := filepath.Join(root, "deployment", "k8s", "manifests")
	entries, err := os.ReadDir(manifestsDir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := filepath.Join(manifestsDir, e.Name())
		cmd := exec.Command("kubectl", "apply", "--dry-run=client", "-f", m)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("kubectl --dry-run=client -f %s failed: %v\noutput: %s", e.Name(), err, out)
		}
	}
}
