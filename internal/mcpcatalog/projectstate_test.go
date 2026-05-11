package mcpcatalog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/darkquasar/fracta/internal/project/scaffolds"
)

func TestLoadProjectState_NoFractaYAML(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadProjectState(dir)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(s.EnabledScaffolds) != 0 {
		t.Errorf("EnabledScaffolds = %v, want empty", s.EnabledScaffolds)
	}
	if _, ok := s.OnlyEnabled(); ok {
		t.Errorf("OnlyEnabled should be (_,false) when no scaffolds enabled")
	}
}

func TestLoadProjectState_OnlyLocal(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "fracta.yaml"), "runtime:\n  backend: local\n")
	s, err := LoadProjectState(dir)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !s.EnabledScaffolds[scaffolds.KindLocal] {
		t.Errorf("local should be enabled; got %v", s.EnabledScaffolds)
	}
	if s.EnabledScaffolds[scaffolds.KindDockerCompose] {
		t.Errorf("docker-compose should NOT be enabled")
	}
	k, ok := s.OnlyEnabled()
	if !ok || k != scaffolds.KindLocal {
		t.Errorf("OnlyEnabled = (%v,%v), want (KindLocal, true)", k, ok)
	}
}

func TestLoadProjectState_OnlyCompose(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "fracta.yaml"), "runtime:\n  backend: local\n")
	mustWrite(t, filepath.Join(dir, "fracta", "docker-compose.yml"), "services:\n  fracta:\n    image: ghcr.io/x\n")
	s, err := LoadProjectState(dir)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !s.EnabledScaffolds[scaffolds.KindDockerCompose] {
		t.Errorf("docker-compose should be enabled; got %v", s.EnabledScaffolds)
	}
	if s.EnabledScaffolds[scaffolds.KindLocal] {
		t.Errorf("local should NOT be enabled when compose present")
	}
	k, ok := s.OnlyEnabled()
	if !ok || k != scaffolds.KindDockerCompose {
		t.Errorf("OnlyEnabled = (%v,%v), want (KindDockerCompose, true)", k, ok)
	}
}

func TestLoadProjectState_OnlyK8s(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "fracta.yaml"), "runtime:\n  backend: kubernetes\n")
	s, err := LoadProjectState(dir)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !s.EnabledScaffolds[scaffolds.KindK8s] {
		t.Errorf("k8s should be enabled; got %v", s.EnabledScaffolds)
	}
	k, ok := s.OnlyEnabled()
	if !ok || k != scaffolds.KindK8s {
		t.Errorf("OnlyEnabled = (%v,%v), want (KindK8s, true)", k, ok)
	}
}

func TestLoadProjectState_ConfiguredLocal(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "fracta.yaml"), `runtime:
  backend: local
mcp_servers:
  servers:
    elastic:
      local:
        command: podman
`)
	s, err := LoadProjectState(dir)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !s.Configured["elastic"][scaffolds.KindLocal] {
		t.Errorf("elastic should be configured for local; got %+v", s.Configured)
	}
}

func TestLoadProjectState_ConfiguredK8sViaManifest(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "fracta.yaml"), "runtime:\n  backend: kubernetes\n")
	mustWrite(t, filepath.Join(dir, "fracta", "k8s", "manifests", "elastic-mcp.yaml"), "kind: Deployment\n")
	s, err := LoadProjectState(dir)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !s.Configured["elastic"][scaffolds.KindK8s] {
		t.Errorf("elastic should be configured for k8s; got %+v", s.Configured)
	}
}

func TestLoadProjectState_ConfiguredComposeViaService(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "fracta.yaml"), "runtime:\n  backend: local\n")
	mustWrite(t, filepath.Join(dir, "fracta", "docker-compose.yml"), `services:
  fracta:
    image: x
  elastic-mcp:
    image: docker.elastic.co/mcp/elasticsearch:latest
`)
	s, err := LoadProjectState(dir)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !s.Configured["elastic"][scaffolds.KindDockerCompose] {
		t.Errorf("elastic should be configured for compose; got %+v", s.Configured)
	}
}

func TestLoadProjectState_K8sManifestDeleted(t *testing.T) {
	// Operator deleted the manifests dir after init; the scaffold is still
	// "enabled" because fracta.yaml says backend: kubernetes.
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "fracta.yaml"), "runtime:\n  backend: kubernetes\n")
	// No fracta/k8s/manifests/ dir at all.
	s, err := LoadProjectState(dir)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !s.EnabledScaffolds[scaffolds.KindK8s] {
		t.Errorf("k8s should still be enabled even with no manifests/ dir; got %v", s.EnabledScaffolds)
	}
	if _, err := os.Stat(filepath.Join(dir, "fracta", "k8s", "manifests")); err == nil {
		t.Fatalf("test invariant: manifests dir should not exist")
	}
}
