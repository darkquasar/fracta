package resolve

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_EmbedURI(t *testing.T) {
	r, err := Parse("embed://graph-schema/core")
	if err != nil {
		t.Fatalf("Parse embed://: %v", err)
	}
	if got, want := r.Source(), "embed://graph-schema/core"; got != want {
		t.Errorf("Source() = %q, want %q", got, want)
	}
	fsys, base, err := r.Open()
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	if base != "graph-schema/core" {
		t.Errorf("base = %q, want graph-schema/core", base)
	}
	if fsys == nil {
		t.Error("Open() returned nil fs.FS")
	}
}

func TestParse_EmbedURI_MultiSegment(t *testing.T) {
	r, err := Parse("embed://graph-schema/knowledge-garden")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, base, err := r.Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if base != "graph-schema/knowledge-garden" {
		t.Errorf("base = %q, want graph-schema/knowledge-garden", base)
	}
}

func TestParse_EmbedURI_Missing(t *testing.T) {
	r, err := Parse("embed://graph-schema/does-not-exist")
	if err != nil {
		t.Fatalf("Parse should accept malformed-but-syntactic URI: %v", err)
	}
	if _, _, err := r.Open(); err == nil {
		t.Error("Open should fail for missing embedded family")
	}
}

func TestParse_FileURI_Absolute(t *testing.T) {
	tmp := t.TempDir()
	r, err := Parse("file://" + tmp)
	if err != nil {
		t.Fatalf("Parse file:// %s: %v", tmp, err)
	}
	fsys, base, err := r.Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if base != "." {
		t.Errorf("base = %q, want .", base)
	}
	if fsys == nil {
		t.Error("Open() returned nil fs.FS")
	}
}

func TestParse_FileURI_NotADir(t *testing.T) {
	tmp := t.TempDir()
	// Pass a path to a file that won't exist as a directory.
	bogus := filepath.Join(tmp, "nonexistent")
	r, err := Parse("file://" + bogus)
	if err != nil {
		t.Fatalf("Parse should accept syntactically valid URI: %v", err)
	}
	if _, _, err := r.Open(); err == nil {
		t.Error("Open should fail for nonexistent path")
	}
}

func TestParse_BareString(t *testing.T) {
	_, err := Parse("graph-schema/core")
	if err == nil {
		t.Fatal("Parse should reject bare strings without a scheme")
	}
	if !strings.Contains(err.Error(), "no scheme") {
		t.Errorf("error should mention missing scheme; got: %v", err)
	}
	if !strings.Contains(err.Error(), "embed://") || !strings.Contains(err.Error(), "file:///") {
		t.Errorf("error should suggest both supported schemes; got: %v", err)
	}
}

func TestParse_EmptyString(t *testing.T) {
	_, err := Parse("")
	if err == nil {
		t.Fatal("Parse should reject empty string")
	}
}

func TestParse_UnknownScheme(t *testing.T) {
	_, err := Parse("s3://my-bucket/schemas/core")
	if err == nil {
		t.Fatal("Parse should reject unregistered scheme")
	}
	if !strings.Contains(err.Error(), "unknown") || !strings.Contains(err.Error(), "s3") {
		t.Errorf("error should name the unknown scheme; got: %v", err)
	}
}
