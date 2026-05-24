package scaffolds

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// makeFlatTarballGz: kind dir at archive root (httpsSource expectation).
// Distinct from makeTarballGz used by github tests (which wraps in <repo>-<ref>).
func makeFlatTarballGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	dirs := map[string]bool{}
	for path := range files {
		parts := strings.Split(path, "/")
		for i := 1; i < len(parts); i++ {
			dirs[strings.Join(parts[:i], "/")+"/"] = true
		}
	}
	for d := range dirs {
		_ = tw.WriteHeader(&tar.Header{Name: d, Typeflag: tar.TypeDir, Mode: 0o755})
	}
	for path, content := range files {
		_ = tw.WriteHeader(&tar.Header{
			Name: path, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content)),
		})
		_, _ = tw.Write([]byte(content))
	}
	_ = tw.Close()
	_ = gw.Close()
	return buf.Bytes()
}

func TestHttpsSource_DownloadAndExtract(t *testing.T) {
	tarball := makeFlatTarballGz(t, map[string]string{
		"k8s/fracta.yaml":   "kubernetes",
		"local/fracta.yaml": "local",
	})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	}))
	defer srv.Close()

	// httptest.NewTLSServer uses an unsigned cert; swap default client to
	// the test server's client which trusts that cert.
	prev := http.DefaultClient
	http.DefaultClient = srv.Client()
	defer func() { http.DefaultClient = prev }()

	src, err := HttpsSource(context.Background(), srv.URL+"/scaffold.tar.gz", KindK8s, "")
	if err != nil {
		t.Fatalf("HttpsSource: %v", err)
	}
	defer src.Close()

	rebased, err := src.FS()
	if err != nil {
		t.Fatalf("FS: %v", err)
	}
	data, err := fs.ReadFile(rebased, "fracta.yaml")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "kubernetes" {
		t.Errorf("rebased content = %q, want kubernetes", data)
	}

	root, err := src.RootFS()
	if err != nil {
		t.Fatalf("RootFS: %v", err)
	}
	if _, err := fs.Stat(root, "local"); err != nil {
		t.Errorf("RootFS missing sibling local/: %v", err)
	}
}

func TestHttpsSource_ChecksumMatch(t *testing.T) {
	tarball := makeFlatTarballGz(t, map[string]string{"local/x.txt": "x"})
	sum := sha256.Sum256(tarball)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	}))
	defer srv.Close()
	prev := http.DefaultClient
	http.DefaultClient = srv.Client()
	defer func() { http.DefaultClient = prev }()

	src, err := HttpsSource(context.Background(), srv.URL+"/x.tar.gz", KindLocal, "sha256:"+hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("HttpsSource: %v", err)
	}
	_ = src.Close()
}

func TestHttpsSource_ChecksumMismatch(t *testing.T) {
	tarball := makeFlatTarballGz(t, map[string]string{"local/x.txt": "x"})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	}))
	defer srv.Close()
	prev := http.DefaultClient
	http.DefaultClient = srv.Client()
	defer func() { http.DefaultClient = prev }()

	_, err := HttpsSource(context.Background(), srv.URL+"/x.tar.gz", KindLocal, "sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Fatalf("expected checksum mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error = %q", err)
	}
}

func TestHttpsSource_BadChecksumFormat(t *testing.T) {
	tarball := makeFlatTarballGz(t, map[string]string{"local/x.txt": "x"})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	}))
	defer srv.Close()
	prev := http.DefaultClient
	http.DefaultClient = srv.Client()
	defer func() { http.DefaultClient = prev }()

	_, err := HttpsSource(context.Background(), srv.URL+"/x.tar.gz", KindLocal, "md5:abc")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "sha256:") {
		t.Errorf("error = %q, want hint about sha256: prefix", err)
	}
}

func TestHttpsSource_OversizeBody(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := bytes.Repeat([]byte{0}, 1<<20)
		written := 0
		for written < maxScaffoldTarball+1 {
			n, _ := w.Write(chunk)
			if n == 0 {
				return
			}
			written += n
		}
	}))
	defer srv.Close()
	prev := http.DefaultClient
	http.DefaultClient = srv.Client()
	defer func() { http.DefaultClient = prev }()

	_, err := HttpsSource(context.Background(), srv.URL+"/big.tar.gz", KindLocal, "")
	if err == nil {
		t.Fatalf("expected size cap error, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds size limit") {
		t.Errorf("error = %q", err)
	}
}

func TestHttpsSource_RequiresHTTPS(t *testing.T) {
	_, err := HttpsSource(context.Background(), "http://example.com/x.tar.gz", KindLocal, "")
	if err == nil {
		t.Fatalf("expected scheme error, got nil")
	}
	if !strings.Contains(err.Error(), "https://") {
		t.Errorf("error = %q", err)
	}
}
