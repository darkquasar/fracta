package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCheckpointRules_MissingFile(t *testing.T) {
	rules, err := LoadCheckpointRules(os.DirFS(t.TempDir()), ".")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("expected empty slice, got %d rules", len(rules))
	}
}

func TestLoadCheckpointRules_ValidFile(t *testing.T) {
	dir := t.TempDir()
	yaml := `rules:
  - name: test_rule
    layer: universal
    severity: error
    query: |
      MATCH (n:Foo) WHERE NOT (n)-[:BAR]->() RETURN n.name AS name
    gap_template:
      type: missing_bar
      description: "Node '{name}' has no BAR relationship"
      suggested_action: "Add BAR edge from '{name}'"
  - name: warn_rule
    layer: particular
    severity: warning
    query: "MATCH (h:Hunt) WHERE NOT (h)-[]->() RETURN h.name AS name"
    gap_template:
      type: empty_hunt
      description: "Hunt '{name}' is empty"
      suggested_action: "Link entities to hunt"
`
	if err := os.WriteFile(filepath.Join(dir, "checkpoint.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write checkpoint.yaml: %v", err)
	}

	rules, err := LoadCheckpointRules(os.DirFS(dir), ".")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	r := rules[0]
	if r.Name != "test_rule" {
		t.Errorf("Name = %q, want 'test_rule'", r.Name)
	}
	if r.Layer != "universal" {
		t.Errorf("Layer = %q, want 'universal'", r.Layer)
	}
	if r.Severity != "error" {
		t.Errorf("Severity = %q, want 'error'", r.Severity)
	}
	if !strings.Contains(r.Query, "MATCH (n:Foo)") {
		t.Errorf("Query doesn't contain expected Cypher: %q", r.Query)
	}
	if r.GapType != "missing_bar" {
		t.Errorf("GapType = %q, want 'missing_bar'", r.GapType)
	}
	if !strings.Contains(r.GapDescription, "{name}") {
		t.Errorf("GapDescription should contain template placeholder: %q", r.GapDescription)
	}
	if !strings.Contains(r.SuggestedAction, "{name}") {
		t.Errorf("SuggestedAction should contain template placeholder: %q", r.SuggestedAction)
	}

	r2 := rules[1]
	if r2.Name != "warn_rule" {
		t.Errorf("rule[1].Name = %q, want 'warn_rule'", r2.Name)
	}
	if r2.Layer != "particular" {
		t.Errorf("rule[1].Layer = %q, want 'particular'", r2.Layer)
	}
	if r2.Severity != "warning" {
		t.Errorf("rule[1].Severity = %q, want 'warning'", r2.Severity)
	}
}

func TestLoadCheckpointRules_EmptyName(t *testing.T) {
	dir := t.TempDir()
	yaml := `rules:
  - name: ""
    layer: universal
    severity: error
    query: "MATCH (n) RETURN n"
    gap_template:
      type: test
      description: test
      suggested_action: test
`
	if err := os.WriteFile(filepath.Join(dir, "checkpoint.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write checkpoint.yaml: %v", err)
	}

	_, err := LoadCheckpointRules(os.DirFS(dir), ".")
	if err == nil || !strings.Contains(err.Error(), "empty name") {
		t.Errorf("expected empty name error, got: %v", err)
	}
}

func TestLoadCheckpointRules_InvalidLayer(t *testing.T) {
	dir := t.TempDir()
	yaml := `rules:
  - name: bad_layer
    layer: magical
    severity: error
    query: "MATCH (n) RETURN n"
    gap_template:
      type: test
      description: test
      suggested_action: test
`
	if err := os.WriteFile(filepath.Join(dir, "checkpoint.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write checkpoint.yaml: %v", err)
	}

	_, err := LoadCheckpointRules(os.DirFS(dir), ".")
	if err == nil || !strings.Contains(err.Error(), "invalid layer") {
		t.Errorf("expected invalid layer error, got: %v", err)
	}
}

func TestLoadCheckpointRules_InvalidSeverity(t *testing.T) {
	dir := t.TempDir()
	yaml := `rules:
  - name: bad_severity
    layer: universal
    severity: critical
    query: "MATCH (n) RETURN n"
    gap_template:
      type: test
      description: test
      suggested_action: test
`
	if err := os.WriteFile(filepath.Join(dir, "checkpoint.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write checkpoint.yaml: %v", err)
	}

	_, err := LoadCheckpointRules(os.DirFS(dir), ".")
	if err == nil || !strings.Contains(err.Error(), "invalid severity") {
		t.Errorf("expected invalid severity error, got: %v", err)
	}
}

func TestLoadCheckpointRules_EmptyQuery(t *testing.T) {
	dir := t.TempDir()
	yaml := `rules:
  - name: no_query
    layer: universal
    severity: error
    query: ""
    gap_template:
      type: test
      description: test
      suggested_action: test
`
	if err := os.WriteFile(filepath.Join(dir, "checkpoint.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write checkpoint.yaml: %v", err)
	}

	_, err := LoadCheckpointRules(os.DirFS(dir), ".")
	if err == nil || !strings.Contains(err.Error(), "empty query") {
		t.Errorf("expected empty query error, got: %v", err)
	}
}

func TestLoadCheckpointRules_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "checkpoint.yaml"), []byte("{{invalid yaml"), 0o644)

	_, err := LoadCheckpointRules(os.DirFS(dir), ".")
	if err == nil || !strings.Contains(err.Error(), "parsing checkpoint.yaml") {
		t.Errorf("expected parse error, got: %v", err)
	}
}
