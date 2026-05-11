package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// catalogSourceDir builds an external "catalog source" directory laid out
// the way Fetch expects: <dir>/mcp-servers/{catalog.yaml, <id>/server.yaml}.
// Returns the absolute path operators would pass as the fetch source.
func catalogSourceDir(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	cat := `version: "` + version + `"
description: test catalog
servers:
  - id: elastic
    path: elastic/server.yaml
  - id: notion
    path: notion/server.yaml
`
	_, entries := minimalCatalog()
	writeFile(t, filepath.Join(dir, "mcp-servers", "catalog.yaml"), cat)
	for id, body := range entries {
		writeFile(t, filepath.Join(dir, "mcp-servers", id, "server.yaml"), body)
	}
	return dir
}

func resetFetchFlags() {
	fetchMergeFlag = false
	fetchFilterFlag = ""
	fetchSourceChecksumFlag = ""
	fetchYesFlag = false
}

func TestConfigMcpFetchEmptyProjectFromLocalSource(t *testing.T) {
	root := tempProject(t, tempProjectOpts{
		fractaYAML: "runtime:\n  backend: local\n",
	})
	srcDir := catalogSourceDir(t, "1")
	resetFetchFlags()
	fetchYesFlag = true

	if err := runConfigMcpFetch(configMcpFetchCmd, []string{srcDir}); err != nil {
		t.Fatalf("fetch: %v", err)
	}

	for _, want := range []string{
		"catalog.yaml",
		"elastic/server.yaml",
		"notion/server.yaml",
		".fracta-source",
	} {
		if _, err := os.Stat(filepath.Join(root, "mcp-servers", want)); err != nil {
			t.Errorf("missing %s: %v", want, err)
		}
	}

	got, _ := os.ReadFile(filepath.Join(root, "mcp-servers", ".fracta-source"))
	if !strings.Contains(string(got), srcDir) {
		t.Errorf(".fracta-source should record source path; got %q", got)
	}
}

func TestConfigMcpFetchMergeDoesNotUpdateFractaSource(t *testing.T) {
	root := tempProject(t, tempProjectOpts{
		fractaYAML: "runtime:\n  backend: local\n",
	})
	srcA := catalogSourceDir(t, "1")
	srcB := catalogSourceDir(t, "2")
	resetFetchFlags()
	fetchYesFlag = true

	// First fetch — records srcA.
	if err := runConfigMcpFetch(configMcpFetchCmd, []string{srcA}); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	beforeBytes, _ := os.ReadFile(filepath.Join(root, "mcp-servers", ".fracta-source"))

	// Merge fetch from srcB — must NOT update .fracta-source.
	resetFetchFlags()
	fetchYesFlag = true
	fetchMergeFlag = true
	if err := runConfigMcpFetch(configMcpFetchCmd, []string{srcB}); err != nil {
		t.Fatalf("merge fetch: %v", err)
	}
	afterBytes, _ := os.ReadFile(filepath.Join(root, "mcp-servers", ".fracta-source"))

	if string(beforeBytes) != string(afterBytes) {
		t.Errorf("--merge updated .fracta-source\nbefore=%q\nafter =%q", beforeBytes, afterBytes)
	}
}

func TestConfigMcpFetchRecordedSourceFallback(t *testing.T) {
	root := tempProject(t, tempProjectOpts{
		fractaYAML: "runtime:\n  backend: local\n",
	})
	srcDir := catalogSourceDir(t, "1")
	resetFetchFlags()
	fetchYesFlag = true

	// Seed an existing .fracta-source pointing at srcDir.
	if err := os.MkdirAll(filepath.Join(root, "mcp-servers"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "mcp-servers", ".fracta-source"), []byte(srcDir+"\n"), 0o644); err != nil {
		t.Fatalf("seed .fracta-source: %v", err)
	}

	// No positional argument — fetch should resolve to the seeded source.
	if err := runConfigMcpFetch(configMcpFetchCmd, nil); err != nil {
		t.Fatalf("fetch with recorded source: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "mcp-servers", "elastic", "server.yaml")); err != nil {
		t.Errorf("elastic entry not written via recorded source: %v", err)
	}
}

func TestConfigMcpFetchPreflightPrintsCounts(t *testing.T) {
	root := tempProject(t, tempProjectOpts{
		fractaYAML: "runtime:\n  backend: local\n",
	})
	srcDir := catalogSourceDir(t, "1")
	resetFetchFlags()
	fetchYesFlag = true

	if err := runConfigMcpFetch(configMcpFetchCmd, []string{srcDir}); err != nil {
		t.Fatalf("first fetch: %v", err)
	}

	resetFetchFlags()
	fetchYesFlag = true
	out, err := captureStdout(t, func() error {
		return runConfigMcpFetch(configMcpFetchCmd, []string{srcDir})
	})
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if !strings.Contains(out, "Local catalog state") {
		t.Errorf("preflight missing local state line:\n%s", out)
	}
	_ = root
}
