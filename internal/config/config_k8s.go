package config

import (
	"fmt"

	yamlv3 "gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

// k8sBuiltInVolumeNames lists the volume names the runtime always defines on
// agent pods. extra_volume_mounts may reference these without declaring a
// matching extra_volumes entry.
var k8sBuiltInVolumeNames = map[string]struct{}{
	"workspace":    {},
	"agent-config": {},
	"auth-secret":  {},
}

// k8sConfigJSON mirrors KubernetesConfig with json tags so corev1 types decode
// natively via sigs.k8s.io/yaml. Field names match KubernetesConfig's yaml tags
// 1:1 — sigs.k8s.io/yaml routes YAML → JSON → struct, and json tags drive the
// final decode.
type k8sConfigJSON struct {
	Namespace         string               `json:"namespace,omitempty"`
	Image             string               `json:"image,omitempty"`
	ImagePullPolicy   string               `json:"image_pull_policy,omitempty"`
	ServiceAccount    string               `json:"service_account,omitempty"`
	PVC               string               `json:"pvc,omitempty"`
	PVCMountPath      string               `json:"pvc_mount_path,omitempty"`
	Labels            map[string]string    `json:"labels,omitempty"`
	Annotations       map[string]string    `json:"annotations,omitempty"`
	Tolerations       []string             `json:"tolerations,omitempty"`
	NodeSelector      map[string]string    `json:"node_selector,omitempty"`
	Resources         ResourceConfig       `json:"resources,omitempty"`
	JobTTLSeconds     int                  `json:"job_ttl_seconds,omitempty"`
	ExtraVolumes      []corev1.Volume      `json:"extra_volumes,omitempty"`
	ExtraVolumeMounts []corev1.VolumeMount `json:"extra_volume_mounts,omitempty"`
}

// UnmarshalYAML routes KubernetesConfig through sigs.k8s.io/yaml so the
// corev1.Volume / corev1.VolumeMount fields decode via their json tags. Without
// this, gopkg.in/yaml.v3 would silently produce empty corev1 values because the
// kubernetes API types tag fields with json, not yaml.
func (k *KubernetesConfig) UnmarshalYAML(node *yamlv3.Node) error {
	raw, err := yamlv3.Marshal(node)
	if err != nil {
		return fmt.Errorf("kubernetes config: re-marshal node: %w", err)
	}
	var dst k8sConfigJSON
	if err := sigsyaml.Unmarshal(raw, &dst); err != nil {
		return fmt.Errorf("kubernetes config: %w", err)
	}
	*k = KubernetesConfig(dst)
	return nil
}

// Validate enforces spec-42 §8: each extra_volume_mounts[].name MUST reference
// either a declared extra_volumes[].name or one of the built-in volume names
// the runtime defines (workspace, agent-config, auth-secret). Empty fields skip
// validation.
func (k *KubernetesConfig) Validate() error {
	if len(k.ExtraVolumeMounts) == 0 {
		return nil
	}
	known := make(map[string]struct{}, len(k.ExtraVolumes)+len(k8sBuiltInVolumeNames))
	for name := range k8sBuiltInVolumeNames {
		known[name] = struct{}{}
	}
	for _, v := range k.ExtraVolumes {
		known[v.Name] = struct{}{}
	}
	for _, m := range k.ExtraVolumeMounts {
		if _, ok := known[m.Name]; !ok {
			valid := make([]string, 0, len(known))
			for n := range known {
				valid = append(valid, n)
			}
			return fmt.Errorf("kubernetes.extra_volume_mounts[].name=%q references no defined volume; valid names: %v", m.Name, valid)
		}
	}
	return nil
}
