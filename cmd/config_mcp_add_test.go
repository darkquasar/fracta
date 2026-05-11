package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resetAddFlags() {
	addTargetDeploymentFlag = ""
	addVariantFlag = ""
	addDryRunFlag = false
	addForceFlag = false
	addYesFlag = false
	addPullFlag = false
	addBuildFlag = false
}

// fixtureProjectForK8sAdd builds a project with:
//   - the 2-entry minimalCatalog at <root>/mcp-servers/
//   - fracta.yaml with runtime.backend = kubernetes
//   - empty fracta/k8s/manifests/ directory (so the scaffold is "enabled")
func fixtureProjectForK8sAdd(t *testing.T) string {
	cat, entries := minimalCatalog()
	root := tempProject(t, tempProjectOpts{
		catalogYAML: cat,
		entries:     entries,
		fractaYAML:  "runtime:\n  backend: kubernetes\n",
	})
	if err := os.MkdirAll(filepath.Join(root, "fracta", "k8s", "manifests"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return root
}

func TestConfigMcpAddElasticK8s_WritesManifestAndUpdatesFractaYAML(t *testing.T) {
	root := fixtureProjectForK8sAdd(t)
	resetAddFlags()
	addYesFlag = true // skip interactive prompt

	if err := runConfigMcpAdd(configMcpAddCmd, []string{"elastic"}); err != nil {
		t.Fatalf("add: %v", err)
	}

	manifestPath := filepath.Join(root, "fracta", "k8s", "manifests", "elastic-mcp.yaml")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Errorf("manifest not written: %v", err)
	}
	secretPath := filepath.Join(root, "fracta", "k8s", "manifests", "elastic-mcp-secret.yaml")
	if _, err := os.Stat(secretPath); err != nil {
		t.Errorf("secret stub not written (elastic has env_required so the stub should land): %v", err)
	}

	fractaYAML, err := os.ReadFile(filepath.Join(root, "fracta.yaml"))
	if err != nil {
		t.Fatalf("read fracta.yaml: %v", err)
	}
	got := string(fractaYAML)
	if !strings.Contains(got, "mcp_servers:") {
		t.Errorf("fracta.yaml missing mcp_servers block:\n%s", got)
	}
	if !strings.Contains(got, "elastic") {
		t.Errorf("fracta.yaml missing elastic entry:\n%s", got)
	}
	if !strings.Contains(got, "remote:") {
		t.Errorf("fracta.yaml missing remote block:\n%s", got)
	}

	// No .bak files should remain after a clean add.
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".bak") {
			t.Errorf("leftover .bak file: %s", e.Name())
		}
	}
}

func TestConfigMcpAddDryRunWritesNothing(t *testing.T) {
	root := fixtureProjectForK8sAdd(t)
	resetAddFlags()
	addDryRunFlag = true
	addYesFlag = true

	pre, _ := os.ReadFile(filepath.Join(root, "fracta.yaml"))
	out, err := captureStdout(t, func() error {
		return runConfigMcpAdd(configMcpAddCmd, []string{"elastic"})
	})
	if err != nil {
		t.Fatalf("dry-run add: %v", err)
	}
	if !strings.Contains(out, "dry run") {
		t.Errorf("preflight should mention dry-run: %q", out)
	}
	post, _ := os.ReadFile(filepath.Join(root, "fracta.yaml"))
	if string(pre) != string(post) {
		t.Errorf("dry-run mutated fracta.yaml:\npre=%q\npost=%q", pre, post)
	}
	if _, err := os.Stat(filepath.Join(root, "fracta", "k8s", "manifests", "elastic-mcp.yaml")); !os.IsNotExist(err) {
		t.Errorf("dry-run wrote manifest")
	}
}

func TestConfigMcpAddBlockedUntilK8sFails(t *testing.T) {
	root := fixtureProjectForK8sAdd(t)
	_ = root
	resetAddFlags()
	addYesFlag = true

	err := runConfigMcpAdd(configMcpAddCmd, []string{"notion"})
	if err == nil {
		t.Fatalf("expected error for notion k8s (support is blocked_until_*)")
	}
	if !strings.Contains(err.Error(), "blocked_until_gateway_oauth_token_store") {
		t.Errorf("error should surface support gate text: %v", err)
	}
}

func TestConfigMcpAddUnknownServerErrors(t *testing.T) {
	fixtureProjectForK8sAdd(t)
	resetAddFlags()
	addYesFlag = true

	err := runConfigMcpAdd(configMcpAddCmd, []string{"unknown-server"})
	if err == nil {
		t.Fatalf("expected error for unknown server")
	}
	if !strings.Contains(err.Error(), "unknown-server") || !strings.Contains(err.Error(), "not found") {
		t.Errorf("error wording: %v", err)
	}
}

func TestConfigMcpAddNoScaffoldErrors(t *testing.T) {
	// fracta.yaml exists but runtime.backend is neither local nor kubernetes
	// → no scaffold enabled → add should refuse.
	cat, entries := minimalCatalog()
	tempProject(t, tempProjectOpts{
		catalogYAML: cat,
		entries:     entries,
		fractaYAML:  "runtime:\n  backend: unknown\n",
	})
	resetAddFlags()
	addYesFlag = true
	addTargetDeploymentFlag = "k8s"

	err := runConfigMcpAdd(configMcpAddCmd, []string{"elastic"})
	if err == nil {
		t.Fatalf("expected scaffold-not-enabled error")
	}
	if !strings.Contains(err.Error(), "fracta init --scaffold") {
		t.Errorf("error should name prereq command: %v", err)
	}
}

func TestConfigMcpAddReRunWithoutForceErrors(t *testing.T) {
	fixtureProjectForK8sAdd(t)
	resetAddFlags()
	addYesFlag = true

	if err := runConfigMcpAdd(configMcpAddCmd, []string{"elastic"}); err != nil {
		t.Fatalf("first add: %v", err)
	}
	err := runConfigMcpAdd(configMcpAddCmd, []string{"elastic"})
	if err == nil {
		t.Fatalf("expected error on re-run without --force")
	}
}

func TestConfigMcpAddReRunWithForceIdempotent(t *testing.T) {
	root := fixtureProjectForK8sAdd(t)
	resetAddFlags()
	addYesFlag = true

	if err := runConfigMcpAdd(configMcpAddCmd, []string{"elastic"}); err != nil {
		t.Fatalf("first add: %v", err)
	}
	first, _ := os.ReadFile(filepath.Join(root, "fracta.yaml"))

	addForceFlag = true
	if err := runConfigMcpAdd(configMcpAddCmd, []string{"elastic"}); err != nil {
		t.Fatalf("force add: %v", err)
	}
	second, _ := os.ReadFile(filepath.Join(root, "fracta.yaml"))
	if string(first) != string(second) {
		t.Errorf("idempotency: forced re-add produced a different fracta.yaml\nfirst:\n%s\nsecond:\n%s",
			string(first), string(second))
	}
}

func TestConfigMcpAddPlanRollback(t *testing.T) {
	// Plant a manifest file at the expected output path with a sentinel
	// content. Without --force, planAdd should refuse and not mutate
	// anything; the sentinel content remains intact.
	root := fixtureProjectForK8sAdd(t)
	manifestPath := filepath.Join(root, "fracta", "k8s", "manifests", "elastic-mcp.yaml")
	sentinel := []byte("# sentinel content; planAdd must refuse without --force\n")
	if err := os.WriteFile(manifestPath, sentinel, 0o644); err != nil {
		t.Fatalf("plant sentinel: %v", err)
	}

	resetAddFlags()
	addYesFlag = true

	err := runConfigMcpAdd(configMcpAddCmd, []string{"elastic"})
	if err == nil {
		t.Fatalf("expected refusal due to existing manifest without --force")
	}
	got, _ := os.ReadFile(manifestPath)
	if string(got) != string(sentinel) {
		t.Errorf("sentinel content was modified; got=%q", got)
	}
	// No .bak should be left behind.
	if _, err := os.Stat(manifestPath + ".bak"); !os.IsNotExist(err) {
		t.Errorf(".bak should not exist; stat err=%v", err)
	}
}
