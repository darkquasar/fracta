package config

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// TestKubernetesExtraVolumesParse round-trips a ConfigMap-backed extra volume
// + matching mount through the standard YAML loader. The corev1 fields must
// decode via their json tags (sigs.k8s.io/yaml routing in config_k8s.go).
func TestKubernetesExtraVolumesParse(t *testing.T) {
	yamlDoc := `
runtime:
  kubernetes:
    namespace: fracta
    image: ghcr.io/example/fracta:latest
    extra_volumes:
      - name: auth-helpers
        configMap:
          name: fracta-auth-helpers
          defaultMode: 0755
    extra_volume_mounts:
      - name: auth-helpers
        mountPath: /opt/fracta/auth-helpers
        readOnly: true
`
	cfg, err := ParseConfig([]byte(yamlDoc))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	k8s := cfg.Runtime.Kubernetes
	if k8s.Namespace != "fracta" {
		t.Errorf("Namespace = %q, want fracta", k8s.Namespace)
	}
	if len(k8s.ExtraVolumes) != 1 {
		t.Fatalf("ExtraVolumes len = %d, want 1", len(k8s.ExtraVolumes))
	}
	v := k8s.ExtraVolumes[0]
	if v.Name != "auth-helpers" {
		t.Errorf("volume name = %q, want auth-helpers", v.Name)
	}
	if v.ConfigMap == nil {
		t.Fatalf("ConfigMap source nil; got %+v", v.VolumeSource)
	}
	if v.ConfigMap.Name != "fracta-auth-helpers" {
		t.Errorf("ConfigMap.Name = %q, want fracta-auth-helpers", v.ConfigMap.Name)
	}
	if v.ConfigMap.DefaultMode == nil || *v.ConfigMap.DefaultMode != 0755 {
		t.Errorf("ConfigMap.DefaultMode = %v, want 0755", v.ConfigMap.DefaultMode)
	}
	if len(k8s.ExtraVolumeMounts) != 1 {
		t.Fatalf("ExtraVolumeMounts len = %d, want 1", len(k8s.ExtraVolumeMounts))
	}
	m := k8s.ExtraVolumeMounts[0]
	if m.Name != "auth-helpers" || m.MountPath != "/opt/fracta/auth-helpers" || !m.ReadOnly {
		t.Errorf("mount = %+v, want {auth-helpers /opt/fracta/auth-helpers ro}", m)
	}
}

// TestKubernetesExtraVolumesEmpty: absent extras decode to nil slices and
// produce no diagnostics from Validate().
func TestKubernetesExtraVolumesEmpty(t *testing.T) {
	yamlDoc := `
runtime:
  kubernetes:
    namespace: fracta
    image: ghcr.io/example/fracta:latest
`
	cfg, err := ParseConfig([]byte(yamlDoc))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	k8s := cfg.Runtime.Kubernetes
	if k8s.ExtraVolumes != nil {
		t.Errorf("ExtraVolumes = %+v, want nil", k8s.ExtraVolumes)
	}
	if k8s.ExtraVolumeMounts != nil {
		t.Errorf("ExtraVolumeMounts = %+v, want nil", k8s.ExtraVolumeMounts)
	}
	if err := k8s.Validate(); err != nil {
		t.Errorf("Validate on empty extras: unexpected error %v", err)
	}
}

// TestKubernetesExtraVolumesInvalid: structurally wrong input (a string where
// a list of corev1.Volume objects is expected) surfaces a decode error
// attributed to the kubernetes config block.
func TestKubernetesExtraVolumesInvalid(t *testing.T) {
	yamlDoc := `
runtime:
  kubernetes:
    namespace: fracta
    extra_volumes: "not-a-list"
`
	_, err := ParseConfig([]byte(yamlDoc))
	if err == nil {
		t.Fatalf("ParseConfig: expected error for invalid extra_volumes type, got nil")
	}
	if !strings.Contains(err.Error(), "kubernetes config") {
		t.Errorf("error = %v, want it to mention 'kubernetes config'", err)
	}
}

// TestKubernetesExtraVolumesMountMismatch: a mount referencing an undefined
// volume name must error at config load with the offending name and a list of
// valid names.
func TestKubernetesExtraVolumesMountMismatch(t *testing.T) {
	yamlDoc := `
runtime:
  kubernetes:
    namespace: fracta
    extra_volumes:
      - name: auth-helpers
        configMap:
          name: fracta-auth-helpers
    extra_volume_mounts:
      - name: nonexistent
        mountPath: /opt/wrong
`
	_, err := ParseConfig([]byte(yamlDoc))
	if err == nil {
		t.Fatalf("ParseConfig: expected mount-mismatch error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "nonexistent") {
		t.Errorf("error %q missing offending name 'nonexistent'", msg)
	}
	// The error should list valid names; at minimum the ones we declared and
	// the built-ins.
	for _, want := range []string{"auth-helpers", "workspace", "agent-config", "auth-secret"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing valid name %q", msg, want)
		}
	}
}

// TestKubernetesExtraVolumesMountMatchesBuiltin: a mount referencing a
// built-in volume name (workspace) succeeds even without a matching
// extra_volumes entry. This is by design — operators may want to layer
// additional mounts onto existing built-in volumes.
func TestKubernetesExtraVolumesMountMatchesBuiltin(t *testing.T) {
	yamlDoc := `
runtime:
  kubernetes:
    namespace: fracta
    extra_volume_mounts:
      - name: workspace
        mountPath: /workspace/extra
        subPath: nested
`
	cfg, err := ParseConfig([]byte(yamlDoc))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(cfg.Runtime.Kubernetes.ExtraVolumeMounts) != 1 {
		t.Fatalf("ExtraVolumeMounts len = %d, want 1", len(cfg.Runtime.Kubernetes.ExtraVolumeMounts))
	}
}

// Sanity-check direct construction + Validate without a config-load round trip.
func TestKubernetesConfigValidate_Direct(t *testing.T) {
	k := &KubernetesConfig{
		ExtraVolumes: []corev1.Volume{{Name: "helpers"}},
		ExtraVolumeMounts: []corev1.VolumeMount{
			{Name: "helpers", MountPath: "/x"},
		},
	}
	if err := k.Validate(); err != nil {
		t.Errorf("Validate: unexpected error %v", err)
	}
	k.ExtraVolumeMounts = append(k.ExtraVolumeMounts, corev1.VolumeMount{Name: "ghost", MountPath: "/y"})
	if err := k.Validate(); err == nil {
		t.Errorf("Validate: expected error for ghost mount, got nil")
	}
}
