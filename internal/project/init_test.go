package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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

	if err := Init(root); err != nil {
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

	// .fracta/state.db should exist.
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

	if err := Init(root); err != nil {
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

	if err := Init(root); err != nil {
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
