package mcpcatalog

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func copyComposeFixture(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("testdata", "compose", name)
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

// TestComposeRoundTripPreservesVolumes — round-trip a compose file with a
// trailing `volumes:` block; the trailing content must survive.
func TestComposeRoundTripPreservesVolumes(t *testing.T) {
	path := copyComposeFixture(t, "baseline.yml")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	root, err := ReadComposeYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteComposeYAMLAtomic(path, root); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, after) {
		t.Fatalf("compose round-trip drift\n=== ORIGINAL ===\n%s\n=== AFTER ===\n%s", original, after)
	}
	if !strings.Contains(string(after), "volumes:") {
		t.Errorf("volumes: block lost on round-trip")
	}
}

// TestUpsertComposeServiceByteIdentical — the rendered elastic-mcp block,
// upserted into an empty services map, produces output containing the input
// block byte-for-byte (modulo the 2-space service-name indentation).
// This is the contract from spec.md §11 / H2 acceptance.
func TestUpsertComposeServiceByteIdentical(t *testing.T) {
	path := copyComposeFixture(t, "baseline.yml")

	serviceBlock, err := os.ReadFile(filepath.Join("testdata", "compose", "elastic-mcp-block.yml"))
	if err != nil {
		t.Fatal(err)
	}

	root, err := ReadComposeYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpsertComposeService(root, "elastic-mcp", serviceBlock, false); err != nil {
		t.Fatal(err)
	}
	if err := WriteComposeYAMLAtomic(path, root); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// The rendered block lines, indented under "  elastic-mcp:", must appear
	// verbatim in the output. We don't compare entire files because the
	// services-map boundary (where to place a new key inside a previously-empty
	// flow `{}` map) has multiple valid orderings re: surrounding keys; what
	// matters is the block content is preserved bit-for-bit.
	wantLines := []string{
		"  elastic-mcp:",
		"    image: docker.elastic.co/mcp/elasticsearch:latest",
		`    command: ["http", "--address", "0.0.0.0:8000", "--sse"]`,
		"    environment:",
		`      ES_URL: "${ELASTIC_URL}"`,
		`      ES_API_KEY: "${ELASTIC_API_KEY}"`,
		"    healthcheck:",
		`      test: ["CMD-SHELL", "python3 -c 'import urllib.request; urllib.request.urlopen(\"http://localhost:8000/sse\")'"]`,
		"      interval: 10s",
		"      timeout: 5s",
		"      retries: 5",
		"      start_period: 10s",
	}
	gotStr := string(got)
	for _, want := range wantLines {
		if !strings.Contains(gotStr, want) {
			t.Errorf("missing line: %q\nFull output:\n%s", want, gotStr)
		}
	}
	if !strings.Contains(gotStr, "volumes:") {
		t.Errorf("volumes: block lost after upsert")
	}
}

// TestUpsertComposeService_WithoutForceErrors — without force, second upsert
// errors and does NOT mutate the file.
func TestUpsertComposeService_WithoutForceErrors(t *testing.T) {
	path := copyComposeFixture(t, "baseline.yml")
	block := []byte("image: foo:latest\n")
	root, err := ReadComposeYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpsertComposeService(root, "elastic-mcp", block, false); err != nil {
		t.Fatal(err)
	}
	if err := WriteComposeYAMLAtomic(path, root); err != nil {
		t.Fatal(err)
	}
	beforeRetry, _ := os.ReadFile(path)

	root2, _ := ReadComposeYAML(path)
	err = UpsertComposeService(root2, "elastic-mcp", block, false)
	if err == nil {
		t.Fatal("expected error for existing service without force")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error doesn't mention 'already exists': %v", err)
	}
	afterRetry, _ := os.ReadFile(path)
	if !bytes.Equal(beforeRetry, afterRetry) {
		t.Fatalf("file mutated despite error")
	}
}

// TestRemoveComposeService — remove an existing service; idempotent for
// missing services.
func TestRemoveComposeService(t *testing.T) {
	path := copyComposeFixture(t, "baseline.yml")
	block, _ := os.ReadFile(filepath.Join("testdata", "compose", "elastic-mcp-block.yml"))

	root, err := ReadComposeYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpsertComposeService(root, "elastic-mcp", block, false); err != nil {
		t.Fatal(err)
	}
	if err := WriteComposeYAMLAtomic(path, root); err != nil {
		t.Fatal(err)
	}

	root2, _ := ReadComposeYAML(path)
	if err := RemoveComposeService(root2, "elastic-mcp"); err != nil {
		t.Fatal(err)
	}
	if err := WriteComposeYAMLAtomic(path, root2); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "elastic-mcp:") {
		t.Errorf("elastic-mcp not removed:\n%s", data)
	}

	// Idempotent.
	root3, _ := ReadComposeYAML(path)
	if err := RemoveComposeService(root3, "elastic-mcp"); err != nil {
		t.Fatalf("second RemoveComposeService should be a no-op: %v", err)
	}
	if err := RemoveComposeService(root3, "ghost-mcp"); err != nil {
		t.Fatalf("RemoveComposeService for absent service should be a no-op: %v", err)
	}
}
