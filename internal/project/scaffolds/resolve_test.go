package scaffolds

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveSource_Empty: no spec → EmbeddedSource.
func TestResolveSource_Empty(t *testing.T) {
	src, err := ResolveSource(context.Background(), "", KindLocal, "")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	defer src.Close()
	if _, ok := src.(*embeddedSource); !ok {
		t.Errorf("got %T, want *embeddedSource", src)
	}
}

// TestResolveSource_Path: a real directory path → PathSource.
func TestResolveSource_Path(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "k8s"), 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := ResolveSource(context.Background(), dir, KindK8s, "")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	defer src.Close()
	if _, ok := src.(*pathSource); !ok {
		t.Errorf("got %T, want *pathSource", src)
	}
}

// TestResolveSource_Github: spec with github: prefix → GithubSource.
func TestResolveSource_Github(t *testing.T) {
	tarball := makeTarballGz(t, "myrepo-abc1234", map[string]string{
		"k8s/fracta.yaml": "x",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	}))
	defer srv.Close()
	prev := githubBaseURL
	githubBaseURL = srv.URL
	defer func() { githubBaseURL = prev }()

	src, err := ResolveSource(context.Background(), "github:owner/myrepo@abc1234", KindK8s, "")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	defer src.Close()
	if _, ok := src.(*githubSource); !ok {
		t.Errorf("got %T, want *githubSource", src)
	}
}

// TestResolveSource_Https: spec with https:// prefix → HttpsSource.
func TestResolveSource_Https(t *testing.T) {
	tarball := makeFlatTarballGz(t, map[string]string{"local/x.txt": "x"})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	}))
	defer srv.Close()
	prev := http.DefaultClient
	http.DefaultClient = srv.Client()
	defer func() { http.DefaultClient = prev }()

	src, err := ResolveSource(context.Background(), srv.URL+"/x.tar.gz", KindLocal, "")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	defer src.Close()
	if _, ok := src.(*httpsSource); !ok {
		t.Errorf("got %T, want *httpsSource", src)
	}
}

// TestResolveSource_PathBadDir: a path that doesn't exist surfaces a useful
// error.
func TestResolveSource_PathBadDir(t *testing.T) {
	_, err := ResolveSource(context.Background(), "/nonexistent/path/does/not/exist", KindK8s, "")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "k8s") {
		t.Errorf("error = %q, want it to mention 'k8s'", err)
	}
}
