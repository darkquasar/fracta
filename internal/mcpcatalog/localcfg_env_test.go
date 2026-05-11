package mcpcatalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAppendEnvExample_CreatesFile — when the file doesn't exist yet, the
// appender creates it with the header + KEY= lines.
func TestAppendEnvExample_CreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.example")
	if err := AppendEnvExample(path, "elastic", []string{"ES_URL", "ES_API_KEY"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	want := "# Required by elastic MCP server\nES_URL=\nES_API_KEY=\n"
	if got != want {
		t.Fatalf("output mismatch\n=== WANT ===\n%s\n=== GOT ===\n%s", want, got)
	}
}

// TestAppendEnvExample_Idempotent — calling twice with same vars is a no-op
// on the second call (header only appears once, no duplicate KEY= lines).
func TestAppendEnvExample_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.example")
	for i := 0; i < 3; i++ {
		if err := AppendEnvExample(path, "elastic", []string{"ES_URL", "ES_API_KEY"}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	if strings.Count(got, "# Required by elastic MCP server") != 1 {
		t.Errorf("header appears more than once:\n%s", got)
	}
	if strings.Count(got, "ES_URL=") != 1 {
		t.Errorf("ES_URL= appears more than once:\n%s", got)
	}
	if strings.Count(got, "ES_API_KEY=") != 1 {
		t.Errorf("ES_API_KEY= appears more than once:\n%s", got)
	}
}

// TestAppendEnvExample_PreservesExistingValues — existing KEY=value lines are
// left exactly as the operator wrote them; we never overwrite values.
func TestAppendEnvExample_PreservesExistingValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.example")
	existing := "ES_URL=https://operator-set-value.example.com\n"
	if err := os.WriteFile(path, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}
	if err := AppendEnvExample(path, "elastic", []string{"ES_URL", "ES_API_KEY"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	if !strings.Contains(got, "ES_URL=https://operator-set-value.example.com") {
		t.Errorf("operator value was overwritten:\n%s", got)
	}
	if strings.Count(got, "ES_URL=") != 1 {
		t.Errorf("ES_URL appears more than once (overwrote operator value):\n%s", got)
	}
	if !strings.Contains(got, "ES_API_KEY=") {
		t.Errorf("ES_API_KEY not appended:\n%s", got)
	}
}

// TestAppendEnvExample_CommentedOutCountsAsPresent — `# KEY=value` lines are
// treated as already-present so we don't duplicate them.
func TestAppendEnvExample_CommentedOutCountsAsPresent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.example")
	existing := "# ES_URL=https://placeholder\n"
	if err := os.WriteFile(path, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}
	if err := AppendEnvExample(path, "elastic", []string{"ES_URL"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	if strings.Count(got, "ES_URL") != 1 {
		t.Errorf("commented ES_URL was duplicated (uncommented version added):\n%s", got)
	}
}

// TestAppendEnvExample_HeaderOncePerServer — running for two distinct servers
// adds two distinct header comments; a second run for the SAME server does
// not add a duplicate header.
func TestAppendEnvExample_HeaderOncePerServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.example")
	if err := AppendEnvExample(path, "elastic", []string{"ES_URL"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendEnvExample(path, "notion", []string{"NOTION_TOKEN"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendEnvExample(path, "elastic", []string{"ES_URL"}); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	got := string(data)
	if strings.Count(got, "# Required by elastic MCP server") != 1 {
		t.Errorf("elastic header appears more than once:\n%s", got)
	}
	if strings.Count(got, "# Required by notion MCP server") != 1 {
		t.Errorf("notion header appears more than once:\n%s", got)
	}
}

// TestAppendEnvExample_NewKeysAppendedToExistingFile — when ONE of the
// requested keys is new and the others are present, only the new key is
// appended.
func TestAppendEnvExample_NewKeysAppendedToExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.example")
	existing := "# Required by elastic MCP server\nES_URL=\n"
	if err := os.WriteFile(path, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}
	if err := AppendEnvExample(path, "elastic",
		[]string{"ES_URL", "ES_API_KEY"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	if strings.Count(got, "# Required by elastic MCP server") != 1 {
		t.Errorf("duplicate header:\n%s", got)
	}
	if !strings.Contains(got, "ES_URL=") {
		t.Errorf("ES_URL missing:\n%s", got)
	}
	if !strings.Contains(got, "ES_API_KEY=") {
		t.Errorf("ES_API_KEY not appended:\n%s", got)
	}
}

// TestAppendEnvExample_TrailingNewlineHandling — a file without a trailing
// newline still gets the new block on a fresh line.
func TestAppendEnvExample_TrailingNewlineHandling(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.example")
	if err := os.WriteFile(path, []byte("FOO=bar"), 0644); err != nil { // no trailing \n
		t.Fatal(err)
	}
	if err := AppendEnvExample(path, "elastic", []string{"ES_URL"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	// First line still intact; new content on subsequent lines.
	lines := strings.Split(got, "\n")
	if lines[0] != "FOO=bar" {
		t.Errorf("first line corrupted: %q\nfull:\n%s", lines[0], got)
	}
	if !strings.Contains(got, "ES_URL=") {
		t.Errorf("ES_URL not appended:\n%s", got)
	}
}

// TestAppendEnvExample_EmptyVarsNoOp — empty vars slice doesn't create or
// modify the file at all (no spurious empty header).
func TestAppendEnvExample_EmptyVarsNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.example")
	if err := AppendEnvExample(path, "elastic", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should not exist for empty vars; stat err: %v", err)
	}
}
