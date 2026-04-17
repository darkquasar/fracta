package assets

import (
	"testing"
	"testing/fstest"
)

func TestMustLoad(t *testing.T) {
	fsys := fstest.MapFS{
		"root/hello.md": {Data: []byte("# Hello\nWorld")},
	}
	s := New(fsys, "root")
	got := s.MustLoad("hello.md")
	if got != "# Hello\nWorld" {
		t.Errorf("MustLoad = %q, want %q", got, "# Hello\nWorld")
	}
}

func TestMustLoad_NoRoot(t *testing.T) {
	fsys := fstest.MapFS{
		"hello.md": {Data: []byte("direct")},
	}
	s := New(fsys, "")
	got := s.MustLoad("hello.md")
	if got != "direct" {
		t.Errorf("MustLoad = %q, want %q", got, "direct")
	}
}

func TestMustLoad_PanicsOnMissing(t *testing.T) {
	fsys := fstest.MapFS{}
	s := New(fsys, "root")
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for missing file")
		}
	}()
	s.MustLoad("nonexistent.md")
}

func TestMustRender(t *testing.T) {
	fsys := fstest.MapFS{
		"tmpl/greet.md.tmpl": {Data: []byte("Hello {{.Name}}, branch {{.Branch}}!")},
	}
	s := New(fsys, "tmpl")
	got := s.MustRender("greet.md.tmpl", map[string]string{
		"Name":   "agent-1",
		"Branch": "main",
	})
	if got != "Hello agent-1, branch main!" {
		t.Errorf("MustRender = %q", got)
	}
}

func TestMustRender_MissingKeyError(t *testing.T) {
	fsys := fstest.MapFS{
		"t.tmpl": {Data: []byte("{{.Missing}}")},
	}
	s := New(fsys, "")
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for missing template key")
		}
	}()
	s.MustRender("t.tmpl", map[string]string{"Other": "val"})
}

func TestMustRender_PanicsOnBadTemplate(t *testing.T) {
	fsys := fstest.MapFS{
		"bad.tmpl": {Data: []byte("{{.Unclosed")},
	}
	s := New(fsys, "")
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for bad template syntax")
		}
	}()
	s.MustRender("bad.tmpl", nil)
}
