package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureRemoteAvailableCatalog provides a "remote" catalog source with one
// extra entry (vendor) beyond what the local catalog has — so the diff shows
// 'available' for vendor.
func fixtureRemoteAvailableCatalog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cat := `version: "2"
description: remote catalog
servers:
  - id: elastic
    path: elastic/server.yaml
  - id: notion
    path: notion/server.yaml
  - id: vendor
    path: vendor/server.yaml
`
	_, entries := minimalCatalog()
	entries["vendor"] = `id: vendor
name: Vendor MCP
category: security
status: tested
description: Internal vendor MCP server
upstream: { type: first-party, url: https://example.com/ }
auth:
  modes: [env_token]
variants:
  container:
    image: vendor/vendor-mcp:latest
    image_owner: external
    transport: streamable-http
    service_url: http://vendor-mcp.fracta.svc:3000/mcp
support:
  local_process: supported
  docker_compose: supported
  kubernetes: supported
`
	writeFile(t, filepath.Join(dir, "mcp-servers", "catalog.yaml"), cat)
	for id, body := range entries {
		writeFile(t, filepath.Join(dir, "mcp-servers", id, "server.yaml"), body)
	}
	return dir
}

func TestConfigMcpListRemoteShowsAvailableAndConfigured(t *testing.T) {
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
	cat, entries := minimalCatalog()
	writeFile(t, filepath.Join(root, "mcp-servers", "catalog.yaml"), cat)
	for id, body := range entries {
		writeFile(t, filepath.Join(root, "mcp-servers", id, "server.yaml"), body)
	}

	// Plant a .fracta-source so ResolveFetchSource picks up the remote dir
	// without us needing a positional argument.
	remoteSrc := fixtureRemoteAvailableCatalog(t)
	writeFile(t, filepath.Join(root, "mcp-servers", ".fracta-source"), remoteSrc)

	listOutputFlag = "table"
	listRemoteFlag = true
	listFilterFlag = ""
	listNoImageStateFlag = true
	listTargetDeploymentFlag = ""

	out, err := captureStdout(t, func() error {
		return runConfigMcpList(configMcpListCmd, nil)
	})
	if err != nil {
		t.Fatalf("list --remote: %v", err)
	}

	// vendor is in remote but not local → available
	if !strings.Contains(out, "vendor") || !strings.Contains(out, "available") {
		t.Errorf("vendor should be listed as available:\n%s", out)
	}
	// elastic is configured in fracta.yaml → "configured (..."
	if !strings.Contains(out, "elastic") || !strings.Contains(out, "configured") {
		t.Errorf("elastic should be marked configured:\n%s", out)
	}
	// REMOTE column shows catalog.yaml version (v2 in the fixture)
	if !strings.Contains(out, "v2") {
		t.Errorf("REMOTE column should show v2:\n%s", out)
	}
}

// TestConfigMcpListRemoteOnFreshProject covers the marketplace-browser flow:
// an operator who has just run `fracta init` (and so has no local catalog
// yet) should still be able to run `list --remote` to see what's available
// upstream. Every remote entry renders as "available" with LOCAL = "not
// fetched".
func TestConfigMcpListRemoteOnFreshProject(t *testing.T) {
	root := tempProject(t, tempProjectOpts{
		fractaYAML: "runtime:\n  backend: kubernetes\n",
	})
	// Note: no <root>/mcp-servers/ — fresh project.

	remoteSrc := fixtureRemoteAvailableCatalog(t)
	writeFile(t, filepath.Join(root, "mcp-servers", ".fracta-source"), remoteSrc)
	// Above call had to create mcp-servers/ to write the .fracta-source file;
	// we delete the rest so LoadProjectCatalog returns ErrNoCatalog. The
	// .fracta-source file alone is not a catalog.
	_ = os.Remove(filepath.Join(root, "mcp-servers", "catalog.yaml"))

	listOutputFlag = "table"
	listRemoteFlag = true
	listFilterFlag = ""
	listNoImageStateFlag = true
	listTargetDeploymentFlag = ""

	out, err := captureStdout(t, func() error {
		return runConfigMcpList(configMcpListCmd, nil)
	})
	if err != nil {
		t.Fatalf("list --remote on fresh project: %v", err)
	}

	if !strings.Contains(out, "not fetched") {
		t.Errorf("LOCAL column should show 'not fetched' on fresh project:\n%s", out)
	}
	if !strings.Contains(out, "available") {
		t.Errorf("every remote entry should be 'available' on fresh project:\n%s", out)
	}
	if !strings.Contains(out, "vendor") || !strings.Contains(out, "elastic") {
		t.Errorf("expected vendor + elastic rows from fixture remote:\n%s", out)
	}
}

func TestConfigMcpListRemoteDegradesWhenSourceFails(t *testing.T) {
	root := tempProject(t, tempProjectOpts{
		fractaYAML: "runtime:\n  backend: local\n",
	})
	cat, entries := minimalCatalog()
	writeFile(t, filepath.Join(root, "mcp-servers", "catalog.yaml"), cat)
	for id, body := range entries {
		writeFile(t, filepath.Join(root, "mcp-servers", id, "server.yaml"), body)
	}
	// Point .fracta-source at a non-existent path so Fetch fails.
	bogusPath := filepath.Join(t.TempDir(), "does-not-exist")
	writeFile(t, filepath.Join(root, "mcp-servers", ".fracta-source"), bogusPath)

	listOutputFlag = "table"
	listRemoteFlag = true
	listFilterFlag = ""
	listNoImageStateFlag = true
	listTargetDeploymentFlag = "all"

	stderr := captureStderr(t, func() {
		_, err := captureStdout(t, func() error {
			return runConfigMcpList(configMcpListCmd, nil)
		})
		if err != nil {
			t.Fatalf("list --remote with bogus source should not error: %v", err)
		}
	})
	if !strings.Contains(stderr, "remote unavailable") {
		t.Errorf("expected 'remote unavailable' warning on stderr: %q", stderr)
	}
	_ = os.Stderr // keep import
}
