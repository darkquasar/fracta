package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigMcpRemoveK8sDeletesManifestsAndYAMLEntry(t *testing.T) {
	root := tempProject(t, tempProjectOpts{
		fractaYAML: `runtime:
  backend: kubernetes
mcp_servers:
  servers:
    elastic:
      remote:
        url: http://elastic-mcp.fracta.svc:3000/mcp
`,
	})
	// Drop a k8s manifest + secret stub so the plan detects them.
	writeFile(t, filepath.Join(root, "deployment", "k8s", "manifests", "elastic-mcp.yaml"),
		"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: elastic-mcp\n")
	writeFile(t, filepath.Join(root, "deployment", "k8s", "manifests", "elastic-mcp-secret.yaml"),
		"apiVersion: v1\nkind: Secret\nmetadata:\n  name: elastic-mcp-secrets\n")

	removeTargetDeploymentFlag = "k8s"
	removeKeepConfigFlag = false
	removeYesFlag = true

	if err := runConfigMcpRemove(configMcpRemoveCmd, []string{"elastic"}); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "deployment", "k8s", "manifests", "elastic-mcp.yaml")); !os.IsNotExist(err) {
		t.Errorf("expected manifest to be deleted, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "deployment", "k8s", "manifests", "elastic-mcp-secret.yaml")); !os.IsNotExist(err) {
		t.Errorf("expected secret stub to be deleted, stat err=%v", err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "fracta.yaml"))
	if strings.Contains(string(data), "elastic") {
		t.Errorf("fracta.yaml still references elastic:\n%s", string(data))
	}
}

func TestConfigMcpRemoveKeepConfigPreservesFractaYAML(t *testing.T) {
	root := tempProject(t, tempProjectOpts{
		fractaYAML: `runtime:
  backend: kubernetes
mcp_servers:
  servers:
    elastic:
      remote:
        url: http://elastic-mcp.fracta.svc:3000/mcp
`,
	})
	writeFile(t, filepath.Join(root, "deployment", "k8s", "manifests", "elastic-mcp.yaml"),
		"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: elastic-mcp\n")

	removeTargetDeploymentFlag = "k8s"
	removeKeepConfigFlag = true
	removeYesFlag = true

	if err := runConfigMcpRemove(configMcpRemoveCmd, []string{"elastic"}); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "deployment", "k8s", "manifests", "elastic-mcp.yaml")); !os.IsNotExist(err) {
		t.Errorf("expected manifest deleted")
	}
	data, _ := os.ReadFile(filepath.Join(root, "fracta.yaml"))
	if !strings.Contains(string(data), "elastic") {
		t.Errorf("--keep-config should preserve fracta.yaml entry; got:\n%s", string(data))
	}
}

func TestConfigMcpRemoveNothingToDo(t *testing.T) {
	// fracta.yaml exists but no server config — remove is a no-op.
	tempProject(t, tempProjectOpts{
		fractaYAML: "runtime:\n  backend: kubernetes\n",
	})
	removeTargetDeploymentFlag = "k8s"
	removeKeepConfigFlag = false
	removeYesFlag = true

	out, err := captureStdout(t, func() error {
		return runConfigMcpRemove(configMcpRemoveCmd, []string{"elastic"})
	})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !strings.Contains(out, "Nothing to remove") {
		t.Errorf("expected no-op message, got: %q", out)
	}
}

func TestConfigMcpRemoveAmbiguousModeError(t *testing.T) {
	// No scaffold enabled → can't infer mode. Need explicit flag.
	tempProject(t, tempProjectOpts{})
	removeTargetDeploymentFlag = ""
	removeKeepConfigFlag = false
	removeYesFlag = true

	err := runConfigMcpRemove(configMcpRemoveCmd, []string{"elastic"})
	if err == nil {
		t.Fatalf("expected error for ambiguous mode")
	}
	if !strings.Contains(err.Error(), "--target-deployment") {
		t.Errorf("error should mention --target-deployment: %v", err)
	}
}
