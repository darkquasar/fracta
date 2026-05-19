package scaffolds

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// migratedK8sManifests lists the k8s manifest filenames that exist BOTH in
// deployment/k8s-local-cluster/manifests/ and in the embedded scaffold tree.
// The spec-42 §11 R3 drift test enforces presence parity: each name must
// exist in both locations until the deployment/ copies are deleted by the
// follow-up tombstone PR.
//
// MCP-server manifests (elastic-mcp, purple-mcp) are deferred to spec-43 and
// intentionally NOT in this list — they live in deployment/ only.
var migratedK8sManifests = []string{
	"agent-job-template.yaml",
	"falkordb.yaml",
	"fracta-controlplane.yaml",
	"fracta-gateway.yaml",
	"namespace.yaml",
	"postgres.yaml",
	"pvc.yaml",
	"rbac.yaml",
	"workspace-pvc.yaml",
}

// repoRoot walks up from the test working directory until it finds the
// fracta repo root (identified by go.mod). Returns "" if not found — the
// drift test then skips.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	return ""
}

// TestScaffoldsK8sDriftParity verifies that every migrated manifest lives in
// BOTH deployment/k8s-local-cluster/manifests/ AND
// internal/project/scaffolds/templates/k8s/deployment/k8s/manifests/. Skipped if
// the deployment tree is gone (post-tombstone-PR future).
func TestScaffoldsK8sDriftParity(t *testing.T) {
	root := repoRoot(t)
	if root == "" {
		t.Skip("repo root not located; skipping drift test")
	}
	deploymentDir := filepath.Join(root, "deployment", "k8s-local-cluster", "manifests")
	if _, err := os.Stat(deploymentDir); os.IsNotExist(err) {
		t.Skip("deployment/k8s-local-cluster/manifests removed; drift test no longer applicable")
	}
	templatesDir := filepath.Join(root, "internal", "project", "scaffolds", "templates", "k8s", "deployment", "k8s", "manifests")
	if _, err := os.Stat(templatesDir); err != nil {
		t.Fatalf("templates dir missing: %v", err)
	}

	missingDeployment := []string{}
	missingTemplates := []string{}
	for _, name := range migratedK8sManifests {
		if _, err := os.Stat(filepath.Join(deploymentDir, name)); err != nil {
			missingDeployment = append(missingDeployment, name)
		}
		if _, err := os.Stat(filepath.Join(templatesDir, name)); err != nil {
			missingTemplates = append(missingTemplates, name)
		}
	}
	sort.Strings(missingDeployment)
	sort.Strings(missingTemplates)
	if len(missingDeployment) > 0 {
		t.Errorf("manifests missing from deployment/k8s-local-cluster/manifests/: %v", missingDeployment)
	}
	if len(missingTemplates) > 0 {
		t.Errorf("manifests missing from templates/k8s/.../manifests/: %v", missingTemplates)
	}

	// The templates/ tree may add NEW manifests not in deployment/ (e.g.
	// spec-42's auth-helpers-configmap.yaml). That's allowed. But every
	// file present in deployment/ that is in the migrated list MUST also
	// be in templates/, which is what the loop above already checks.
	//
	// We also assert that templates/ has no surprises beyond known names
	// — anything new requires updating migratedK8sManifests OR documenting
	// it as a templates-only addition (auth-helpers-configmap.yaml is the
	// canonical example).
	allowedTemplatesOnly := map[string]struct{}{
		"auth-helpers-configmap.yaml": {},
	}
	templatesEntries, err := os.ReadDir(templatesDir)
	if err != nil {
		t.Fatalf("readdir templates: %v", err)
	}
	known := map[string]struct{}{}
	for _, n := range migratedK8sManifests {
		known[n] = struct{}{}
	}
	for n := range allowedTemplatesOnly {
		known[n] = struct{}{}
	}
	for _, e := range templatesEntries {
		if e.IsDir() {
			continue
		}
		if _, ok := known[e.Name()]; !ok {
			t.Errorf("templates manifest %q is not in migratedK8sManifests nor allowedTemplatesOnly; if intentional, update scaffolds_lint_test.go", e.Name())
		}
	}
} 
