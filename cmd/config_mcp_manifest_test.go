package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// catalogDirFromRepoRoot returns the on-repo catalog dir, so tests can render
// real fixtures (notably fracta-test-server) without duplicating server.yaml
// content into testdata.
func catalogDirFromRepoRoot(t *testing.T) string {
	t.Helper()
	// We're in cmd/ — repo root is one level up.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(filepath.Dir(wd), "mcp-servers")
}

// resetManifestFlags clears flag globals between subtests.
func resetManifestFlags(t *testing.T) {
	t.Helper()
	oldV, oldN, oldI, oldD, oldO := manifestVariant, manifestNamespace, manifestImage, manifestCatalogDir, manifestOutput
	manifestVariant, manifestNamespace, manifestImage, manifestCatalogDir, manifestOutput = "", "", "", "", "k8s"
	t.Cleanup(func() {
		manifestVariant, manifestNamespace, manifestImage, manifestCatalogDir, manifestOutput = oldV, oldN, oldI, oldD, oldO
	})
}

func TestConfigMcpManifest_K8sOutput_FractaTestServer(t *testing.T) {
	resetManifestFlags(t)
	manifestCatalogDir = catalogDirFromRepoRoot(t)

	var buf bytes.Buffer
	configMcpManifestCmd.SetOut(&buf)
	configMcpManifestCmd.SetErr(&buf)

	if err := runConfigMcpManifest(configMcpManifestCmd, []string{"fracta-test-server"}); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	out := buf.String()
	mustContain := []string{
		"kind: Deployment",
		"kind: Service",
		"name: fracta-test-server-mcp",
		"namespace: fracta",
		"image: fracta/mcp-fracta-fracta-test-server:latest",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q in output:\n%s", s, out)
		}
	}
}

func TestConfigMcpManifest_ComposeOutput_FractaTestServer(t *testing.T) {
	resetManifestFlags(t)
	manifestCatalogDir = catalogDirFromRepoRoot(t)
	manifestOutput = "compose"

	var buf bytes.Buffer
	configMcpManifestCmd.SetOut(&buf)

	if err := runConfigMcpManifest(configMcpManifestCmd, []string{"fracta-test-server"}); err != nil {
		t.Fatalf("manifest compose: %v", err)
	}

	out := buf.String()
	// Compose output must NOT include k8s headers.
	if strings.Contains(out, "kind: Deployment") {
		t.Errorf("compose output should not contain k8s Deployment:\n%s", out)
	}
	if !strings.Contains(out, "fracta-test-server-mcp:") {
		t.Errorf("compose output should declare a service block; got:\n%s", out)
	}
	if !strings.Contains(out, "image: fracta/mcp-fracta-fracta-test-server:latest") {
		t.Errorf("compose output missing image; got:\n%s", out)
	}
}

func TestConfigMcpManifest_FractaYAMLOutput_FractaTestServer(t *testing.T) {
	resetManifestFlags(t)
	manifestCatalogDir = catalogDirFromRepoRoot(t)
	manifestOutput = "fracta-yaml"

	var buf bytes.Buffer
	configMcpManifestCmd.SetOut(&buf)

	if err := runConfigMcpManifest(configMcpManifestCmd, []string{"fracta-test-server"}); err != nil {
		t.Fatalf("manifest fracta-yaml: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "transport: streamable-http") {
		t.Errorf("fracta-yaml output should declare transport; got:\n%s", out)
	}
	if !strings.Contains(out, "url: http://fracta-test-server.fracta.svc:8000/mcp") {
		t.Errorf("fracta-yaml output should declare service URL; got:\n%s", out)
	}
}

func TestConfigMcpManifest_NamespaceOverride(t *testing.T) {
	resetManifestFlags(t)
	manifestCatalogDir = catalogDirFromRepoRoot(t)
	manifestNamespace = "custom-ns"

	var buf bytes.Buffer
	configMcpManifestCmd.SetOut(&buf)

	if err := runConfigMcpManifest(configMcpManifestCmd, []string{"fracta-test-server"}); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if !strings.Contains(buf.String(), "namespace: custom-ns") {
		t.Errorf("--namespace override not reflected in output:\n%s", buf.String())
	}
}

func TestConfigMcpManifest_UnknownServer(t *testing.T) {
	resetManifestFlags(t)
	manifestCatalogDir = catalogDirFromRepoRoot(t)

	var buf bytes.Buffer
	configMcpManifestCmd.SetOut(&buf)

	err := runConfigMcpManifest(configMcpManifestCmd, []string{"does-not-exist"})
	if err == nil {
		t.Fatal("expected error for unknown server id")
	}
	if !strings.Contains(err.Error(), "not found in catalog") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfigMcpManifest_UnknownOutput(t *testing.T) {
	resetManifestFlags(t)
	manifestCatalogDir = catalogDirFromRepoRoot(t)
	manifestOutput = "bogus"

	var buf bytes.Buffer
	configMcpManifestCmd.SetOut(&buf)

	err := runConfigMcpManifest(configMcpManifestCmd, []string{"fracta-test-server"})
	if err == nil {
		t.Fatal("expected error for unknown --output")
	}
	if !strings.Contains(err.Error(), "unknown --output") {
		t.Errorf("unexpected error: %v", err)
	}
}
