package mcpcatalog

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkquasar/fracta/internal/project/scaffolds"
)

// copyFixture copies a testdata fixture into a temp dir and returns the path.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("testdata", "fracta_yaml", name)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture %s: %v", src, err)
	}
	dst := filepath.Join(t.TempDir(), filepath.Base(name))
	if err := os.WriteFile(dst, data, 0644); err != nil {
		t.Fatalf("write temp fixture: %v", err)
	}
	return dst
}

// TestReadWriteRoundTrip — comment-rich fixture round-trips byte-identically.
// This is the read-then-write no-op case: NO normalization, NO mutation. It
// proves the yaml.v3 *yaml.Node pipeline preserves comments, key order, and
// indentation for an already-canonical input.
func TestReadWriteRoundTrip(t *testing.T) {
	path := copyFixture(t, "with_comments.yaml")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	root, err := ReadFractaYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFractaYAMLAtomic(path, root); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, after) {
		t.Fatalf("round-trip drift\n=== ORIGINAL ===\n%s\n=== AFTER ===\n%s", original, after)
	}
}

// TestUpsertIdempotent — upserting the same value twice is a no-op on the
// second call (byte-identical). This is the spec.md §4 R5 contract.
func TestUpsertIdempotent(t *testing.T) {
	path := copyFixture(t, "with_comments.yaml")

	rendered := []byte(`url: http://elastic-mcp.fracta.svc:3000/mcp
auth: env_token
`)

	apply := func() []byte {
		root, err := ReadFractaYAML(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := UpsertMCPServer(root, "elastic", scaffolds.KindK8s, rendered, true); err != nil {
			t.Fatal(err)
		}
		if err := WriteFractaYAMLAtomic(path, root); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	first := apply()
	second := apply()
	if !bytes.Equal(first, second) {
		t.Fatalf("upsert is not idempotent\n=== FIRST ===\n%s\n=== SECOND ===\n%s", first, second)
	}

	// Sanity check the value actually landed under the right keys.
	if !strings.Contains(string(first), "elastic:") || !strings.Contains(string(first), "remote:") {
		t.Fatalf("upsert produced unexpected output:\n%s", first)
	}
}

// TestUpsertWithoutForceErrors — second upsert without force=true returns an
// error and does NOT mutate the file.
func TestUpsertWithoutForceErrors(t *testing.T) {
	path := copyFixture(t, "with_comments.yaml")

	rendered := []byte(`url: http://elastic-mcp.fracta.svc:3000/mcp
`)

	// First upsert: succeeds.
	root, err := ReadFractaYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpsertMCPServer(root, "elastic", scaffolds.KindK8s, rendered, false); err != nil {
		t.Fatal(err)
	}
	if err := WriteFractaYAMLAtomic(path, root); err != nil {
		t.Fatal(err)
	}
	afterFirst, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Second upsert: force=false, entry exists → error, no mutation.
	root2, err := ReadFractaYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	err = UpsertMCPServer(root2, "elastic", scaffolds.KindK8s, rendered, false)
	if err == nil {
		t.Fatal("expected error for existing entry without --force")
	}
	if !strings.Contains(err.Error(), "already configured") {
		t.Fatalf("error doesn't mention 'already configured': %v", err)
	}

	afterSecond, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterFirst, afterSecond) {
		t.Fatalf("file mutated despite error\n=== BEFORE ===\n%s\n=== AFTER ===\n%s", afterFirst, afterSecond)
	}
}

// TestUpsertWithForcePreservesComments — with --force, replacing an entry
// preserves comments elsewhere in the file.
func TestUpsertWithForcePreservesComments(t *testing.T) {
	path := copyFixture(t, "with_comments.yaml")

	first := []byte(`url: http://old-url:3000/mcp
`)
	second := []byte(`url: http://new-url:3000/mcp
auth: env_token
`)

	root, err := ReadFractaYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpsertMCPServer(root, "elastic", scaffolds.KindK8s, first, false); err != nil {
		t.Fatal(err)
	}
	if err := WriteFractaYAMLAtomic(path, root); err != nil {
		t.Fatal(err)
	}

	root2, err := ReadFractaYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpsertMCPServer(root2, "elastic", scaffolds.KindK8s, second, true); err != nil {
		t.Fatal(err)
	}
	if err := WriteFractaYAMLAtomic(path, root2); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, want := range []string{
		"fracta.yaml — operator config",              // top-of-file comment
		"# Edited by hand; preserve these comments.", // second top comment
		"# one of: local, kubernetes",                // line comment on runtime.backend
		"http://new-url:3000/mcp",                    // new value
		"auth: env_token",                            // new field
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "http://old-url") {
		t.Errorf("old value not replaced:\n%s", out)
	}
}

// TestRemoveMCPServer — removing an entry leaves the rest of the file intact;
// removing a non-existent entry is a no-op (no error).
func TestRemoveMCPServer(t *testing.T) {
	path := copyFixture(t, "with_comments.yaml")
	rendered := []byte(`url: http://elastic-mcp.fracta.svc:3000/mcp
`)

	root, err := ReadFractaYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpsertMCPServer(root, "elastic", scaffolds.KindK8s, rendered, false); err != nil {
		t.Fatal(err)
	}
	if err := WriteFractaYAMLAtomic(path, root); err != nil {
		t.Fatal(err)
	}

	root2, err := ReadFractaYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveMCPServer(root2, "elastic", scaffolds.KindK8s); err != nil {
		t.Fatal(err)
	}
	if err := WriteFractaYAMLAtomic(path, root2); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "elastic-mcp") {
		t.Errorf("elastic entry not removed:\n%s", data)
	}

	// Idempotent: removing again is a no-op.
	root3, err := ReadFractaYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveMCPServer(root3, "elastic", scaffolds.KindK8s); err != nil {
		t.Fatalf("second RemoveMCPServer should be a no-op, got: %v", err)
	}

	// Removing a totally absent server is also a no-op.
	if err := RemoveMCPServer(root3, "ghost", scaffolds.KindK8s); err != nil {
		t.Fatalf("RemoveMCPServer for absent server should be a no-op, got: %v", err)
	}
}

// TestNormalizeFractaYAML_DateShapedScalars — issue 2. Date-shaped, version-
// shaped, and ambiguous-boolean scalars get explicit double quotes on first
// pass; second pass is a no-op.
func TestNormalizeFractaYAML_DateShapedScalars(t *testing.T) {
	path := copyFixture(t, "date_shaped_scalars.yaml")

	changed, err := NormalizeFractaYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true on first normalize")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join("testdata", "fracta_yaml", "normalized_after_first_edit.yaml")
	want, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("normalize output mismatch\n=== WANT ===\n%s\n=== GOT ===\n%s", want, got)
	}

	// Second pass is a no-op.
	changed2, err := NormalizeFractaYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if changed2 {
		t.Fatal("expected changed=false on second normalize (idempotent)")
	}
}

// TestNormalizeThenUpsertIdempotent — after normalize, two consecutive upserts
// on a date-shaped fixture produce byte-identical output. This eliminates the
// "modulo yaml.v3 normalization" hedge.
func TestNormalizeThenUpsertIdempotent(t *testing.T) {
	path := copyFixture(t, "date_shaped_scalars.yaml")
	if _, err := NormalizeFractaYAML(path); err != nil {
		t.Fatal(err)
	}
	rendered := []byte(`url: http://elastic-mcp.fracta.svc:3000/mcp
`)
	apply := func() []byte {
		root, err := ReadFractaYAML(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := UpsertMCPServer(root, "elastic", scaffolds.KindK8s, rendered, true); err != nil {
			t.Fatal(err)
		}
		if err := WriteFractaYAMLAtomic(path, root); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	first := apply()
	second := apply()
	if !bytes.Equal(first, second) {
		t.Fatalf("upsert after normalize is not idempotent\n=== FIRST ===\n%s\n=== SECOND ===\n%s", first, second)
	}
}

// TestNoBakFilesAfterMutation — after a single-file upsert+write, no .bak
// files exist in the project tree. spec.md §11 R2.
func TestNoBakFilesAfterMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fracta.yaml")
	src, err := os.ReadFile(filepath.Join("testdata", "fracta_yaml", "with_comments.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, src, 0644); err != nil {
		t.Fatal(err)
	}

	root, err := ReadFractaYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpsertMCPServer(root, "elastic", scaffolds.KindK8s,
		[]byte("url: http://x:3000/mcp\n"), true); err != nil {
		t.Fatal(err)
	}
	if err := WriteFractaYAMLAtomic(path, root); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".bak") {
			t.Errorf("unexpected .bak file: %s", e.Name())
		}
	}
}

// TestOrphanTempCleanup — an orphan ".fracta.yaml.tmp.XXX" left over from a
// simulated crash between fsync and rename is removed on the next mutation.
// This is the "kill between f.Sync and os.Rename" scenario in the acceptance
// criteria for H1.
func TestOrphanTempCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fracta.yaml")
	// Pre-populate the file (the "original intact" prerequisite).
	src, _ := os.ReadFile(filepath.Join("testdata", "fracta_yaml", "with_comments.yaml"))
	if err := os.WriteFile(path, src, 0644); err != nil {
		t.Fatal(err)
	}
	// Simulate the crash: drop an orphan temp that "almost made it".
	orphan := filepath.Join(dir, ".fracta.yaml.tmp.123456")
	if err := os.WriteFile(orphan, []byte("garbage"), 0644); err != nil {
		t.Fatal(err)
	}

	// Original file still intact (precondition).
	current, _ := os.ReadFile(path)
	if !bytes.Equal(current, src) {
		t.Fatalf("precondition: original file should be intact")
	}

	// Next mutation: should clean up the orphan and succeed.
	root, err := ReadFractaYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpsertMCPServer(root, "elastic", scaffolds.KindK8s,
		[]byte("url: http://x:3000/mcp\n"), true); err != nil {
		t.Fatal(err)
	}
	if err := WriteFractaYAMLAtomic(path, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan temp not cleaned up: %v", err)
	}
}

// TestUpsertMCPServer_ModeKeyMapping — local→"local"; docker-compose and k8s
// both → "remote".
func TestUpsertMCPServer_ModeKeyMapping(t *testing.T) {
	cases := []struct {
		kind scaffolds.Kind
		key  string
	}{
		{scaffolds.KindLocal, "local"},
		{scaffolds.KindDockerCompose, "remote"},
		{scaffolds.KindK8s, "remote"},
	}
	for _, tc := range cases {
		t.Run(tc.kind.String(), func(t *testing.T) {
			path := copyFixture(t, "with_comments.yaml")
			root, err := ReadFractaYAML(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := UpsertMCPServer(root, "elastic", tc.kind,
				[]byte("url: http://x\n"), false); err != nil {
				t.Fatal(err)
			}
			if err := WriteFractaYAMLAtomic(path, root); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(path)
			if !strings.Contains(string(data), "  "+tc.key+":") {
				t.Errorf("expected key %q in output for %s:\n%s", tc.key, tc.kind, data)
			}
		})
	}
}
