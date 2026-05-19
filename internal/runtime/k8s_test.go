package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/model"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeapi "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestKubernetesBackend_SpawnCreatesJob(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{})

	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{
		ID:      "agent1",
		Command: "claude",
		Args:    []string{"-p", "hello"},
		Image:   "fracta/agent:latest",
		Env:     []string{"KEY=value", "FOO=bar"},
		Resources: &ResourceRequirements{
			CPURequest:    "250m",
			CPULimit:      "500m",
			MemoryRequest: "256Mi",
			MemoryLimit:   "512Mi",
		},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Verify the Job was created
	job, err := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-agent1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get job: %v", err)
	}

	if job.Labels["app"] != "fracta" {
		t.Errorf("job label app = %q, want %q", job.Labels["app"], "fracta")
	}
	if job.Labels[labelAgentID] != "agent1" {
		t.Errorf("job label agent-id = %q, want %q", job.Labels[labelAgentID], "agent1")
	}

	if len(job.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("containers count = %d, want 1", len(job.Spec.Template.Spec.Containers))
	}

	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != "fracta/agent:latest" {
		t.Errorf("container image = %q, want %q", container.Image, "fracta/agent:latest")
	}
	// Command should be nil (Docker ENTRYPOINT runs first, then exec "$@").
	// The agent command is passed as Args so the entrypoint handles auth + sidecar setup.
	if len(container.Command) != 0 {
		t.Errorf("container command = %v, want nil (let ENTRYPOINT handle it)", container.Command)
	}
	if len(container.Args) != 3 || container.Args[0] != "claude" || container.Args[1] != "-p" {
		t.Errorf("container args = %v, want [claude -p hello]", container.Args)
	}
	if len(container.Env) != 2 {
		t.Fatalf("env count = %d, want 2", len(container.Env))
	}
	if container.Env[0].Name != "KEY" || container.Env[0].Value != "value" {
		t.Errorf("env[0] = %s=%s, want KEY=value", container.Env[0].Name, container.Env[0].Value)
	}

	// Check resources
	cpuReq := container.Resources.Requests[corev1.ResourceCPU]
	if cpuReq.String() != "250m" {
		t.Errorf("CPU request = %s, want 250m", cpuReq.String())
	}
	memLimit := container.Resources.Limits[corev1.ResourceMemory]
	if memLimit.String() != "512Mi" {
		t.Errorf("Memory limit = %s, want 512Mi", memLimit.String())
	}

	// Check backoff and TTL
	if *job.Spec.BackoffLimit != 0 {
		t.Errorf("BackoffLimit = %d, want 0", *job.Spec.BackoffLimit)
	}
	if *job.Spec.TTLSecondsAfterFinished != 300 {
		t.Errorf("TTL = %d, want 300", *job.Spec.TTLSecondsAfterFinished)
	}

	if job.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("RestartPolicy = %q, want Never", job.Spec.Template.Spec.RestartPolicy)
	}
}

func TestKubernetesBackend_SpawnRequiresImage(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{})

	_, err := b.Spawn(context.Background(), SpawnOpts{
		ID:      "no-image",
		Command: "claude",
	})
	if err == nil {
		t.Fatal("Spawn without Image should fail")
	}
}

func TestKubernetesBackend_SpawnCustomNamespace(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "default-ns", KubernetesJobConfig{})

	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{
		ID:        "custom-ns-agent",
		Image:     "fracta/agent:latest",
		Namespace: "custom-ns",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Job should be in custom-ns, not default-ns
	_, err = client.BatchV1().Jobs("custom-ns").Get(ctx, "fracta-agent-custom-ns-agent", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Job not found in custom-ns: %v", err)
	}
}

func TestKubernetesBackend_DefaultNamespace(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "", KubernetesJobConfig{})

	if b.namespace != "fracta" {
		t.Errorf("default namespace = %q, want %q", b.namespace, "fracta")
	}
}

func TestKubernetesBackend_Kill(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:             "kill-me",
		Image:          "fracta/agent:latest",
		ConfigSnapshot: "settings: true\n",
		AuthSecretData: map[string][]byte{"token": []byte("secret")},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Verify resources exist before kill
	if _, err := client.CoreV1().ConfigMaps("test-ns").Get(ctx, "fracta-config-kill-me", metav1.GetOptions{}); err != nil {
		t.Fatalf("ConfigMap should exist before Kill: %v", err)
	}
	if _, err := client.CoreV1().Secrets("test-ns").Get(ctx, "fracta-auth-kill-me", metav1.GetOptions{}); err != nil {
		t.Fatalf("Secret should exist before Kill: %v", err)
	}

	if err := b.Kill(ctx, "kill-me"); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	// All resources should be deleted
	if _, err := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-kill-me", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("Job should be NotFound after Kill, got: %v", err)
	}
	if _, err := client.CoreV1().ConfigMaps("test-ns").Get(ctx, "fracta-config-kill-me", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("ConfigMap should be NotFound after Kill, got: %v", err)
	}
	if _, err := client.CoreV1().Secrets("test-ns").Get(ctx, "fracta-auth-kill-me", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("Secret should be NotFound after Kill, got: %v", err)
	}
}

func TestKubernetesBackend_Kill_NoConfigMap(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:    "kill-bare",
		Image: "fracta/agent:latest",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if err := b.Kill(ctx, "kill-bare"); err != nil {
		t.Fatalf("Kill should not error for bare spawn: %v", err)
	}
}

func TestKubernetesBackend_Kill_JobGone_StillCleansResources(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:             "kill-orphan",
		Image:          "fracta/agent:latest",
		ConfigSnapshot: "settings: true\n",
		AuthSecretData: map[string][]byte{"token": []byte("secret")},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Simulate Job already gone (TTL or manual delete)
	if err := client.BatchV1().Jobs("test-ns").Delete(ctx, "fracta-agent-kill-orphan", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("pre-delete Job: %v", err)
	}

	// Kill returns ErrNotFound for the Job...
	err = b.Kill(ctx, "kill-orphan")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Kill should return ErrNotFound when Job is gone, got: %v", err)
	}

	// ...but ConfigMap and Secret should still be cleaned up
	if _, err := client.CoreV1().ConfigMaps("test-ns").Get(ctx, "fracta-config-kill-orphan", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("ConfigMap should be cleaned even when Job is gone, got: %v", err)
	}
	if _, err := client.CoreV1().Secrets("test-ns").Get(ctx, "fracta-auth-kill-orphan", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("Secret should be cleaned even when Job is gone, got: %v", err)
	}
}

func TestKubernetesBackend_StatusCompleted(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:    "status-test",
		Image: "fracta/agent:latest",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Simulate Job completion by updating its status
	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-status-test", metav1.GetOptions{})
	job.Status.Conditions = append(job.Status.Conditions, batchv1.JobCondition{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	})
	client.BatchV1().Jobs("test-ns").UpdateStatus(ctx, job, metav1.UpdateOptions{})

	status, err := b.Status(ctx, "status-test")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != model.StatusCompleted {
		t.Errorf("Status = %q, want %q", status, model.StatusCompleted)
	}
}

func TestKubernetesBackend_StatusFailed(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:    "fail-test",
		Image: "fracta/agent:latest",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-fail-test", metav1.GetOptions{})
	job.Status.Conditions = append(job.Status.Conditions, batchv1.JobCondition{
		Type:    batchv1.JobFailed,
		Status:  corev1.ConditionTrue,
		Message: "BackoffLimitExceeded",
	})
	client.BatchV1().Jobs("test-ns").UpdateStatus(ctx, job, metav1.UpdateOptions{})

	status, err := b.Status(ctx, "fail-test")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != model.StatusFailed {
		t.Errorf("Status = %q, want %q", status, model.StatusFailed)
	}
}

func TestKubernetesBackend_StatusRunning(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:    "running-test",
		Image: "fracta/agent:latest",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-running-test", metav1.GetOptions{})
	job.Status.Active = 1
	client.BatchV1().Jobs("test-ns").UpdateStatus(ctx, job, metav1.UpdateOptions{})

	status, err := b.Status(ctx, "running-test")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != model.StatusRunning {
		t.Errorf("Status = %q, want %q", status, model.StatusRunning)
	}
}

func TestKubernetesBackend_StatusPending(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:    "pending-test",
		Image: "fracta/agent:latest",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	status, err := b.Status(ctx, "pending-test")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != model.StatusPending {
		t.Errorf("Status = %q, want %q", status, model.StatusPending)
	}
}

func TestKubernetesBackend_StatusNotFound(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{})

	_, err := b.Status(context.Background(), "nonexistent")
	if err == nil {
		t.Error("Status for nonexistent job should fail")
	}
}

func TestKubernetesBackend_SpawnNoResources(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:    "no-resources",
		Image: "fracta/agent:latest",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-no-resources", metav1.GetOptions{})
	container := job.Spec.Template.Spec.Containers[0]

	if len(container.Resources.Requests) != 0 {
		t.Errorf("Resources.Requests should be empty, got %v", container.Resources.Requests)
	}
	if len(container.Resources.Limits) != 0 {
		t.Errorf("Resources.Limits should be empty, got %v", container.Resources.Limits)
	}
}

// --- New tests for wiring ---

func TestKubernetesBackend_ImageFallbackFromConfig(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:v2",
	})

	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{
		ID: "config-image",
		// Image intentionally empty — should fall back to config
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-config-image", metav1.GetOptions{})
	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != "fracta/agent:v2" {
		t.Errorf("image = %q, want %q", container.Image, "fracta/agent:v2")
	}
}

func TestKubernetesBackend_SpawnOptsImageOverridesConfig(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:default",
	})

	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{
		ID:    "override-image",
		Image: "fracta/agent:override",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-override-image", metav1.GetOptions{})
	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != "fracta/agent:override" {
		t.Errorf("image = %q, want %q", container.Image, "fracta/agent:override")
	}
}

func TestKubernetesBackend_ServiceAccount(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image:          "fracta/agent:latest",
		ServiceAccount: "fracta-agent",
	})

	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{ID: "sa-test"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-sa-test", metav1.GetOptions{})
	if job.Spec.Template.Spec.ServiceAccountName != "fracta-agent" {
		t.Errorf("ServiceAccountName = %q, want %q", job.Spec.Template.Spec.ServiceAccountName, "fracta-agent")
	}
}

func TestKubernetesBackend_PVCVolumeAndMount(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
		PVC:   "fracta-workspace",
	})

	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{ID: "pvc-test"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-pvc-test", metav1.GetOptions{})
	podSpec := job.Spec.Template.Spec

	// Check volume
	if len(podSpec.Volumes) != 1 {
		t.Fatalf("volumes count = %d, want 1", len(podSpec.Volumes))
	}
	vol := podSpec.Volumes[0]
	if vol.Name != "workspace" {
		t.Errorf("volume name = %q, want %q", vol.Name, "workspace")
	}
	if vol.PersistentVolumeClaim == nil || vol.PersistentVolumeClaim.ClaimName != "fracta-workspace" {
		t.Errorf("PVC claim = %v, want fracta-workspace", vol.PersistentVolumeClaim)
	}

	// Check mount
	container := podSpec.Containers[0]
	if len(container.VolumeMounts) != 1 {
		t.Fatalf("volumeMounts count = %d, want 1", len(container.VolumeMounts))
	}
	mount := container.VolumeMounts[0]
	if mount.Name != "workspace" || mount.MountPath != "/workspace" {
		t.Errorf("mount = {%s, %s}, want {workspace, /workspace}", mount.Name, mount.MountPath)
	}
}

func TestKubernetesBackend_HostEnvVars(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
	})

	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{
		ID: "hostenv-test",
		HostEnv: []EnvEntry{
			{
				Name: "TEST_AUTH_TOKEN",
				SecretRef: &SecretRef{
					Name: "test-secret",
					Key:  "token",
				},
			},
			{Name: "TEST_REGION", Value: "us-west-2"},
			{Name: "TEST_MODEL", Value: "test-model-v1"},
		},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-hostenv-test", metav1.GetOptions{})
	container := job.Spec.Template.Spec.Containers[0]

	var foundToken, foundRegion, foundModel bool
	for _, env := range container.Env {
		switch env.Name {
		case "TEST_AUTH_TOKEN":
			foundToken = true
			if env.ValueFrom == nil || env.ValueFrom.SecretKeyRef == nil {
				t.Error("TEST_AUTH_TOKEN should use secretKeyRef")
			} else {
				if env.ValueFrom.SecretKeyRef.Name != "test-secret" {
					t.Errorf("secretKeyRef.Name = %q, want %q", env.ValueFrom.SecretKeyRef.Name, "test-secret")
				}
				if env.ValueFrom.SecretKeyRef.Key != "token" {
					t.Errorf("secretKeyRef.Key = %q, want %q", env.ValueFrom.SecretKeyRef.Key, "token")
				}
			}
		case "TEST_REGION":
			foundRegion = true
			if env.Value != "us-west-2" {
				t.Errorf("TEST_REGION = %q, want %q", env.Value, "us-west-2")
			}
		case "TEST_MODEL":
			foundModel = true
			if env.Value != "test-model-v1" {
				t.Errorf("TEST_MODEL = %q, want %q", env.Value, "test-model-v1")
			}
		}
	}

	if !foundToken {
		t.Error("TEST_AUTH_TOKEN not found in env")
	}
	if !foundRegion {
		t.Error("TEST_REGION not found in env")
	}
	if !foundModel {
		t.Error("TEST_MODEL not found in env")
	}
}

func TestKubernetesBackend_ConfigLabelsAndAnnotations(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image:       "fracta/agent:latest",
		Labels:      map[string]string{"component": "agent", "team": "security"},
		Annotations: map[string]string{"note": "test"},
	})

	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{ID: "labels-test"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-labels-test", metav1.GetOptions{})

	// Standard labels should be present
	if job.Labels["app"] != "fracta" {
		t.Errorf("label app = %q, want fracta", job.Labels["app"])
	}
	if job.Labels[labelAgentID] != "labels-test" {
		t.Errorf("label agent-id = %q, want labels-test", job.Labels[labelAgentID])
	}
	// Config labels should be merged
	if job.Labels["component"] != "agent" {
		t.Errorf("label component = %q, want agent", job.Labels["component"])
	}
	if job.Labels["team"] != "security" {
		t.Errorf("label team = %q, want security", job.Labels["team"])
	}

	// Pod template should have same labels
	podLabels := job.Spec.Template.Labels
	if podLabels["component"] != "agent" {
		t.Errorf("pod label component = %q, want agent", podLabels["component"])
	}

	// Annotations
	if job.Annotations["note"] != "test" {
		t.Errorf("annotation note = %q, want test", job.Annotations["note"])
	}
	if job.Spec.Template.Annotations["note"] != "test" {
		t.Errorf("pod annotation note = %q, want test", job.Spec.Template.Annotations["note"])
	}
}

func TestKubernetesBackend_ImagePullPolicy(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image:           "fracta/agent:latest",
		ImagePullPolicy: "Never",
	})

	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{ID: "pull-policy-test"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-pull-policy-test", metav1.GetOptions{})
	container := job.Spec.Template.Spec.Containers[0]
	if container.ImagePullPolicy != corev1.PullNever {
		t.Errorf("ImagePullPolicy = %q, want %q", container.ImagePullPolicy, corev1.PullNever)
	}
}

func TestKubernetesBackend_WorkingDir(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
	})

	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{ID: "workdir-test"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-workdir-test", metav1.GetOptions{})
	container := job.Spec.Template.Spec.Containers[0]
	expected := "/workspace/agents/workdir-test"
	if container.WorkingDir != expected {
		t.Errorf("WorkingDir = %q, want %q", container.WorkingDir, expected)
	}
}

func TestKubernetesBackend_CustomPVCMountPath(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image:        "fracta/agent:latest",
		PVC:          "custom-pvc",
		PVCMountPath: "/data",
	})

	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{ID: "mount-test"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-mount-test", metav1.GetOptions{})
	container := job.Spec.Template.Spec.Containers[0]

	// WorkingDir should use custom mount path
	if container.WorkingDir != "/data/agents/mount-test" {
		t.Errorf("WorkingDir = %q, want %q", container.WorkingDir, "/data/agents/mount-test")
	}

	// VolumeMount should use custom path
	if len(container.VolumeMounts) != 1 || container.VolumeMounts[0].MountPath != "/data" {
		t.Errorf("mount path = %v, want /data", container.VolumeMounts)
	}
}

func TestKubernetesBackend_TTLFromConfig(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image:         "fracta/agent:latest",
		JobTTLSeconds: 600,
	})

	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{ID: "ttl-test"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-ttl-test", metav1.GetOptions{})
	if *job.Spec.TTLSecondsAfterFinished != 600 {
		t.Errorf("TTL = %d, want 600", *job.Spec.TTLSecondsAfterFinished)
	}
}

func TestKubernetesBackend_TolerationsAndNodeSelector(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
		Tolerations: []corev1.Toleration{{
			Key:      "dedicated",
			Operator: corev1.TolerationOpEqual,
			Value:    "agents",
			Effect:   corev1.TaintEffectNoSchedule,
		}},
		NodeSelector: map[string]string{"node-type": "agent"},
	})

	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{ID: "sched-test"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-sched-test", metav1.GetOptions{})
	podSpec := job.Spec.Template.Spec

	if len(podSpec.Tolerations) != 1 {
		t.Fatalf("tolerations count = %d, want 1", len(podSpec.Tolerations))
	}
	if podSpec.Tolerations[0].Key != "dedicated" || podSpec.Tolerations[0].Value != "agents" {
		t.Errorf("toleration = %+v, want dedicated=agents", podSpec.Tolerations[0])
	}

	if podSpec.NodeSelector["node-type"] != "agent" {
		t.Errorf("nodeSelector = %v, want node-type=agent", podSpec.NodeSelector)
	}
}

func TestKubernetesBackend_ResourcesFromConfig(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
		Resources: ResourceRequirements{
			CPURequest:    "500m",
			CPULimit:      "2",
			MemoryRequest: "512Mi",
			MemoryLimit:   "2Gi",
		},
	})

	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{ID: "config-res"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-config-res", metav1.GetOptions{})
	container := job.Spec.Template.Spec.Containers[0]

	cpuReq := container.Resources.Requests[corev1.ResourceCPU]
	if cpuReq.String() != "500m" {
		t.Errorf("CPU request = %s, want 500m", cpuReq.String())
	}
	memLim := container.Resources.Limits[corev1.ResourceMemory]
	if memLim.String() != "2Gi" {
		t.Errorf("Memory limit = %s, want 2Gi", memLim.String())
	}
}

func TestKubernetesBackend_SpawnOptsResourcesOverrideConfig(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
		Resources: ResourceRequirements{
			CPURequest: "500m",
			CPULimit:   "2",
		},
	})

	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{
		ID: "override-res",
		Resources: &ResourceRequirements{
			CPURequest: "1",
			CPULimit:   "4",
		},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-override-res", metav1.GetOptions{})
	container := job.Spec.Template.Spec.Containers[0]

	cpuReq := container.Resources.Requests[corev1.ResourceCPU]
	if cpuReq.String() != "1" {
		t.Errorf("CPU request = %s, want 1 (override)", cpuReq.String())
	}
	cpuLim := container.Resources.Limits[corev1.ResourceCPU]
	if cpuLim.String() != "4" {
		t.Errorf("CPU limit = %s, want 4 (override)", cpuLim.String())
	}
}

func TestKubernetesBackend_FullJobSpec(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "fracta", KubernetesJobConfig{
		Image:           "fracta/agent:latest",
		ImagePullPolicy: "Never",
		ServiceAccount:  "fracta-agent",
		PVC:             "fracta-workspace",
		Labels:          map[string]string{"component": "agent"},
		JobTTLSeconds:   300,
		Resources: ResourceRequirements{
			CPURequest:    "500m",
			CPULimit:      "2",
			MemoryRequest: "512Mi",
			MemoryLimit:   "2Gi",
		},
	})

	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{
		ID:   "hunt-01",
		Args: []string{"-p", "run the task", "--output-format", "json"},
		HostEnv: []EnvEntry{
			{
				Name: "TEST_AUTH_TOKEN",
				SecretRef: &SecretRef{
					Name: "test-auth",
					Key:  "token",
				},
			},
			{Name: "TEST_REGION", Value: "us-west-2"},
			{Name: "TEST_MODEL", Value: "test-model-v1"},
		},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("fracta").Get(ctx, "fracta-agent-hunt-01", metav1.GetOptions{})

	// Job metadata
	if job.Name != "fracta-agent-hunt-01" {
		t.Errorf("job name = %q", job.Name)
	}
	if job.Namespace != "fracta" {
		t.Errorf("namespace = %q", job.Namespace)
	}
	if job.Labels["component"] != "agent" {
		t.Errorf("label component = %q", job.Labels["component"])
	}

	podSpec := job.Spec.Template.Spec

	// ServiceAccount
	if podSpec.ServiceAccountName != "fracta-agent" {
		t.Errorf("SA = %q", podSpec.ServiceAccountName)
	}

	// Volume
	if len(podSpec.Volumes) != 1 || podSpec.Volumes[0].PersistentVolumeClaim.ClaimName != "fracta-workspace" {
		t.Errorf("volumes = %v", podSpec.Volumes)
	}

	container := podSpec.Containers[0]

	// Image + policy
	if container.Image != "fracta/agent:latest" {
		t.Errorf("image = %q", container.Image)
	}
	if container.ImagePullPolicy != corev1.PullNever {
		t.Errorf("pullPolicy = %q", container.ImagePullPolicy)
	}

	// WorkingDir
	if container.WorkingDir != "/workspace/agents/hunt-01" {
		t.Errorf("workingDir = %q", container.WorkingDir)
	}

	// VolumeMount
	if len(container.VolumeMounts) != 1 || container.VolumeMounts[0].MountPath != "/workspace" {
		t.Errorf("mounts = %v", container.VolumeMounts)
	}

	// Host env vars (via HostEnv, not AuthJobConfig)
	envMap := make(map[string]corev1.EnvVar)
	for _, e := range container.Env {
		envMap[e.Name] = e
	}
	if tok, ok := envMap["TEST_AUTH_TOKEN"]; !ok {
		t.Error("missing TEST_AUTH_TOKEN")
	} else if tok.ValueFrom == nil || tok.ValueFrom.SecretKeyRef == nil {
		t.Error("TEST_AUTH_TOKEN should use secretKeyRef")
	}
	if envMap["TEST_REGION"].Value != "us-west-2" {
		t.Errorf("TEST_REGION = %q", envMap["TEST_REGION"].Value)
	}
	if envMap["TEST_MODEL"].Value != "test-model-v1" {
		t.Errorf("TEST_MODEL = %q", envMap["TEST_MODEL"].Value)
	}

	// Resources
	cpuReq := container.Resources.Requests[corev1.ResourceCPU]
	if cpuReq.String() != "500m" {
		t.Errorf("CPU request = %s", cpuReq.String())
	}
}

func TestConvertHostEnv_PlainValues(t *testing.T) {
	entries := []EnvEntry{
		{Name: "TEST_KEY", Value: "test-value"},
		{Name: "TEST_OTHER", Value: "other-value"},
	}
	result := convertHostEnv(entries)
	if len(result) != 2 {
		t.Fatalf("convertHostEnv count = %d, want 2", len(result))
	}
	if result[0].Name != "TEST_KEY" || result[0].Value != "test-value" {
		t.Errorf("result[0] = %+v", result[0])
	}
	if result[1].ValueFrom != nil {
		t.Error("plain value should not have ValueFrom")
	}
}

func TestConvertHostEnv_SecretRef(t *testing.T) {
	entries := []EnvEntry{
		{
			Name: "TEST_SECRET",
			SecretRef: &SecretRef{
				Name: "my-secret",
				Key:  "my-key",
			},
		},
	}
	result := convertHostEnv(entries)
	if len(result) != 1 {
		t.Fatalf("convertHostEnv count = %d, want 1", len(result))
	}
	ev := result[0]
	if ev.Name != "TEST_SECRET" {
		t.Errorf("Name = %q", ev.Name)
	}
	if ev.ValueFrom == nil || ev.ValueFrom.SecretKeyRef == nil {
		t.Fatal("expected secretKeyRef")
	}
	if ev.ValueFrom.SecretKeyRef.Name != "my-secret" {
		t.Errorf("SecretKeyRef.Name = %q", ev.ValueFrom.SecretKeyRef.Name)
	}
	if ev.ValueFrom.SecretKeyRef.Key != "my-key" {
		t.Errorf("SecretKeyRef.Key = %q", ev.ValueFrom.SecretKeyRef.Key)
	}
}

func TestConvertHostEnv_Empty(t *testing.T) {
	result := convertHostEnv(nil)
	if len(result) != 0 {
		t.Errorf("convertHostEnv(nil) = %d entries, want 0", len(result))
	}
}

func TestMergeEnv_HostEnvOverwritesDuplicates(t *testing.T) {
	base := []string{"TEST_KEY=base-value", "TEST_OTHER=keep"}
	hostEnv := []EnvEntry{
		{Name: "TEST_KEY", Value: "host-value"},
		{Name: "TEST_NEW", Value: "new-value"},
	}

	result := mergeEnv(base, hostEnv)

	envMap := make(map[string]corev1.EnvVar)
	for _, ev := range result {
		envMap[ev.Name] = ev
	}

	if len(result) != 3 {
		t.Fatalf("mergeEnv count = %d, want 3", len(result))
	}
	if envMap["TEST_KEY"].Value != "host-value" {
		t.Errorf("TEST_KEY = %q, want %q (HostEnv should win)", envMap["TEST_KEY"].Value, "host-value")
	}
	if envMap["TEST_OTHER"].Value != "keep" {
		t.Errorf("TEST_OTHER = %q, want %q", envMap["TEST_OTHER"].Value, "keep")
	}
	if envMap["TEST_NEW"].Value != "new-value" {
		t.Errorf("TEST_NEW = %q, want %q", envMap["TEST_NEW"].Value, "new-value")
	}
}

func TestMergeEnv_SecretRefOverwritesPlain(t *testing.T) {
	base := []string{"TEST_TOKEN=plain-value"}
	hostEnv := []EnvEntry{
		{
			Name:      "TEST_TOKEN",
			SecretRef: &SecretRef{Name: "secret-store", Key: "token"},
		},
	}

	result := mergeEnv(base, hostEnv)
	if len(result) != 1 {
		t.Fatalf("mergeEnv count = %d, want 1", len(result))
	}
	if result[0].ValueFrom == nil || result[0].ValueFrom.SecretKeyRef == nil {
		t.Fatal("TEST_TOKEN should be overwritten with secretKeyRef")
	}
	if result[0].ValueFrom.SecretKeyRef.Name != "secret-store" {
		t.Errorf("SecretKeyRef.Name = %q", result[0].ValueFrom.SecretKeyRef.Name)
	}
}

func TestMergeEnv_EmptyInputs(t *testing.T) {
	result := mergeEnv(nil, nil)
	if len(result) != 0 {
		t.Errorf("mergeEnv(nil, nil) = %d entries, want 0", len(result))
	}
}

// --- K3: ConfigMap lifecycle tests ---

func TestSpawn_ConfigMapCreation(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
	})
	ctx := context.Background()

	snapshot := "connections:\n  graph:\n    host: localhost\nstrategy:\n  dir: /opt/fracta/strategies\n"

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:             "cm-test",
		ConfigSnapshot: snapshot,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Verify ConfigMap was created with correct name and data
	cm, err := client.CoreV1().ConfigMaps("test-ns").Get(ctx, "fracta-config-cm-test", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("ConfigMap not found: %v", err)
	}

	if cm.Data["agent-config.yaml"] != snapshot {
		t.Errorf("ConfigMap data = %q, want %q", cm.Data["agent-config.yaml"], snapshot)
	}

	// Verify labels are set
	if cm.Labels["app"] != "fracta" {
		t.Errorf("ConfigMap label app = %q, want %q", cm.Labels["app"], "fracta")
	}
	if cm.Labels[labelAgentID] != "cm-test" {
		t.Errorf("ConfigMap label agent-id = %q, want %q", cm.Labels[labelAgentID], "cm-test")
	}
}

func TestSpawn_ConfigMapOwnerReference(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
	})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:             "owner-test",
		ConfigSnapshot: "logging:\n  level: debug\n",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Get the Job to check its UID
	job, err := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-owner-test", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Job not found: %v", err)
	}

	// Get ConfigMap and verify ownerReference
	cm, err := client.CoreV1().ConfigMaps("test-ns").Get(ctx, "fracta-config-owner-test", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("ConfigMap not found: %v", err)
	}

	if len(cm.OwnerReferences) != 1 {
		t.Fatalf("ownerReferences count = %d, want 1", len(cm.OwnerReferences))
	}
	ref := cm.OwnerReferences[0]
	if ref.APIVersion != "batch/v1" {
		t.Errorf("ownerRef APIVersion = %q, want %q", ref.APIVersion, "batch/v1")
	}
	if ref.Kind != "Job" {
		t.Errorf("ownerRef Kind = %q, want %q", ref.Kind, "Job")
	}
	if ref.Name != job.Name {
		t.Errorf("ownerRef Name = %q, want %q", ref.Name, job.Name)
	}
	if ref.UID != job.UID {
		t.Errorf("ownerRef UID = %q, want %q", ref.UID, job.UID)
	}
	if ref.Controller == nil || !*ref.Controller {
		t.Error("ownerRef Controller should be true")
	}
}

func TestSpawn_ConfigMapVolume(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
		PVC:   "fracta-workspace",
	})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:             "vol-test",
		ConfigSnapshot: "strategy:\n  dir: /opt/fracta/strategies\n",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, err := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-vol-test", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Job not found: %v", err)
	}

	podSpec := job.Spec.Template.Spec
	container := podSpec.Containers[0]

	// Should have 2 volumes: workspace PVC + agent-config ConfigMap
	if len(podSpec.Volumes) != 2 {
		t.Fatalf("volumes count = %d, want 2", len(podSpec.Volumes))
	}

	// Find the agent-config volume
	var configVol *corev1.Volume
	for i := range podSpec.Volumes {
		if podSpec.Volumes[i].Name == "agent-config" {
			configVol = &podSpec.Volumes[i]
			break
		}
	}
	if configVol == nil {
		t.Fatal("agent-config volume not found")
	}
	if configVol.ConfigMap == nil {
		t.Fatal("agent-config volume should be a ConfigMap volume")
	}
	if configVol.ConfigMap.Name != "fracta-config-vol-test" {
		t.Errorf("ConfigMap volume name = %q, want %q", configVol.ConfigMap.Name, "fracta-config-vol-test")
	}

	// Should have 2 mounts: workspace + agent-config
	if len(container.VolumeMounts) != 2 {
		t.Fatalf("volumeMounts count = %d, want 2", len(container.VolumeMounts))
	}

	// Find the agent-config mount
	var configMount *corev1.VolumeMount
	for i := range container.VolumeMounts {
		if container.VolumeMounts[i].Name == "agent-config" {
			configMount = &container.VolumeMounts[i]
			break
		}
	}
	if configMount == nil {
		t.Fatal("agent-config volume mount not found")
	}
	if configMount.MountPath != "/etc/fracta" {
		t.Errorf("mount path = %q, want %q", configMount.MountPath, "/etc/fracta")
	}
	if !configMount.ReadOnly {
		t.Error("agent-config mount should be read-only")
	}
}

func TestSpawn_NoConfigSnapshot(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
	})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID: "no-cm-test",
		// ConfigSnapshot intentionally empty
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// No ConfigMap should exist
	cmList, err := client.CoreV1().ConfigMaps("test-ns").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("List ConfigMaps: %v", err)
	}
	if len(cmList.Items) != 0 {
		t.Errorf("ConfigMaps count = %d, want 0 (no ConfigMap when snapshot empty)", len(cmList.Items))
	}

	// Job should have no config volume or mount
	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-no-cm-test", metav1.GetOptions{})
	podSpec := job.Spec.Template.Spec
	container := podSpec.Containers[0]

	for _, vol := range podSpec.Volumes {
		if vol.Name == "agent-config" {
			t.Error("agent-config volume should not exist when ConfigSnapshot is empty")
		}
	}
	for _, mount := range container.VolumeMounts {
		if mount.Name == "agent-config" {
			t.Error("agent-config mount should not exist when ConfigSnapshot is empty")
		}
	}
}

// --- Workspace files injection tests (ConfigMap+emptyDir model) ---

func TestSpawn_WorkspaceFilesInConfigMap(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
	})
	ctx := context.Background()

	wsFiles := []WorkspaceArtifact{
		{ConfigMapKey: "dot-claude--settings.json", DestPath: ".claude/settings.json", Content: `{"permissions":{"allow":["Bash(git *)"]}}`},
		{ConfigMapKey: "dot-mcp.json", DestPath: ".mcp.json", Content: `{"mcpServers":{}}`},
		{ConfigMapKey: "CLAUDE.md", DestPath: "CLAUDE.md", Content: "# Task\nDo the thing."},
		{ConfigMapKey: "dot-fracta--user-settings.json", DestPath: ".fracta/user-settings.json", Content: `{"apiKeyHelper":"/usr/local/bin/fetch-bedrock-token"}`},
	}

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:             "ws-test",
		ConfigSnapshot: "logging:\n  level: info\n",
		WorkspaceFiles: wsFiles,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Verify ConfigMap contains both config snapshot and workspace files
	cm, err := client.CoreV1().ConfigMaps("test-ns").Get(ctx, "fracta-config-ws-test", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("ConfigMap not found: %v", err)
	}

	if cm.Data["agent-config.yaml"] != "logging:\n  level: info\n" {
		t.Errorf("ConfigMap missing agent-config.yaml")
	}
	for _, wf := range wsFiles {
		if cm.Data[wf.ConfigMapKey] != wf.Content {
			t.Errorf("ConfigMap[%q] = %q, want %q", wf.ConfigMapKey, cm.Data[wf.ConfigMapKey], wf.Content)
		}
	}
}

func TestSpawn_EmptyDirWhenNoPVC(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
		// No PVC configured
	})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:             "emptydir-test",
		WorkspaceFiles: []WorkspaceArtifact{{ConfigMapKey: "CLAUDE.md", DestPath: "CLAUDE.md", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-emptydir-test", metav1.GetOptions{})
	podSpec := job.Spec.Template.Spec

	// Should have workspace (emptyDir) + agent-config volumes
	var foundEmptyDir, foundConfigMap bool
	for _, vol := range podSpec.Volumes {
		if vol.Name == "workspace" && vol.EmptyDir != nil {
			foundEmptyDir = true
		}
		if vol.Name == "agent-config" && vol.ConfigMap != nil {
			foundConfigMap = true
		}
	}
	if !foundEmptyDir {
		t.Error("expected emptyDir volume for workspace")
	}
	if !foundConfigMap {
		t.Error("expected ConfigMap volume for agent-config")
	}

	// Container should have workspace + agent-config mounts
	container := podSpec.Containers[0]
	var hasWorkspaceMount, hasConfigMount bool
	for _, vm := range container.VolumeMounts {
		if vm.Name == "workspace" && vm.MountPath == "/workspace" {
			hasWorkspaceMount = true
		}
		if vm.Name == "agent-config" && vm.MountPath == "/etc/fracta" {
			hasConfigMount = true
		}
	}
	if !hasWorkspaceMount {
		t.Error("container missing workspace volume mount")
	}
	if !hasConfigMount {
		t.Error("container missing agent-config volume mount")
	}
}

func TestSpawn_InitContainer(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
	})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID: "init-test",
		WorkspaceFiles: []WorkspaceArtifact{
			{ConfigMapKey: "CLAUDE.md", DestPath: "CLAUDE.md", Content: "do stuff"},
			{ConfigMapKey: "dot-claude--settings.json", DestPath: ".claude/settings.json", Content: "{}"},
		},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-init-test", metav1.GetOptions{})
	podSpec := job.Spec.Template.Spec

	if len(podSpec.InitContainers) != 1 {
		t.Fatalf("initContainers count = %d, want 1", len(podSpec.InitContainers))
	}

	init := podSpec.InitContainers[0]
	if init.Name != "workspace-init" {
		t.Errorf("initContainer name = %q, want %q", init.Name, "workspace-init")
	}
	if init.Image != "fracta/agent:latest" {
		t.Errorf("initContainer image = %q, want %q", init.Image, "fracta/agent:latest")
	}

	// Should have agent-config + workspace mounts
	mountNames := make(map[string]bool)
	for _, vm := range init.VolumeMounts {
		mountNames[vm.Name] = true
	}
	if !mountNames["agent-config"] {
		t.Error("initContainer missing agent-config mount")
	}
	if !mountNames["workspace"] {
		t.Error("initContainer missing workspace mount")
	}

	// Command should be sh -c with copy script
	if len(init.Command) != 3 || init.Command[0] != "sh" || init.Command[1] != "-c" {
		t.Errorf("initContainer command = %v, want [sh -c <script>]", init.Command)
	}
}

func TestSpawn_InitContainerInheritsImagePullPolicy(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image:           "fracta/agent:latest",
		ImagePullPolicy: "Never",
	})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:             "pull-init-test",
		WorkspaceFiles: []WorkspaceArtifact{{ConfigMapKey: "CLAUDE.md", DestPath: "CLAUDE.md", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-pull-init-test", metav1.GetOptions{})
	init := job.Spec.Template.Spec.InitContainers[0]
	if init.ImagePullPolicy != corev1.PullNever {
		t.Errorf("initContainer ImagePullPolicy = %q, want %q", init.ImagePullPolicy, corev1.PullNever)
	}
}

func TestSpawn_NoInitContainerWithoutWorkspaceFiles(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
	})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:             "no-init-test",
		ConfigSnapshot: "logging:\n  level: info\n",
		// No WorkspaceFiles
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-no-init-test", metav1.GetOptions{})
	if len(job.Spec.Template.Spec.InitContainers) != 0 {
		t.Errorf("initContainers count = %d, want 0 (no workspace files)", len(job.Spec.Template.Spec.InitContainers))
	}
}

func TestSpawn_WorkspaceFilesWithPVC(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
		PVC:   "fracta-workspace",
	})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:             "pvc-ws-test",
		WorkspaceFiles: []WorkspaceArtifact{{ConfigMapKey: "CLAUDE.md", DestPath: "CLAUDE.md", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-pvc-ws-test", metav1.GetOptions{})
	podSpec := job.Spec.Template.Spec

	// Should use PVC, not emptyDir
	for _, vol := range podSpec.Volumes {
		if vol.Name == "workspace" {
			if vol.EmptyDir != nil {
				t.Error("workspace volume should be PVC, not emptyDir, when PVC is configured")
			}
			if vol.PersistentVolumeClaim == nil || vol.PersistentVolumeClaim.ClaimName != "fracta-workspace" {
				t.Error("workspace volume should be PVC fracta-workspace")
			}
		}
	}

	// But initContainer should still be present (to distribute files)
	if len(podSpec.InitContainers) != 1 {
		t.Fatalf("initContainers count = %d, want 1 (files still need distribution)", len(podSpec.InitContainers))
	}
}

func TestSpawn_WorkspaceFilesOnlyNoConfigSnapshot(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
	})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:             "ws-only-test",
		WorkspaceFiles: []WorkspaceArtifact{{ConfigMapKey: "CLAUDE.md", DestPath: "CLAUDE.md", Content: "hello"}},
		// No ConfigSnapshot
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// ConfigMap should exist with workspace files only (no agent-config.yaml key)
	cm, err := client.CoreV1().ConfigMaps("test-ns").Get(ctx, "fracta-config-ws-only-test", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("ConfigMap not found: %v", err)
	}
	if _, ok := cm.Data["agent-config.yaml"]; ok {
		t.Error("ConfigMap should not have agent-config.yaml when ConfigSnapshot is empty")
	}
	if cm.Data["CLAUDE.md"] != "hello" {
		t.Errorf("ConfigMap[CLAUDE.md] = %q, want %q", cm.Data["CLAUDE.md"], "hello")
	}
}

func TestSpawn_ConfigMapOnlyVolumeNoPVC(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
		// No PVC configured
	})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:             "cm-only-test",
		ConfigSnapshot: "logging:\n  level: info\n",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-cm-only-test", metav1.GetOptions{})
	podSpec := job.Spec.Template.Spec
	container := podSpec.Containers[0]

	// Should have exactly 1 volume: agent-config (no PVC)
	if len(podSpec.Volumes) != 1 {
		t.Fatalf("volumes count = %d, want 1", len(podSpec.Volumes))
	}
	if podSpec.Volumes[0].Name != "agent-config" {
		t.Errorf("volume name = %q, want %q", podSpec.Volumes[0].Name, "agent-config")
	}

	// Should have exactly 1 mount: agent-config
	if len(container.VolumeMounts) != 1 {
		t.Fatalf("volumeMounts count = %d, want 1", len(container.VolumeMounts))
	}
	if container.VolumeMounts[0].Name != "agent-config" {
		t.Errorf("mount name = %q, want %q", container.VolumeMounts[0].Name, "agent-config")
	}
}

// --- Auth Secret lifecycle tests ---

func TestSpawn_AuthSecretCreation(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{Image: "fracta/agent:latest"})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:                  "auth-test",
		AuthSecretData:      map[string][]byte{"bedrock-token": []byte("secret-token-value")},
		AuthSecretMountPath: "/var/run/fracta-auth",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	secret, err := client.CoreV1().Secrets("test-ns").Get(ctx, "fracta-auth-auth-test", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Secret not found: %v", err)
	}
	if string(secret.Data["bedrock-token"]) != "secret-token-value" {
		t.Errorf("Secret data = %q, want %q", string(secret.Data["bedrock-token"]), "secret-token-value")
	}
	if secret.Labels["app"] != "fracta" {
		t.Errorf("Secret label app = %q, want fracta", secret.Labels["app"])
	}
}

func TestSpawn_AuthSecretOwnerReference(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{Image: "fracta/agent:latest"})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:                  "auth-owner-test",
		AuthSecretData:      map[string][]byte{"token": []byte("val")},
		AuthSecretMountPath: "/var/run/fracta-auth",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-auth-owner-test", metav1.GetOptions{})
	secret, _ := client.CoreV1().Secrets("test-ns").Get(ctx, "fracta-auth-auth-owner-test", metav1.GetOptions{})

	if len(secret.OwnerReferences) != 1 {
		t.Fatalf("ownerReferences count = %d, want 1", len(secret.OwnerReferences))
	}
	ref := secret.OwnerReferences[0]
	if ref.Kind != "Job" || ref.Name != job.Name || ref.UID != job.UID {
		t.Errorf("ownerRef = {%s, %s, %s}, want {Job, %s, %s}", ref.Kind, ref.Name, ref.UID, job.Name, job.UID)
	}
}

func TestSpawn_AuthSecretVolume(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{Image: "fracta/agent:latest"})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:                  "auth-vol-test",
		AuthSecretData:      map[string][]byte{"token": []byte("val")},
		AuthSecretMountPath: "/var/run/fracta-auth",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-auth-vol-test", metav1.GetOptions{})
	podSpec := job.Spec.Template.Spec
	container := podSpec.Containers[0]

	// Check volume
	var foundSecretVol bool
	for _, vol := range podSpec.Volumes {
		if vol.Name == "auth-secret" && vol.Secret != nil && vol.Secret.SecretName == "fracta-auth-auth-vol-test" {
			foundSecretVol = true
		}
	}
	if !foundSecretVol {
		t.Error("auth-secret volume not found")
	}

	// Check mount
	var foundSecretMount bool
	for _, vm := range container.VolumeMounts {
		if vm.Name == "auth-secret" && vm.MountPath == "/var/run/fracta-auth" && vm.ReadOnly {
			foundSecretMount = true
		}
	}
	if !foundSecretMount {
		t.Error("auth-secret volume mount not found at /var/run/fracta-auth")
	}
}

func TestSpawn_NoAuthSecretWhenEmpty(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{Image: "fracta/agent:latest"})
	ctx := context.Background()

	_, err := b.Spawn(ctx, SpawnOpts{
		ID: "no-auth-test",
		// No AuthSecretData
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	secrets, _ := client.CoreV1().Secrets("test-ns").List(ctx, metav1.ListOptions{})
	if len(secrets.Items) != 0 {
		t.Errorf("Secrets count = %d, want 0", len(secrets.Items))
	}

	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-no-auth-test", metav1.GetOptions{})
	for _, vol := range job.Spec.Template.Spec.Volumes {
		if vol.Name == "auth-secret" {
			t.Error("auth-secret volume should not exist when no AuthSecretData")
		}
	}
}

// --- Event bus tests ---

// recordingBus captures emitted events for test assertions.
type recordingBus struct {
	mu     sync.Mutex
	events []events.Event
}

func (r *recordingBus) Emit(_ context.Context, e events.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recordingBus) captured() []events.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]events.Event, len(r.events))
	copy(cp, r.events)
	return cp
}

func TestKubernetesBackend_EmitsJobCreated(t *testing.T) {
	client := fake.NewSimpleClientset()
	rec := &recordingBus{}
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
	})
	b.SetEventBus(rec)

	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{
		ID:    "event-test",
		Model: "claude-sonnet",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	captured := rec.captured()
	if len(captured) != 1 {
		t.Fatalf("expected 1 event, got %d", len(captured))
	}

	e := captured[0]
	if e.Component != "runtime.k8s" {
		t.Errorf("Component = %q, want %q", e.Component, "runtime.k8s")
	}
	if e.Category != "agent" {
		t.Errorf("Category = %q, want %q", e.Category, "agent")
	}
	if e.Resource != "task:event-test" {
		t.Errorf("Resource = %q, want %q", e.Resource, "task:event-test")
	}
	if e.Action != "job_create" {
		t.Errorf("Action = %q, want %q", e.Action, "job_create")
	}
	if e.Outcome != "success" {
		t.Errorf("Outcome = %q, want %q", e.Outcome, "success")
	}
	if e.Severity != "info" {
		t.Errorf("Severity = %q, want %q", e.Severity, "info")
	}
	if e.Attrs["job_name"] != "fracta-agent-event-test" {
		t.Errorf("Attrs[job_name] = %q, want %q", e.Attrs["job_name"], "fracta-agent-event-test")
	}
	if e.Attrs["namespace"] != "test-ns" {
		t.Errorf("Attrs[namespace] = %q, want %q", e.Attrs["namespace"], "test-ns")
	}
	if e.Attrs["model"] != "claude-sonnet" {
		t.Errorf("Attrs[model] = %q, want %q", e.Attrs["model"], "claude-sonnet")
	}
}

func TestKubernetesBackend_NoEventWithoutBus(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
	})
	// No SetEventBus call — should not panic

	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{ID: "no-bus-test"})
	if err != nil {
		t.Fatalf("Spawn without bus: %v", err)
	}
}

// --- buildInitScript tests ---

func TestBuildInitScript_Claude(t *testing.T) {
	artifacts := []WorkspaceArtifact{
		{ConfigMapKey: "dot-claude--settings.json", DestPath: ".claude/settings.json"},
		{ConfigMapKey: "dot-mcp.json", DestPath: ".mcp.json"},
		{ConfigMapKey: "dot-fracta--user-settings.json", DestPath: ".fracta/user-settings.json"},
		{ConfigMapKey: "CLAUDE.md", DestPath: "CLAUDE.md"},
	}

	script := buildInitScript("/workspace/agents/test", artifacts)

	// Must create parent dirs for nested paths.
	if !strings.Contains(script, "mkdir -p") {
		t.Error("script should contain mkdir -p for nested dirs")
	}
	if !strings.Contains(script, "/workspace/agents/test/.claude") {
		t.Error("script should mkdir .claude dir")
	}
	if !strings.Contains(script, "/workspace/agents/test/.fracta") {
		t.Error("script should mkdir .fracta dir")
	}

	// Must copy each artifact from ConfigMap mount to dest.
	for _, a := range artifacts {
		cpCmd := fmt.Sprintf("cp /etc/fracta/%s /workspace/agents/test/%s", a.ConfigMapKey, a.DestPath)
		if !strings.Contains(script, cpCmd) {
			t.Errorf("script missing cp for %s: %s", a.ConfigMapKey, script)
		}
	}
}

func TestBuildInitScript_Codex(t *testing.T) {
	artifacts := []WorkspaceArtifact{
		{ConfigMapKey: "dot-codex--config.toml", DestPath: ".codex/config.toml"},
		{ConfigMapKey: "AGENTS.md", DestPath: "AGENTS.md"},
	}

	script := buildInitScript("/workspace/agents/codex-agent", artifacts)

	// Must create .codex dir.
	if !strings.Contains(script, "/workspace/agents/codex-agent/.codex") {
		t.Error("script should mkdir .codex dir")
	}

	// Must cp both files.
	if !strings.Contains(script, "cp /etc/fracta/dot-codex--config.toml /workspace/agents/codex-agent/.codex/config.toml") {
		t.Errorf("script missing codex config.toml cp: %s", script)
	}
	if !strings.Contains(script, "cp /etc/fracta/AGENTS.md /workspace/agents/codex-agent/AGENTS.md") {
		t.Errorf("script missing AGENTS.md cp: %s", script)
	}
}

func TestBuildInitScript_OpenCode(t *testing.T) {
	artifacts := []WorkspaceArtifact{
		{ConfigMapKey: "opencode.json", DestPath: "opencode.json"},
		{ConfigMapKey: "AGENTS.md", DestPath: "AGENTS.md"},
	}

	script := buildInitScript("/workspace/agents/oc-agent", artifacts)

	// Flat files still need workDir created.
	if !strings.Contains(script, "mkdir -p /workspace/agents/oc-agent") {
		t.Errorf("script should create workDir for flat files: %s", script)
	}

	if !strings.Contains(script, "cp /etc/fracta/opencode.json /workspace/agents/oc-agent/opencode.json") {
		t.Errorf("script missing opencode.json cp: %s", script)
	}
	if !strings.Contains(script, "cp /etc/fracta/AGENTS.md /workspace/agents/oc-agent/AGENTS.md") {
		t.Errorf("script missing AGENTS.md cp: %s", script)
	}
}

func TestBuildInitScript_FlatAndNested(t *testing.T) {
	artifacts := []WorkspaceArtifact{
		{ConfigMapKey: "AGENTS.md", DestPath: "AGENTS.md"},
		{ConfigMapKey: "dot-codex--config.toml", DestPath: ".codex/config.toml"},
	}

	script := buildInitScript("/workspace/agents/mixed", artifacts)

	// Both workDir and nested subdir should be created, sorted.
	expected := "mkdir -p /workspace/agents/mixed /workspace/agents/mixed/.codex"
	if !strings.HasPrefix(script, expected) {
		t.Errorf("script should start with sorted mkdir:\nwant: %s\n got: %s", expected, script)
	}
}

func TestBuildInitScript_Empty(t *testing.T) {
	script := buildInitScript("/workspace/agents/empty", nil)
	if script != "" {
		t.Errorf("empty artifacts should produce empty script, got %q", script)
	}
}

func TestSpawn_CodexWorkspaceArtifacts(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
	})
	ctx := context.Background()

	codexArtifacts := []WorkspaceArtifact{
		{ConfigMapKey: "dot-codex--config.toml", DestPath: ".codex/config.toml", Content: "[mcp]\nurl = \"http://gw\""},
		{ConfigMapKey: "AGENTS.md", DestPath: "AGENTS.md", Content: "# Codex Agent"},
	}

	_, err := b.Spawn(ctx, SpawnOpts{
		ID:             "codex-k8s-test",
		WorkspaceFiles: codexArtifacts,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Verify ConfigMap has the right keys.
	cm, err := client.CoreV1().ConfigMaps("test-ns").Get(ctx, "fracta-config-codex-k8s-test", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("ConfigMap not found: %v", err)
	}
	if cm.Data["dot-codex--config.toml"] != "[mcp]\nurl = \"http://gw\"" {
		t.Errorf("ConfigMap[dot-codex--config.toml] wrong content")
	}
	if cm.Data["AGENTS.md"] != "# Codex Agent" {
		t.Errorf("ConfigMap[AGENTS.md] wrong content")
	}

	// Verify initContainer has .codex mkdir and correct cp commands.
	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-codex-k8s-test", metav1.GetOptions{})
	init := job.Spec.Template.Spec.InitContainers[0]
	script := init.Command[2]

	if !strings.Contains(script, ".codex") {
		t.Errorf("initContainer script should mkdir .codex: %s", script)
	}
	if !strings.Contains(script, "dot-codex--config.toml") {
		t.Errorf("initContainer script should cp dot-codex--config.toml: %s", script)
	}
}

func TestKillStreamPod_CleansUpConfigMapAndSecret(t *testing.T) {
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{})
	id := "stream-cleanup-test"

	// Pre-create pod, ConfigMap, and Secret (simulating SpawnStreamPod).
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("fracta-stream-%s", id), Namespace: "test-ns"}}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("fracta-config-%s", id), Namespace: "test-ns"}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("fracta-auth-%s", id), Namespace: "test-ns"}}

	if _, err := client.CoreV1().Pods("test-ns").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().ConfigMaps("test-ns").Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Secrets("test-ns").Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	if err := b.KillStreamPod(ctx, id); err != nil {
		t.Fatalf("KillStreamPod: %v", err)
	}

	// All three resources should be gone.
	_, err := client.CoreV1().Pods("test-ns").Get(ctx, pod.Name, metav1.GetOptions{})
	if err == nil {
		t.Error("pod still exists after KillStreamPod")
	}
	_, err = client.CoreV1().ConfigMaps("test-ns").Get(ctx, cm.Name, metav1.GetOptions{})
	if err == nil {
		t.Error("ConfigMap still exists after KillStreamPod")
	}
	_, err = client.CoreV1().Secrets("test-ns").Get(ctx, secret.Name, metav1.GetOptions{})
	if err == nil {
		t.Error("Secret still exists after KillStreamPod")
	}
}

func TestKillStreamPod_PodGone_StillCleansUpResources(t *testing.T) {
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{})
	id := "orphan-cleanup-test"

	// Only create ConfigMap and Secret — pod is already gone (evicted/manual delete).
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("fracta-config-%s", id), Namespace: "test-ns"}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("fracta-auth-%s", id), Namespace: "test-ns"}}

	if _, err := client.CoreV1().ConfigMaps("test-ns").Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Secrets("test-ns").Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	err := b.KillStreamPod(ctx, id)
	if err == nil {
		t.Fatal("expected ErrNotFound for missing pod")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got: %v", err)
	}

	// Resources should still be cleaned up despite pod being gone.
	_, err = client.CoreV1().ConfigMaps("test-ns").Get(ctx, cm.Name, metav1.GetOptions{})
	if err == nil {
		t.Error("ConfigMap still exists — should have been cleaned up even though pod was missing")
	}
	_, err = client.CoreV1().Secrets("test-ns").Get(ctx, secret.Name, metav1.GetOptions{})
	if err == nil {
		t.Error("Secret still exists — should have been cleaned up even though pod was missing")
	}
}

// --- spec-42 §8: extra_volumes / extra_volume_mounts plumbing ---

func makeAuthHelpersExtras() ([]corev1.Volume, []corev1.VolumeMount) {
	mode := int32(0755)
	vols := []corev1.Volume{
		{
			Name: "auth-helpers",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "fracta-auth-helpers"},
					DefaultMode:          &mode,
				},
			},
		},
	}
	mounts := []corev1.VolumeMount{
		{Name: "auth-helpers", MountPath: "/opt/fracta/auth-helpers", ReadOnly: true},
	}
	return vols, mounts
}

// TestSpawn_WithExtraVolumes confirms operator-supplied extras land on the
// spawned Job's pod spec: present in podSpec.Volumes and the main container's
// VolumeMounts, absent from the workspace-init initContainer (spec-42 §8).
func TestSpawn_WithExtraVolumes(t *testing.T) {
	vols, mounts := makeAuthHelpersExtras()
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image:             "fracta/agent:latest",
		ExtraVolumes:      vols,
		ExtraVolumeMounts: mounts,
		// Force an initContainer path by injecting a workspace file.
	})

	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{
		ID: "ev-test",
		WorkspaceFiles: []WorkspaceArtifact{
			{ConfigMapKey: "f.txt", DestPath: "f.txt", Content: "x"},
		},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	job, err := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-ev-test", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	podSpec := job.Spec.Template.Spec

	// extras present on pod volumes
	if !containsVolumeNamed(podSpec.Volumes, "auth-helpers") {
		t.Errorf("pod volumes missing 'auth-helpers'; got: %v", volumeNames(podSpec.Volumes))
	}
	// extras present on main container mounts
	if !containsMountNamed(podSpec.Containers[0].VolumeMounts, "auth-helpers") {
		t.Errorf("main container mounts missing 'auth-helpers'; got: %v", mountNames(podSpec.Containers[0].VolumeMounts))
	}
	// extras absent from initContainer
	if len(podSpec.InitContainers) == 0 {
		t.Fatalf("expected an initContainer (workspace file injection)")
	}
	if containsMountNamed(podSpec.InitContainers[0].VolumeMounts, "auth-helpers") {
		t.Errorf("initContainer mounts must NOT include 'auth-helpers'; got: %v", mountNames(podSpec.InitContainers[0].VolumeMounts))
	}
}

// TestSpawn_ExtraVolumesNoEffectWhenEmpty confirms a Spawn without extras
// produces a Job with no auth-helpers leakage.
func TestSpawn_ExtraVolumesNoEffectWhenEmpty(t *testing.T) {
	client := fake.NewSimpleClientset()
	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image: "fracta/agent:latest",
	})
	ctx := context.Background()
	_, err := b.Spawn(ctx, SpawnOpts{ID: "no-extras"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	job, _ := client.BatchV1().Jobs("test-ns").Get(ctx, "fracta-agent-no-extras", metav1.GetOptions{})
	podSpec := job.Spec.Template.Spec
	if containsVolumeNamed(podSpec.Volumes, "auth-helpers") {
		t.Error("pod volumes contain 'auth-helpers' but no extras were configured")
	}
	if containsMountNamed(podSpec.Containers[0].VolumeMounts, "auth-helpers") {
		t.Error("container mounts contain 'auth-helpers' but no extras were configured")
	}
}

// TestSpawnStreamPod_WithExtraVolumes confirms extras land on long-lived
// stream pods too (parity with Spawn). The fake clientset never marks pods
// Ready on its own — we install a Create reactor that pre-populates Ready
// status so SpawnStreamPod's wait loop returns immediately.
func TestSpawnStreamPod_WithExtraVolumes(t *testing.T) {
	vols, mounts := makeAuthHelpersExtras()
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtimeapi.Object, error) {
		ca := action.(ktesting.CreateAction)
		pod := ca.GetObject().(*corev1.Pod)
		pod.Status.Phase = corev1.PodRunning
		pod.Status.PodIP = "10.0.0.42"
		pod.Status.Conditions = []corev1.PodCondition{
			{Type: corev1.PodReady, Status: corev1.ConditionTrue},
		}
		return false, pod, nil // false → let default tracker also store the object
	})

	b := NewKubernetesBackend(client, "test-ns", KubernetesJobConfig{
		Image:             "fracta/agent:latest",
		ExtraVolumes:      vols,
		ExtraVolumeMounts: mounts,
	})

	ctx := context.Background()
	_, err := b.SpawnStreamPod(ctx, StreamPodOpts{
		SpawnOpts: SpawnOpts{ID: "stream-ev", Command: "claude"},
		Port:      8080,
	})
	if err != nil {
		t.Fatalf("SpawnStreamPod: %v", err)
	}

	pod, err := client.CoreV1().Pods("test-ns").Get(ctx, "fracta-stream-stream-ev", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pod: %v", err)
	}
	podSpec := pod.Spec

	if !containsVolumeNamed(podSpec.Volumes, "auth-helpers") {
		t.Errorf("stream pod volumes missing 'auth-helpers'; got: %v", volumeNames(podSpec.Volumes))
	}
	if !containsMountNamed(podSpec.Containers[0].VolumeMounts, "auth-helpers") {
		t.Errorf("stream pod main mounts missing 'auth-helpers'; got: %v", mountNames(podSpec.Containers[0].VolumeMounts))
	}
}

func containsVolumeNamed(vs []corev1.Volume, name string) bool {
	for _, v := range vs {
		if v.Name == name {
			return true
		}
	}
	return false
}

func containsMountNamed(ms []corev1.VolumeMount, name string) bool {
	for _, m := range ms {
		if m.Name == name {
			return true
		}
	}
	return false
}

func volumeNames(vs []corev1.Volume) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Name)
	}
	return out
}

func mountNames(ms []corev1.VolumeMount) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
} 
