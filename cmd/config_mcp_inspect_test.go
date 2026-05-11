package cmd

import (
	"strings"
	"testing"
)

func TestConfigMcpInspectShowsBlockedUntilK8sSupport(t *testing.T) {
	cat, entries := minimalCatalog()
	tempProject(t, tempProjectOpts{
		catalogYAML: cat,
		entries:     entries,
		fractaYAML:  "runtime:\n  backend: kubernetes\n",
	})

	out, err := captureStdout(t, func() error {
		return runConfigMcpInspect(configMcpInspectCmd, []string{"notion"})
	})
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}

	// Notion's k8s support is "blocked_until_gateway_oauth_token_store" in
	// the fixture — operators must see that text verbatim so they know what
	// gates the mode.
	if !strings.Contains(out, "blocked_until_gateway_oauth_token_store") {
		t.Errorf("inspect output missing blocked_until gate: %q", out)
	}
	// Auth modes and variants should both surface.
	if !strings.Contains(out, "oauth") {
		t.Errorf("inspect missing oauth auth mode: %q", out)
	}
	if !strings.Contains(out, "local_proxy") {
		t.Errorf("inspect missing local_proxy variant: %q", out)
	}
}

func TestConfigMcpInspectUnknownServerReturnsError(t *testing.T) {
	cat, entries := minimalCatalog()
	tempProject(t, tempProjectOpts{
		catalogYAML: cat,
		entries:     entries,
		fractaYAML:  "runtime:\n  backend: kubernetes\n",
	})

	err := runConfigMcpInspect(configMcpInspectCmd, []string{"does-not-exist"})
	if err == nil {
		t.Fatalf("expected error for unknown server")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error doesn't name server: %v", err)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error wording: %v", err)
	}
}

func TestConfigMcpInspectNoCatalogRemediation(t *testing.T) {
	tempProject(t, tempProjectOpts{
		fractaYAML: "runtime:\n  backend: local\n",
	})
	err := runConfigMcpInspect(configMcpInspectCmd, []string{"anything"})
	if err == nil {
		t.Fatalf("expected ErrNoCatalog remediation")
	}
	if !strings.Contains(err.Error(), "fracta config mcp fetch") {
		t.Errorf("missing remediation: %v", err)
	}
}
