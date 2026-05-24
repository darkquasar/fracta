package scaffolds

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"
)

// fakeSource wraps a fs.FS to implement Source for tests without touching the
// embedded FS or network code. Walker tests treat the supplied FS as both the
// rebased "kind" tree (FS) and the un-rebased root (RootFS); spec-43-style
// sibling-tree lookups have their own coverage in source_test.go.
type fakeSource struct {
	root fs.FS
	desc string
}

func (f *fakeSource) FS() (fs.FS, error)     { return f.root, nil }
func (f *fakeSource) RootFS() (fs.FS, error) { return f.root, nil }
func (f *fakeSource) Description() string    { return f.desc }
func (f *fakeSource) Close() error           { return nil }

func mkFS(files map[string]*fstest.MapFile) Source {
	return &fakeSource{root: fstest.MapFS(files), desc: "fake"}
}

func TestApply_Materializes(t *testing.T) {
	src := mkFS(map[string]*fstest.MapFile{
		"fracta.yaml":                {Data: []byte("runtime: local"), Mode: 0o644},
		"fracta/configs/x.yaml":      {Data: []byte("a"), Mode: 0o644},
		".fracta/.gitkeep":           {Data: []byte{}, Mode: 0o644},              // dropped by walker
		"fracta/auth-helpers/foo.sh": {Data: []byte("#!/bin/sh\n"), Mode: 0o644}, // forced 0755
	})

	dest := t.TempDir()
	res, err := Apply(context.Background(), src, dest, ApplyOpts{OnConflict: ConflictFail})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	want := map[string]bool{
		"fracta.yaml":                true,
		"fracta/configs/x.yaml":      true,
		"fracta/auth-helpers/foo.sh": true,
	}
	if len(res.Written) != len(want) {
		t.Errorf("Written = %v, want exactly %d entries (%v)", res.Written, len(want), want)
	}
	for _, p := range res.Written {
		if !want[p] {
			t.Errorf("Written includes unexpected %q", p)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, ".fracta", ".gitkeep")); !os.IsNotExist(err) {
		t.Errorf(".gitkeep should NOT have been written; stat err = %v", err)
	}
}

func TestApply_ConflictFail(t *testing.T) {
	src := mkFS(map[string]*fstest.MapFile{
		"a.txt": {Data: []byte("new"), Mode: 0o644},
	})
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "a.txt"), []byte("orig"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Apply(context.Background(), src, dest, ApplyOpts{OnConflict: ConflictFail})
	if err == nil {
		t.Fatalf("Apply: expected conflict error")
	}
	got, _ := os.ReadFile(filepath.Join(dest, "a.txt"))
	if string(got) != "orig" {
		t.Errorf("destination file modified despite conflict: got %q", got)
	}
}

func TestApply_ConflictSkipExisting(t *testing.T) {
	src := mkFS(map[string]*fstest.MapFile{
		"a.txt": {Data: []byte("new"), Mode: 0o644},
		"b.txt": {Data: []byte("fresh"), Mode: 0o644},
	})
	dest := t.TempDir()
	_ = os.WriteFile(filepath.Join(dest, "a.txt"), []byte("orig"), 0o644)
	res, err := Apply(context.Background(), src, dest, ApplyOpts{OnConflict: ConflictSkipExisting})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "a.txt" {
		t.Errorf("Skipped = %v, want [a.txt]", res.Skipped)
	}
	if len(res.Written) != 1 || res.Written[0] != "b.txt" {
		t.Errorf("Written = %v, want [b.txt]", res.Written)
	}
	got, _ := os.ReadFile(filepath.Join(dest, "a.txt"))
	if string(got) != "orig" {
		t.Errorf("a.txt was overwritten: got %q", got)
	}
}

func TestApply_ConflictOverwrite(t *testing.T) {
	src := mkFS(map[string]*fstest.MapFile{
		"a.txt": {Data: []byte("new"), Mode: 0o644},
	})
	dest := t.TempDir()
	_ = os.WriteFile(filepath.Join(dest, "a.txt"), []byte("orig"), 0o644)
	res, err := Apply(context.Background(), src, dest, ApplyOpts{OnConflict: ConflictOverwrite})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Written) != 1 || res.Written[0] != "a.txt" {
		t.Errorf("Written = %v, want [a.txt]", res.Written)
	}
	got, _ := os.ReadFile(filepath.Join(dest, "a.txt"))
	if string(got) != "new" {
		t.Errorf("a.txt content = %q, want %q", got, "new")
	}
}

func TestApply_ModeAuthHelpersAlways0755(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes not meaningful on Windows")
	}
	src := mkFS(map[string]*fstest.MapFile{
		// Helper reported 0644 by source — walker MUST force 0755.
		"fracta/auth-helpers/script.sh": {Data: []byte("x"), Mode: 0o644},
		// Embed-style zero-mode entry under auth-helpers — also forced 0755.
		".fracta/auth-helpers/embed-style.sh": {Data: []byte("y"), Mode: 0},
		// Outside auth-helpers, source mode is preserved.
		"fracta/configs/explicit-0640.yaml": {Data: []byte("z"), Mode: 0o640},
		// Outside auth-helpers, zero-mode falls to 0644.
		"fracta.yaml": {Data: []byte("y"), Mode: 0},
	})
	dest := t.TempDir()
	if _, err := Apply(context.Background(), src, dest, ApplyOpts{OnConflict: ConflictFail}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	cases := []struct {
		path string
		want os.FileMode
	}{
		{"fracta/auth-helpers/script.sh", 0o755},
		{".fracta/auth-helpers/embed-style.sh", 0o755},
		{"fracta/configs/explicit-0640.yaml", 0o640},
		{"fracta.yaml", 0o644},
	}
	for _, c := range cases {
		info, err := os.Stat(filepath.Join(dest, c.path))
		if err != nil {
			t.Errorf("stat %s: %v", c.path, err)
			continue
		}
		if got := info.Mode().Perm(); got != c.want {
			t.Errorf("%s mode = %#o, want %#o", c.path, got, c.want)
		}
	}
}

func TestApply_DryRun(t *testing.T) {
	src := mkFS(map[string]*fstest.MapFile{
		"a.txt":              {Data: []byte("a"), Mode: 0o644},
		"sub/b.txt":          {Data: []byte("b"), Mode: 0o644},
		"sub/auth-helpers/c": {Data: []byte("c"), Mode: 0o644},
	})
	dest := t.TempDir()
	res, err := Apply(context.Background(), src, dest, ApplyOpts{DryRun: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Written) != 3 {
		t.Errorf("Written = %v, want 3 entries", res.Written)
	}
	// Filesystem must remain pristine.
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("dest non-empty after DryRun: %v", entries)
	}
}

// Hostile-fixture R7 test: entries that would escape the destination tree
// must be refused before any write. Note: fstest.MapFS will not let us encode
// an absolute or "../" prefix path (it normalises), so we instead rely on a
// custom Source that yields raw entries. Simpler: assert the walker's guard
// helpers reject these directly.
func TestGuardPath_RejectsTraversal(t *testing.T) {
	cases := []string{
		"../escape",
		"foo/../../escape",
		"/abs/path",
	}
	for _, p := range cases {
		if err := guardPath(p); err == nil {
			t.Errorf("guardPath(%q): expected error, got nil", p)
		}
	}
}

func TestGuardPath_AllowsNormalPaths(t *testing.T) {
	for _, p := range []string{
		"a.txt",
		"sub/b.txt",
		"sub/dir/c",
	} {
		if err := guardPath(p); err != nil {
			t.Errorf("guardPath(%q): unexpected error %v", p, err)
		}
	}
}

// withinDest is the second-line defense: even if guardPath were bypassed,
// withinDest must catch a target that resolves outside dest.
func TestWithinDest(t *testing.T) {
	dest := "/abs/dest"
	cases := []struct {
		target string
		want   bool
	}{
		{"/abs/dest/a", true},
		{"/abs/dest/a/b", true},
		{"/abs/dest", true},
		{"/abs/elsewhere", false},
		{"/abs/dest_evil", false}, // sibling with prefix match
	}
	for _, c := range cases {
		got := withinDest(dest, c.target)
		if got != c.want {
			t.Errorf("withinDest(%q, %q) = %v, want %v", dest, c.target, got, c.want)
		}
	}
}
