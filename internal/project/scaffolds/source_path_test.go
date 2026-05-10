package scaffolds

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPathSource_Valid: a tmp tree with the requested kind subdir resolves
// cleanly. Both FS() (rebased) and RootFS() (un-rebased) return the expected
// shape.
func TestPathSource_Valid(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "k8s"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "k8s", "f.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	src, err := PathSource(dir, KindK8s)
	if err != nil {
		t.Fatalf("PathSource: %v", err)
	}
	defer src.Close()

	rebased, err := src.FS()
	if err != nil {
		t.Fatalf("FS: %v", err)
	}
	data, err := fs.ReadFile(rebased, "f.txt")
	if err != nil {
		t.Fatalf("ReadFile rebased: %v", err)
	}
	if string(data) != "hi" {
		t.Errorf("rebased f.txt = %q, want hi", data)
	}

	root, err := src.RootFS()
	if err != nil {
		t.Fatalf("RootFS: %v", err)
	}
	if _, err := fs.Stat(root, "k8s"); err != nil {
		t.Errorf("RootFS root missing 'k8s' dir: %v", err)
	}

	if !strings.HasPrefix(src.Description(), "local:") {
		t.Errorf("Description = %q, want prefix local:", src.Description())
	}
}

// TestPathSource_MissingKindSubdir: error names the kind and lists what was
// found at the path (helpful operator-facing diagnostic).
func TestPathSource_MissingKindSubdir(t *testing.T) {
	dir := t.TempDir()
	// Populate with a sibling so the diagnostic has something to list.
	_ = os.MkdirAll(filepath.Join(dir, "local"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "docker-compose"), 0o755)

	_, err := PathSource(dir, KindK8s)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "k8s") {
		t.Errorf("error missing kind name: %s", msg)
	}
	for _, want := range []string{"local", "docker-compose"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing sibling listing %q: %s", want, msg)
		}
	}
}

// TestPathSource_FileWhereDirExpected: someone created `k8s` as a regular
// file. Error must say the path isn't a directory.
func TestPathSource_FileWhereDirExpected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "k8s"), []byte("oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := PathSource(dir, KindK8s)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error = %q, want 'not a directory'", err)
	}
}
