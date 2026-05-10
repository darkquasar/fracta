package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkquasar/fracta/internal/project/scaffolds"
	"gopkg.in/yaml.v3"
)

// setupGitRepo creates a temp directory and initializes a git repo in it.
func setupGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}
	return dir
}

func TestInit_WritesFractaYAML(t *testing.T) {
	root := setupGitRepo(t)

	if _, err := Init(root, InitOpts{Scaffold: scaffolds.KindLocal}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// fracta.yaml should exist.
	yamlPath := filepath.Join(root, "fracta.yaml")
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("fracta.yaml not found: %v", err)
	}

	// Parse and verify structure.
	var cfg map[string]interface{}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("fracta.yaml is not valid YAML: %v", err)
	}

	// Verify project section exists.
	if _, ok := cfg["project"]; !ok {
		t.Error("fracta.yaml missing 'project' section")
	}

	// Verify agents section exists.
	agents, ok := cfg["agents"].(map[string]interface{})
	if !ok {
		t.Error("fracta.yaml missing 'agents' section")
	}

	// Verify agent runtime config exists.
	if _, ok := agents["agent_runtimes"]; !ok {
		t.Error("fracta.yaml missing 'agents.agent_runtimes' section")
	}

	// Verify runtime section exists.
	if _, ok := cfg["runtime"]; !ok {
		t.Error("fracta.yaml missing 'runtime' section")
	}

	// .fracta/config.json should NOT exist.
	configJSONPath := filepath.Join(root, ".fracta", "config.json")
	if _, err := os.Stat(configJSONPath); !os.IsNotExist(err) {
		t.Error(".fracta/config.json should NOT be created by fracta init")
	}

	// .fracta/state.db should exist (KindLocal only).
	dbPath := filepath.Join(root, ".fracta", "state.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error(".fracta/state.db should be created")
	}

	// .gitignore should have entries.
	gitignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}
	if !strings.Contains(string(gitignore), ".fracta/") {
		t.Error(".gitignore missing .fracta/ entry")
	}
}

func TestInit_PreservesExistingFractaYAML(t *testing.T) {
	root := setupGitRepo(t)

	// Write a custom fracta.yaml before init.
	customContent := "project:\n  default_base_branch: develop\n"
	yamlPath := filepath.Join(root, "fracta.yaml")
	if err := os.WriteFile(yamlPath, []byte(customContent), 0644); err != nil {
		t.Fatal(err)
	}

	// SkipExisting policy must leave the existing file alone.
	if _, err := Init(root, InitOpts{
		Scaffold:   scaffolds.KindLocal,
		OnConflict: scaffolds.ConflictSkipExisting,
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// fracta.yaml should be unchanged.
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != customContent {
		t.Errorf("fracta.yaml was modified; got:\n%s\nwant:\n%s", string(data), customContent)
	}
}

func TestInit_FractaYAMLHasProjectDefaults(t *testing.T) {
	root := setupGitRepo(t)

	if _, err := Init(root, InitOpts{Scaffold: scaffolds.KindLocal}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "fracta.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	// Verify key defaults in the YAML content.
	content := string(data)
	checks := []struct {
		name   string
		substr string
	}{
		{"default_base_branch", "default_base_branch: main"},
		{"default_runtime", "default_runtime: claude"},
		{"default_mode", "default_mode: batch"},
		{"agent_runtimes", "agent_runtimes:"},
		{"agent_runtimes.claude.adapter", "adapter: claude"},
		{"runtime.backend", "backend: local"},
		{"allowed_tools has Read", "- Read"},
	}

	for _, c := range checks {
		if !strings.Contains(content, c.substr) {
			t.Errorf("fracta.yaml missing %s (%q)", c.name, c.substr)
		}
	}
}

// TestInit_ForceOverwrites: with ConflictOverwrite, an existing fracta.yaml
// is replaced by the scaffold version.
func TestInit_ForceOverwrites(t *testing.T) {
	root := setupGitRepo(t)
	yamlPath := filepath.Join(root, "fracta.yaml")
	if err := os.WriteFile(yamlPath, []byte("# stale\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(root, InitOpts{
		Scaffold:   scaffolds.KindLocal,
		OnConflict: scaffolds.ConflictOverwrite,
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	data, _ := os.ReadFile(yamlPath)
	if strings.TrimSpace(string(data)) == "# stale" {
		t.Errorf("fracta.yaml was not overwritten by --force; got: %s", data)
	}
	if !strings.Contains(string(data), "default_base_branch") {
		t.Errorf("fracta.yaml after --force missing scaffold content; got: %s", data)
	}
}

// TestInit_DockerComposeStructure: kind=docker-compose materializes the
// expected tree (no SQLite, has fracta/docker-compose.yml).
func TestInit_DockerComposeStructure(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed; skipping docker-compose scaffold test")
	}
	// Also skip if `docker compose version` fails (e.g. daemon unavailable
	// in CI). The prereq check is part of Init for docker-compose; this
	// test exercises file structure only, not prereqs.
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("`docker compose` plugin/daemon unavailable; skipping")
	}
	root := setupGitRepo(t)
	if _, err := Init(root, InitOpts{Scaffold: scaffolds.KindDockerCompose}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	for _, want := range []string{
		"fracta.yaml",
		"deployment/docker-compose.yml",
		"deployment/configs/controlplane.yaml",
		"deployment/configs/gateway.yaml",
		"deployment/auth-helpers/README.md",
		"deployment/auth-helpers/fetch-token-example",
	} {
		if _, err := os.Stat(filepath.Join(root, want)); err != nil {
			t.Errorf("missing scaffolded file %q: %v", want, err)
		}
	}

	// Compose mode must NOT create a SQLite db (state lives in postgres).
	if _, err := os.Stat(filepath.Join(root, ".fracta", "state.db")); !os.IsNotExist(err) {
		t.Error(".fracta/state.db should NOT exist for docker-compose scaffold")
	}

	// fetch-token-example must be executable (spec-42 §6).
	info, err := os.Stat(filepath.Join(root, "deployment/auth-helpers/fetch-token-example"))
	if err == nil {
		if mode := info.Mode().Perm(); mode != 0o755 {
			t.Errorf("fetch-token-example mode = %#o, want 0755", mode)
		}
	}
}

// TestInit_K8sStructure: kind=k8s materializes manifests + auth-helpers
// ConfigMap + sets runtime.backend=kubernetes.
func TestInit_K8sStructure(t *testing.T) {
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl not installed; skipping k8s scaffold test")
	}
	root := setupGitRepo(t)
	if _, err := Init(root, InitOpts{Scaffold: scaffolds.KindK8s}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	for _, want := range []string{
		"fracta.yaml",
		"deployment/k8s/manifests/namespace.yaml",
		"deployment/k8s/manifests/fracta-controlplane.yaml",
		"deployment/k8s/manifests/fracta-gateway.yaml",
		"deployment/k8s/manifests/auth-helpers-configmap.yaml",
		"deployment/auth-helpers/README.md",
		"deployment/auth-helpers/fetch-token-example",
	} {
		if _, err := os.Stat(filepath.Join(root, want)); err != nil {
			t.Errorf("missing scaffolded file %q: %v", want, err)
		}
	}

	// fracta.yaml must declare runtime.backend: kubernetes.
	data, err := os.ReadFile(filepath.Join(root, "fracta.yaml"))
	if err != nil {
		t.Fatalf("read fracta.yaml: %v", err)
	}
	if !strings.Contains(string(data), "backend: kubernetes") {
		t.Errorf("k8s fracta.yaml missing 'backend: kubernetes'")
	}
	if !strings.Contains(string(data), "extra_volumes") {
		t.Errorf("k8s fracta.yaml missing 'extra_volumes' block")
	}
}
