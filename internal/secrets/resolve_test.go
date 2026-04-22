package secrets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/darkquasar/fracta/internal/config"
)

func TestResolve_Value(t *testing.T) {
	got, err := Resolve(&config.SecretValue{Value: "literal"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "literal" {
		t.Errorf("got %q, want %q", got, "literal")
	}
}

func TestResolve_Env(t *testing.T) {
	os.Setenv("TEST_SECRET_RESOLVE", "from-env")
	defer os.Unsetenv("TEST_SECRET_RESOLVE")

	got, err := Resolve(&config.SecretValue{Env: "TEST_SECRET_RESOLVE"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from-env" {
		t.Errorf("got %q, want %q", got, "from-env")
	}
}

func TestResolve_EnvUnset(t *testing.T) {
	os.Unsetenv("DEFINITELY_UNSET_SECRET")
	_, err := Resolve(&config.SecretValue{Env: "DEFINITELY_UNSET_SECRET"})
	if err == nil {
		t.Fatal("expected error for unset env var")
	}
}

func TestResolve_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	os.WriteFile(path, []byte("file-secret\n"), 0o600)

	got, err := Resolve(&config.SecretValue{File: path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "file-secret" {
		t.Errorf("got %q, want %q", got, "file-secret")
	}
}

func TestResolve_FileMissing(t *testing.T) {
	_, err := Resolve(&config.SecretValue{File: "/nonexistent/path"})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestResolve_Nil(t *testing.T) {
	_, err := Resolve(nil)
	if err == nil {
		t.Fatal("expected error for nil secret")
	}
}

func TestResolve_Empty(t *testing.T) {
	_, err := Resolve(&config.SecretValue{})
	if err == nil {
		t.Fatal("expected error for empty secret value")
	}
}
