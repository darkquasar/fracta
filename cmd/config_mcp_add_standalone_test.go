package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetAddStandaloneFlags clears the spec-49 standalone flags between subtests.
// (resetAddFlags() in the existing tests handles the original add flags but
// doesn't know about these new ones.)
func resetAddStandaloneFlags(t *testing.T) {
	t.Helper()
	oldC, oldCompose, oldK8s, oldCat := addConfigPath, addComposeFile, addK8sManifestDir, addCatalogDirFlag
	addConfigPath, addComposeFile, addK8sManifestDir, addCatalogDirFlag = "", "", "", ""
	t.Cleanup(func() {
		addConfigPath, addComposeFile, addK8sManifestDir, addCatalogDirFlag = oldC, oldCompose, oldK8s, oldCat
	})
	// Also clear projectRoot so the standalone path is genuine.
	oldPR := projectRoot
	projectRoot = ""
	t.Cleanup(func() { projectRoot = oldPR })
}

// TestConfigMcpAddStandalone_K8s exercises the spec-49 §2.4 standalone flow:
// no project, render the catalog entry against an explicit manifest dir and
// an explicit fracta.yaml path.
func TestConfigMcpAddStandalone_K8s(t *testing.T) {
	resetAddFlags()
	resetAddStandaloneFlags(t)

	dir := t.TempDir()
	manifestsDir := filepath.Join(dir, "out", "manifests")
	if err := os.MkdirAll(manifestsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "fracta.yaml")

	addCatalogDirFlag = catalogDirFromRepoRoot(t)
	addConfigPath = cfgPath
	addK8sManifestDir = manifestsDir
	addTargetDeploymentFlag = "k8s"
	addYesFlag = true

	if err := runConfigMcpAdd(configMcpAddCmd, []string{"fracta-test-server"}); err != nil {
		t.Fatalf("standalone add: %v", err)
	}

	// Manifest file must exist.
	manifestPath := filepath.Join(manifestsDir, "fracta-test-server-mcp.yaml")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("expected manifest at %s: %v", manifestPath, err)
	}
	if !strings.Contains(string(data), "kind: Deployment") {
		t.Errorf("manifest missing Deployment kind")
	}

	// fracta.yaml must have been created with the mcp_servers entry.
	cfgData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("expected fracta.yaml at %s: %v", cfgPath, err)
	}
	if !strings.Contains(string(cfgData), "fracta-test-server") {
		t.Errorf("fracta.yaml missing fracta-test-server entry; got:\n%s", string(cfgData))
	}
}

// TestConfigMcpAddStandalone_K8s_MissingTargetDeployment confirms that
// --target-deployment is required in standalone mode (no project state to
// infer from).
func TestConfigMcpAddStandalone_K8s_MissingTargetDeployment(t *testing.T) {
	resetAddFlags()
	resetAddStandaloneFlags(t)

	dir := t.TempDir()
	addCatalogDirFlag = catalogDirFromRepoRoot(t)
	addConfigPath = filepath.Join(dir, "fracta.yaml")
	// addTargetDeploymentFlag deliberately left empty.
	addYesFlag = true

	err := runConfigMcpAdd(configMcpAddCmd, []string{"fracta-test-server"})
	if err == nil {
		t.Fatal("expected error when standalone mode lacks --target-deployment")
	}
	if !strings.Contains(err.Error(), "--target-deployment is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestConfigMcpAddStandalone_K8s_MissingManifestDir confirms that the k8s
// path needs an explicit manifest dir when standalone.
func TestConfigMcpAddStandalone_K8s_MissingManifestDir(t *testing.T) {
	resetAddFlags()
	resetAddStandaloneFlags(t)

	dir := t.TempDir()
	addCatalogDirFlag = catalogDirFromRepoRoot(t)
	addConfigPath = filepath.Join(dir, "fracta.yaml")
	addTargetDeploymentFlag = "k8s"
	addYesFlag = true

	err := runConfigMcpAdd(configMcpAddCmd, []string{"fracta-test-server"})
	if err == nil {
		t.Fatal("expected error when standalone k8s lacks --k8s-manifest-dir")
	}
	if !strings.Contains(err.Error(), "--k8s-manifest-dir") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestConfigMcpAddStandalone_NoFlags_RequiresProject confirms that without
// any explicit-path flag, the command still demands a fracta project.
func TestConfigMcpAddStandalone_NoFlags_RequiresProject(t *testing.T) {
	resetAddFlags()
	resetAddStandaloneFlags(t)

	// Start in a temp dir that is NOT a fracta project.
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}

	addTargetDeploymentFlag = "local"
	addYesFlag = true

	err = runConfigMcpAdd(configMcpAddCmd, []string{"fracta-test-server"})
	if err == nil {
		t.Fatal("expected 'not a fracta project' error when no flags supplied")
	}
	if !strings.Contains(err.Error(), "not a fracta project") {
		t.Errorf("unexpected error: %v", err)
	}
}
