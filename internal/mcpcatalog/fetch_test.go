package mcpcatalog

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkquasar/fracta/internal/project/scaffolds"
)

// makeFixtureTarball builds a gzipped tar in memory containing the testdata
// catalog under a "mcp-servers/" root. topDir is the inserted top-level
// directory name (codeload tarballs always have one).
func makeFixtureTarball(t *testing.T, topDir string) []byte {
	t.Helper()
	files := map[string]string{
		"mcp-servers/catalog.yaml":            mustReadFile(t, "testdata/catalog/catalog.yaml"),
		"mcp-servers/elastic/server.yaml":     mustReadFile(t, "testdata/catalog/elastic/server.yaml"),
		"mcp-servers/notion/server.yaml":      mustReadFile(t, "testdata/catalog/notion/server.yaml"),
		"mcp-servers/vendor/server.yaml":      mustReadFile(t, "testdata/catalog/vendor/server.yaml"),
		"mcp-servers/ghcr-fracta/server.yaml": mustReadFile(t, "testdata/catalog/ghcr-fracta/server.yaml"),
	}
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	dirs := map[string]bool{topDir + "/": true}
	for path := range files {
		parts := strings.Split(path, "/")
		for i := 1; i < len(parts); i++ {
			dirs[topDir+"/"+strings.Join(parts[:i], "/")+"/"] = true
		}
	}
	for d := range dirs {
		if err := tw.WriteHeader(&tar.Header{Name: d, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range files {
		full := topDir + "/" + path
		if err := tw.WriteHeader(&tar.Header{
			Name: full, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFetch_PlainFromLocalDir(t *testing.T) {
	root := t.TempDir()
	srcDir := t.TempDir()
	copyDir(t, "testdata/catalog", filepath.Join(srcDir, "mcp-servers"))

	res, err := Fetch(context.Background(), root, FetchOpts{Source: srcDir})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.CatalogVersion != "2" {
		t.Errorf("CatalogVersion = %q, want 2", res.CatalogVersion)
	}
	for _, want := range []string{"catalog.yaml", "elastic/server.yaml", "notion/server.yaml", "vendor/server.yaml", ".fracta-source", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(root, "mcp-servers", want)); err != nil {
			t.Errorf("missing %s: %v", want, err)
		}
	}
	recorded, _ := ReadFractaSource(root)
	if recorded != srcDir {
		t.Errorf(".fracta-source = %q, want %q", recorded, srcDir)
	}
	gitignore, _ := os.ReadFile(filepath.Join(root, "mcp-servers", ".gitignore"))
	if !strings.Contains(string(gitignore), ".staging/") {
		t.Errorf(".gitignore missing .staging/: %s", gitignore)
	}
	// No leftover .staging directory.
	if _, err := os.Stat(filepath.Join(root, "mcp-servers", ".staging")); !os.IsNotExist(err) {
		t.Errorf(".staging still exists after fetch")
	}
}

func TestFetch_EmptySourceErrors(t *testing.T) {
	_, err := Fetch(context.Background(), t.TempDir(), FetchOpts{Source: ""})
	if !errors.Is(err, ErrEmptyFetchSource) {
		t.Errorf("err = %v, want ErrEmptyFetchSource", err)
	}
}

func TestResolveFetchSource_Precedence(t *testing.T) {
	root := t.TempDir()
	// 1. explicit wins.
	got, _ := ResolveFetchSource(root, "github:custom/repo@x")
	if got != "github:custom/repo@x" {
		t.Errorf("explicit precedence: got %q", got)
	}
	// 2. .fracta-source wins over default.
	if err := os.MkdirAll(filepath.Join(root, "mcp-servers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mcp-servers", ".fracta-source"), []byte("github:recorded/repo@y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ = ResolveFetchSource(root, "")
	if got != "github:recorded/repo@y" {
		t.Errorf(".fracta-source precedence: got %q", got)
	}
	// 3. default when both absent.
	root2 := t.TempDir()
	got, _ = ResolveFetchSource(root2, "")
	if got != DefaultFetchSourceSpec {
		t.Errorf("default precedence: got %q", got)
	}
}

func TestFetch_HttpsTarball(t *testing.T) {
	tarball := makeFixtureTarball(t, "fracta-main")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	}))
	defer srv.Close()
	// HttpsSource requires real HTTPS — use the test server's client.
	// The httpsSource downloadTarball uses http.DefaultClient, so we can't
	// directly point at httptest.TLS without skipping verification. Test
	// the github-codeload path instead (uses githubBaseURL override).
	t.Skip("HTTPS tarball path requires DefaultClient override; see TestFetch_GithubCodeload")
}

func TestFetch_GithubCodeload(t *testing.T) {
	tarball := makeFixtureTarball(t, "fracta-abc1234")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/darkquasar/fracta/tar.gz/abc1234" {
			http.Error(w, "not found", 404)
			return
		}
		_, _ = w.Write(tarball)
	}))
	defer srv.Close()

	// Override codeload base for the scaffolds package.
	saveGithubBase(t, srv.URL)

	root := t.TempDir()
	res, err := Fetch(context.Background(), root, FetchOpts{Source: "github:darkquasar/fracta@abc1234"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.CatalogVersion != "2" {
		t.Errorf("CatalogVersion = %q", res.CatalogVersion)
	}
	if len(res.RemoteCatalog.Entries) != 4 {
		t.Errorf("entries = %d, want 4", len(res.RemoteCatalog.Entries))
	}
}

func TestFetch_SourceChecksumIgnoredOnGithub(t *testing.T) {
	tarball := makeFixtureTarball(t, "fracta-abc1234")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	}))
	defer srv.Close()
	saveGithubBase(t, srv.URL)

	root := t.TempDir()
	_, err := Fetch(context.Background(), root, FetchOpts{
		Source:         "github:darkquasar/fracta@abc1234",
		SourceChecksum: "sha256:abc",
	})
	if err != nil {
		t.Fatalf("Fetch should silently ignore checksum on github source: %v", err)
	}
}

func TestFetch_SourceChecksumIgnoredOnLocalPath(t *testing.T) {
	srcDir := t.TempDir()
	copyDir(t, "testdata/catalog", filepath.Join(srcDir, "mcp-servers"))
	root := t.TempDir()
	_, err := Fetch(context.Background(), root, FetchOpts{
		Source:         srcDir,
		SourceChecksum: "sha256:abc",
	})
	if err != nil {
		t.Fatalf("Fetch should silently ignore checksum on local path: %v", err)
	}
}

func TestFetch_MergePreservesLocalOnly(t *testing.T) {
	root := t.TempDir()
	// Seed local catalog with a base entry not in remote.
	catRoot := filepath.Join(root, "mcp-servers")
	copyDir(t, "testdata/catalog", catRoot)
	// Add an extra local-only server: acme.
	if err := os.MkdirAll(filepath.Join(catRoot, "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(catRoot, "acme", "server.yaml"), `id: acme
name: Acme
category: org
status: tested
description: Org-private.
upstream: {type: first-party, url: https://acme}
auth: {modes: [env_token]}
variants:
  container:
    image: acme/x:latest
    transport: streamable-http
    service_url: http://acme-mcp.fracta.svc:3000/mcp
support: {local_process: not_supported, docker_compose: supported, kubernetes: supported}
`)
	// Rewrite catalog.yaml to include acme.
	mustWrite(t, filepath.Join(catRoot, "catalog.yaml"), `version: 1
description: Local catalog with acme.
servers:
  - id: elastic
    path: elastic/server.yaml
  - id: notion
    path: notion/server.yaml
  - id: vendor
    path: vendor/server.yaml
  - id: acme
    path: acme/server.yaml
`)
	// Record an existing .fracta-source — should NOT be overwritten by merge.
	mustWrite(t, filepath.Join(catRoot, ".fracta-source"), "github:darkquasar/fracta@main\n")

	// Now fetch from a remote that only has 3 entries (no acme) with --merge.
	srcDir := t.TempDir()
	copyDir(t, "testdata/catalog", filepath.Join(srcDir, "mcp-servers"))

	_, err := Fetch(context.Background(), root, FetchOpts{Source: srcDir, Merge: true})
	if err != nil {
		t.Fatalf("Fetch --merge: %v", err)
	}
	// acme should still exist.
	if _, err := os.Stat(filepath.Join(catRoot, "acme", "server.yaml")); err != nil {
		t.Errorf("acme/server.yaml should be preserved on merge: %v", err)
	}
	// .fracta-source should NOT have changed.
	rec, _ := ReadFractaSource(root)
	if rec != "github:darkquasar/fracta@main" {
		t.Errorf(".fracta-source changed on merge: got %q", rec)
	}
}

func TestFetch_BogusSourceCleansStaging(t *testing.T) {
	root := t.TempDir()
	_, err := Fetch(context.Background(), root, FetchOpts{Source: "/nonexistent/dir"})
	if err == nil {
		t.Fatalf("expected error for nonexistent path")
	}
	// .staging cleaned up.
	if _, err := os.Stat(filepath.Join(root, "mcp-servers", ".staging")); !os.IsNotExist(err) {
		t.Errorf(".staging should be cleaned up after failed fetch")
	}
}

func TestFetch_GithubTreeURLRejected(t *testing.T) {
	root := t.TempDir()
	_, err := Fetch(context.Background(), root, FetchOpts{Source: "https://github.com/owner/repo/tree/main"})
	if err == nil {
		t.Fatalf("expected error for /tree/ URL")
	}
	if !strings.Contains(err.Error(), "browser URL") {
		t.Errorf("err missing 'browser URL' redirect: %v", err)
	}
}

func TestFetch_GithubBlobURLRejected(t *testing.T) {
	root := t.TempDir()
	_, err := Fetch(context.Background(), root, FetchOpts{Source: "https://github.com/owner/repo/blob/main/README.md"})
	if err == nil {
		t.Fatalf("expected error for /blob/ URL")
	}
}

func TestFetch_SSHFormGithub(t *testing.T) {
	tarball := makeFixtureTarball(t, "fracta-abc1234")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	}))
	defer srv.Close()
	saveGithubBase(t, srv.URL)

	root := t.TempDir()
	_, err := Fetch(context.Background(), root, FetchOpts{Source: "git@github.com:darkquasar/fracta.git@abc1234"})
	if err != nil {
		t.Fatalf("SSH form fetch should work: %v", err)
	}
	rec, _ := ReadFractaSource(root)
	if rec != "git@github.com:darkquasar/fracta.git@abc1234" {
		t.Errorf(".fracta-source = %q", rec)
	}
}

func TestFetch_SSHFormNonGithubRejected(t *testing.T) {
	root := t.TempDir()
	_, err := Fetch(context.Background(), root, FetchOpts{Source: "git@gitlab.com:owner/repo"})
	if err == nil {
		t.Fatalf("expected error for gitlab SSH")
	}
	if !strings.Contains(err.Error(), "only git@github.com") {
		t.Errorf("err missing 'only git@github.com': %v", err)
	}
}

func TestFetch_TarballMissingMcpServers(t *testing.T) {
	// Build a tarball that does NOT contain mcp-servers/.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	dirs := []string{"fracta-x/", "fracta-x/local/"}
	for _, d := range dirs {
		_ = tw.WriteHeader(&tar.Header{Name: d, Typeflag: tar.TypeDir, Mode: 0o755})
	}
	body := "x: y\n"
	_ = tw.WriteHeader(&tar.Header{Name: "fracta-x/local/fracta.yaml", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
	_, _ = io.WriteString(tw, body)
	_ = tw.Close()
	_ = gw.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()
	saveGithubBase(t, srv.URL)

	root := t.TempDir()
	_, err := Fetch(context.Background(), root, FetchOpts{Source: "github:owner/repo@deadbeef"})
	if err == nil {
		t.Fatalf("expected error for tarball missing mcp-servers/")
	}
	// Note: scaffolds.GithubSource also validates that <kind>/ subdir
	// exists. With KindLocal pinned by the dispatcher, the kind check
	// happens first and produces the error.
	if !strings.Contains(err.Error(), "local") && !strings.Contains(err.Error(), "mcp-servers") {
		t.Errorf("err = %v; want kind or mcp-servers reference", err)
	}
}

func TestFetch_FilterApplied(t *testing.T) {
	srcDir := t.TempDir()
	copyDir(t, "testdata/catalog", filepath.Join(srcDir, "mcp-servers"))

	root := t.TempDir()
	f, err := ParseFilter("status=tested")
	if err != nil {
		t.Fatalf("ParseFilter: %v", err)
	}
	_, err = Fetch(context.Background(), root, FetchOpts{Source: srcDir, Filter: f})
	if err != nil {
		t.Fatalf("Fetch with filter: %v", err)
	}
	// elastic and vendor are tested; notion is documented and should NOT
	// be present in the staged tree.
	for _, want := range []string{"elastic", "vendor"} {
		if _, err := os.Stat(filepath.Join(root, "mcp-servers", want, "server.yaml")); err != nil {
			t.Errorf("expected %s/server.yaml: %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "mcp-servers", "notion", "server.yaml")); err == nil {
		t.Errorf("notion should have been filtered out")
	}
}

// saveGithubBase swaps the scaffolds-package codeload URL for the duration
// of the test. Uses scaffolds.SetGithubBaseURLForTest — a test-only seam
// kept in production source so cross-package tests can drive the codeload
// host without copy-pasting the variable.
func saveGithubBase(t *testing.T, url string) {
	t.Helper()
	prev := scaffolds.SetGithubBaseURLForTest(url)
	t.Cleanup(func() { scaffolds.SetGithubBaseURLForTest(prev) })
}
