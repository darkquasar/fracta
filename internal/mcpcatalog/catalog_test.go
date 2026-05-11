package mcpcatalog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCatalog_Fixture(t *testing.T) {
	cat, err := LoadCatalog(os.DirFS("testdata/catalog"))
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if cat.Version != "2" {
		t.Errorf("version = %q, want 2", cat.Version)
	}
	if len(cat.Entries) != 3 {
		t.Errorf("entries = %d, want 3", len(cat.Entries))
	}
	notion, ok := cat.Get("notion")
	if !ok {
		t.Fatalf("notion entry missing")
	}
	if len(notion.Auth.Modes) != 1 || notion.Auth.Modes[0] != "oauth" {
		t.Errorf("notion auth.modes = %v, want [oauth]", notion.Auth.Modes)
	}
	wantIDs := []string{"elastic", "notion", "vendor"}
	got := cat.IDs()
	if len(got) != len(wantIDs) {
		t.Fatalf("IDs() = %v, want %v", got, wantIDs)
	}
	for i := range wantIDs {
		if got[i] != wantIDs[i] {
			t.Errorf("IDs()[%d] = %q, want %q", i, got[i], wantIDs[i])
		}
	}
}

func TestLoadCatalog_VersionPreservesString(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "catalog.yaml"), "version: v2\nservers: []\n")
	cat, err := LoadCatalog(os.DirFS(dir))
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if cat.Version != "v2" {
		t.Errorf("version = %q, want v2", cat.Version)
	}
}

func TestLoadCatalog_MalformedEntry(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "catalog.yaml"), `version: 1
servers:
  - id: bad
    path: bad/server.yaml
`)
	if err := os.MkdirAll(filepath.Join(dir, "bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "bad", "server.yaml"), ":not yaml at all: :")
	_, err := LoadCatalog(os.DirFS(dir))
	if err == nil {
		t.Fatalf("expected error on malformed server.yaml")
	}
}

func TestLoadCatalog_MismatchedID(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "catalog.yaml"), `version: 1
servers:
  - id: foo
    path: foo/server.yaml
`)
	if err := os.MkdirAll(filepath.Join(dir, "foo"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "foo", "server.yaml"), "id: bar\nname: Bar\ncategory: x\nstatus: candidate\ndescription: x\nupstream: {type: vendor, url: https://x}\nauth: {modes: [none]}\nvariants: {local: {transport: stdio, command: x}}\nsupport: {local_process: supported, docker_compose: not_supported, kubernetes: not_supported}\n")
	_, err := LoadCatalog(os.DirFS(dir))
	if err == nil {
		t.Fatalf("expected error on id mismatch")
	}
}

func TestLoadCatalog_IgnoresFractaSource(t *testing.T) {
	// Copy fixture and add a .fracta-source; LoadCatalog must not error.
	src := "testdata/catalog"
	dir := t.TempDir()
	copyDir(t, src, dir)
	mustWrite(t, filepath.Join(dir, ".fracta-source"), "github:darkquasar/fracta@main\n")
	cat, err := LoadCatalog(os.DirFS(dir))
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if len(cat.Entries) != 3 {
		t.Errorf("entries = %d, want 3", len(cat.Entries))
	}
}

func TestLoadProjectCatalog_NoCatalog(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadProjectCatalog(dir)
	if !errors.Is(err, ErrNoCatalog) {
		t.Errorf("err = %v, want ErrNoCatalog", err)
	}
}

func TestLoadProjectCatalog_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "mcp-servers"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := LoadProjectCatalog(dir)
	if !errors.Is(err, ErrNoCatalog) {
		t.Errorf("err = %v, want ErrNoCatalog", err)
	}
}

func TestLoadProjectCatalog_Happy(t *testing.T) {
	dir := t.TempDir()
	catDir := filepath.Join(dir, "mcp-servers")
	copyDir(t, "testdata/catalog", catDir)
	cat, err := LoadProjectCatalog(dir)
	if err != nil {
		t.Fatalf("LoadProjectCatalog: %v", err)
	}
	if _, ok := cat.Get("elastic"); !ok {
		t.Errorf("elastic missing")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyDir(t, s, d)
			continue
		}
		b, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(d, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
