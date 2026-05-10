package scaffolds

import (
	"io/fs"
	"testing"
)

// TestEmbeddedSource_FS confirms the embedded FS exposes the requested kind
// subtree. With only .gitkeep placeholders in place, FS().Open(".") must still
// return a usable directory; specific files are tested once C6/C7/C8 land
// real templates.
func TestEmbeddedSource_FS(t *testing.T) {
	for _, k := range AllKinds() {
		src := EmbeddedSource(k)
		f, err := src.FS()
		if err != nil {
			t.Errorf("EmbeddedSource(%v).FS: %v", k, err)
			continue
		}
		// Should at minimum contain the .gitkeep placeholder.
		entries, err := fs.ReadDir(f, ".")
		if err != nil {
			t.Errorf("ReadDir for %v: %v", k, err)
			continue
		}
		if len(entries) == 0 {
			t.Errorf("EmbeddedSource(%v).FS root is empty", k)
		}
		if err := src.Close(); err != nil {
			t.Errorf("Close for %v: %v", k, err)
		}
	}
}

func TestEmbeddedSource_Description(t *testing.T) {
	src := EmbeddedSource(KindLocal)
	if src.Description() != "embedded" {
		t.Errorf("Description = %q, want %q", src.Description(), "embedded")
	}
}

// TestEmbeddedSource_RootFS confirms RootFS() returns the un-rebased FS so
// spec-43 (and any future sibling-tree consumer) can fs.Sub() into directories
// outside templates/<kind>. The root MUST contain a "templates" entry and
// MUST NOT be the templates-rebased view.
func TestEmbeddedSource_RootFS(t *testing.T) {
	src := EmbeddedSource(KindLocal)
	root, err := src.RootFS()
	if err != nil {
		t.Fatalf("RootFS: %v", err)
	}
	entries, err := fs.ReadDir(root, ".")
	if err != nil {
		t.Fatalf("ReadDir root: %v", err)
	}
	var hasTemplates bool
	for _, e := range entries {
		if e.Name() == "templates" {
			hasTemplates = true
			break
		}
	}
	if !hasTemplates {
		t.Errorf("RootFS root entries = %v; expected to contain 'templates'", entries)
	}

	// And FS() must still produce the rebased view — i.e. its root is
	// templates/<kind>/, NOT a directory containing 'templates'.
	rebased, err := src.FS()
	if err != nil {
		t.Fatalf("FS: %v", err)
	}
	rebEntries, err := fs.ReadDir(rebased, ".")
	if err != nil {
		t.Fatalf("ReadDir rebased: %v", err)
	}
	for _, e := range rebEntries {
		if e.Name() == "templates" {
			t.Errorf("FS() must be rebased into templates/<kind>/, but root contains 'templates' itself")
		}
	}
}
