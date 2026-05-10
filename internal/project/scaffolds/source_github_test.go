package scaffolds

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// makeTarballGz builds a gzipped tar in memory. files maps path → content.
// All entries get mode 0644 unless overridden via authHelpers (forced 0755).
func makeTarballGz(t *testing.T, topDir string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	dirs := map[string]bool{topDir + "/": true}
	for path := range files {
		// Walk parents.
		parts := strings.Split(path, "/")
		for i := 1; i < len(parts); i++ {
			d := topDir + "/" + strings.Join(parts[:i], "/") + "/"
			dirs[d] = true
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
			Name:     full,
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     int64(len(content)),
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

func TestGithubSource_DownloadAndExtract(t *testing.T) {
	tar := makeTarballGz(t, "myrepo-abc1234", map[string]string{
		"local/fracta.yaml":    "runtime:\n  backend: local\n",
		"k8s/fracta.yaml":      "runtime:\n  backend: kubernetes\n",
		"k8s/manifests/ns.yaml": "kind: Namespace\n",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/owner/myrepo/tar.gz/abc1234" {
			http.Error(w, "not found", 404)
			return
		}
		_, _ = w.Write(tar)
	}))
	defer srv.Close()

	prevBase := githubBaseURL
	githubBaseURL = srv.URL
	defer func() { githubBaseURL = prevBase }()

	src, err := GithubSource(context.Background(), "github:owner/myrepo@abc1234", KindK8s)
	if err != nil {
		t.Fatalf("GithubSource: %v", err)
	}
	defer src.Close()

	rebased, err := src.FS()
	if err != nil {
		t.Fatalf("FS: %v", err)
	}
	data, err := fs.ReadFile(rebased, "fracta.yaml")
	if err != nil {
		t.Fatalf("ReadFile rebased fracta.yaml: %v", err)
	}
	if !strings.Contains(string(data), "kubernetes") {
		t.Errorf("rebased k8s/fracta.yaml does not mention 'kubernetes'; got: %s", data)
	}

	root, err := src.RootFS()
	if err != nil {
		t.Fatalf("RootFS: %v", err)
	}
	if _, err := fs.Stat(root, "k8s"); err != nil {
		t.Errorf("RootFS missing k8s/: %v", err)
	}
	if _, err := fs.Stat(root, "local"); err != nil {
		t.Errorf("RootFS missing sibling local/: %v", err)
	}

	if !strings.Contains(src.Description(), "github:owner/myrepo@abc1234") {
		t.Errorf("Description = %q", src.Description())
	}

	// Cleanup verification: closing must remove the tmpdir.
	gh := src.(*githubSource)
	tmp := gh.tmpdir
	if tmp == "" {
		t.Fatal("tmpdir empty before Close")
	}
	if err := src.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("tmpdir %s still exists after Close (err=%v)", tmp, err)
	}
}

func TestGithubSource_MissingKindSubdir(t *testing.T) {
	tar := makeTarballGz(t, "myrepo-deadbeef", map[string]string{
		"local/fracta.yaml":          "x",
		"docker-compose/fracta.yaml": "y",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tar)
	}))
	defer srv.Close()
	prev := githubBaseURL
	githubBaseURL = srv.URL
	defer func() { githubBaseURL = prev }()

	_, err := GithubSource(context.Background(), "github:owner/myrepo@deadbeef", KindK8s)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "k8s") {
		t.Errorf("error missing kind: %s", msg)
	}
	for _, want := range []string{"local", "docker-compose"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q: %s", want, msg)
		}
	}
}

// R8: oversize body returns the cap error.
func TestGithubSource_OversizeTarball(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Stream maxScaffoldTarball + 1 zero bytes — well under the LimitReader's
		// detection threshold but enough to trigger the cap.
		// Use a chunk to keep memory bounded.
		chunk := bytes.Repeat([]byte{0}, 1<<20) // 1 MB
		written := 0
		for written < maxScaffoldTarball+1 {
			n, err := w.Write(chunk)
			if err != nil {
				return
			}
			written += n
		}
	}))
	defer srv.Close()
	prev := githubBaseURL
	githubBaseURL = srv.URL
	defer func() { githubBaseURL = prev }()

	_, err := GithubSource(context.Background(), "github:owner/repo@cafebabe", KindLocal)
	if err == nil {
		t.Fatalf("expected size cap error, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds size limit") {
		t.Errorf("error = %q, want size limit error", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d", maxScaffoldTarball)) {
		t.Errorf("error = %q, want cap value in message", err)
	}
}

// Branch-style ref emits the reproducibility warning to stderr.
func TestGithubSource_BranchRefWarning(t *testing.T) {
	tar := makeTarballGz(t, "myrepo-main", map[string]string{
		"local/fracta.yaml": "x",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tar)
	}))
	defer srv.Close()
	prev := githubBaseURL
	githubBaseURL = srv.URL
	defer func() { githubBaseURL = prev }()

	// Capture stderr.
	r, w, _ := os.Pipe()
	prevStderr := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = prevStderr }()

	src, err := GithubSource(context.Background(), "github:owner/myrepo@main", KindLocal)
	if err != nil {
		t.Fatalf("GithubSource: %v", err)
	}
	_ = src.Close()
	_ = w.Close()
	captured, _ := io.ReadAll(r)
	if !strings.Contains(string(captured), "not a pinned commit SHA") {
		t.Errorf("stderr missing reproducibility warning; got: %s", captured)
	}
}

// SHA-looking refs do not emit the warning.
func TestGithubSource_SHARefSilent(t *testing.T) {
	tar := makeTarballGz(t, "myrepo-abc1234", map[string]string{
		"local/fracta.yaml": "x",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tar)
	}))
	defer srv.Close()
	prev := githubBaseURL
	githubBaseURL = srv.URL
	defer func() { githubBaseURL = prev }()

	r, w, _ := os.Pipe()
	prevStderr := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = prevStderr }()

	src, err := GithubSource(context.Background(), "github:owner/myrepo@abc1234", KindLocal)
	if err != nil {
		t.Fatalf("GithubSource: %v", err)
	}
	_ = src.Close()
	_ = w.Close()
	captured, _ := io.ReadAll(r)
	if strings.Contains(string(captured), "not a pinned commit SHA") {
		t.Errorf("SHA-looking ref unexpectedly emitted warning: %s", captured)
	}
}
