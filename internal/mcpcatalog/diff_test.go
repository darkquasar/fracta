package mcpcatalog

import "testing"

func TestDiff_AllBuckets(t *testing.T) {
	local := &Catalog{Entries: map[string]*Entry{
		"a": {ID: "a", Status: "tested"},
		"b": {ID: "b", Status: "tested"},
		"c": {ID: "c", Status: "tested"},
	}}
	remote := &Catalog{Entries: map[string]*Entry{
		"b": {ID: "b", Status: "documented"}, // changed
		"c": {ID: "c", Status: "tested"},     // unchanged
		"d": {ID: "d", Status: "candidate"},  // added
	}}
	d := Diff(local, remote)
	if len(d.Added) != 1 || d.Added[0].ID != "d" {
		t.Errorf("Added = %+v", d.Added)
	}
	if len(d.Removed) != 1 || d.Removed[0].ID != "a" {
		t.Errorf("Removed = %+v", d.Removed)
	}
	if len(d.Changed) != 1 || d.Changed[0].Local.ID != "b" || d.Changed[0].Remote.Status != "documented" {
		t.Errorf("Changed = %+v", d.Changed)
	}
}

func TestDiff_Empty(t *testing.T) {
	d := Diff(nil, nil)
	if !d.IsEmpty() {
		t.Errorf("expected empty diff")
	}
}

func TestDiff_NilLocal(t *testing.T) {
	remote := &Catalog{Entries: map[string]*Entry{"x": {ID: "x"}}}
	d := Diff(nil, remote)
	if len(d.Added) != 1 || len(d.Removed) != 0 || len(d.Changed) != 0 {
		t.Errorf("nil local: Added=%v Removed=%v Changed=%v", d.Added, d.Removed, d.Changed)
	}
}

func TestDiff_NilRemote(t *testing.T) {
	local := &Catalog{Entries: map[string]*Entry{"x": {ID: "x"}}}
	d := Diff(local, nil)
	if len(d.Added) != 0 || len(d.Removed) != 1 {
		t.Errorf("nil remote: Added=%v Removed=%v", d.Added, d.Removed)
	}
}
