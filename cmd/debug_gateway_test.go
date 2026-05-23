package cmd

import (
	"os"
	"strings"
	"testing"
)

// runFromTempDir is a small helper that puts the CWD inside a fresh temp dir
// with no fracta project, runs fn, and restores the prior CWD. Useful for
// asserting that URL resolvers work when no project context is reachable.
func runFromTempDir(t *testing.T, fn func()) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	fn()
}

// resetGatewayFlags clears the package-level flag globals that
// resolveGatewayBaseURL / resolveCPAPIBaseURL read from.
func resetGatewayFlags(t *testing.T) {
	t.Helper()
	oldURL := debugGatewayURL
	oldCPURL := debugGatewayCPURL
	oldEnv := os.Getenv("FRACTA_GATEWAY_URL")

	debugGatewayURL = ""
	debugGatewayCPURL = ""
	_ = os.Unsetenv("FRACTA_GATEWAY_URL")

	t.Cleanup(func() {
		debugGatewayURL = oldURL
		debugGatewayCPURL = oldCPURL
		if oldEnv != "" {
			_ = os.Setenv("FRACTA_GATEWAY_URL", oldEnv)
		}
	})
}

func TestResolveGatewayBaseURL_FlagWinsWhenOutsideProject(t *testing.T) {
	resetGatewayFlags(t)
	debugGatewayURL = "http://gw.example:8080"

	runFromTempDir(t, func() {
		got, err := resolveGatewayBaseURL()
		if err != nil {
			t.Fatalf("expected --gateway-url to resolve outside any project; got: %v", err)
		}
		if got != "http://gw.example:8080" {
			t.Errorf("unexpected URL: %q", got)
		}
	})
}

func TestResolveGatewayBaseURL_EnvWinsWhenOutsideProject(t *testing.T) {
	resetGatewayFlags(t)
	_ = os.Setenv("FRACTA_GATEWAY_URL", "http://gw.from-env:9000")

	runFromTempDir(t, func() {
		got, err := resolveGatewayBaseURL()
		if err != nil {
			t.Fatalf("expected env var to resolve outside any project; got: %v", err)
		}
		if got != "http://gw.from-env:9000" {
			t.Errorf("unexpected URL: %q", got)
		}
	})
}

func TestResolveGatewayBaseURL_NoSourcesReturnsClearError(t *testing.T) {
	resetGatewayFlags(t)

	runFromTempDir(t, func() {
		_, err := resolveGatewayBaseURL()
		if err == nil {
			t.Fatal("expected error when no flag/env/project URL is set")
		}
		// Must NOT mention "not a fracta project" — that would be the wrong
		// failure mode (the user gave no project signal so demanding one is
		// the bug spec-49 §1.4 is fixing).
		if strings.Contains(err.Error(), "not a fracta project") {
			t.Errorf("error should not demand a fracta project; got: %v", err)
		}
		if !strings.Contains(err.Error(), "no gateway URL") {
			t.Errorf("error should be the no-URL message; got: %v", err)
		}
	})
}

func TestResolveCPAPIBaseURL_FlagWinsWhenOutsideProject(t *testing.T) {
	resetGatewayFlags(t)
	debugGatewayCPURL = "http://cp.example:9090"

	runFromTempDir(t, func() {
		got, err := resolveCPAPIBaseURL()
		if err != nil {
			t.Fatalf("expected --cp-url to resolve outside any project; got: %v", err)
		}
		if got != "http://cp.example:9090" {
			t.Errorf("unexpected URL: %q", got)
		}
	})
}

func TestResolveCPAPIBaseURL_NoSourcesReturnsClearError(t *testing.T) {
	resetGatewayFlags(t)

	runFromTempDir(t, func() {
		_, err := resolveCPAPIBaseURL()
		if err == nil {
			t.Fatal("expected error when no flag/project URL is set")
		}
		if strings.Contains(err.Error(), "not a fracta project") {
			t.Errorf("error should not demand a fracta project; got: %v", err)
		}
		if !strings.Contains(err.Error(), "no control-plane API URL") {
			t.Errorf("error should be the no-URL message; got: %v", err)
		}
	})
}
