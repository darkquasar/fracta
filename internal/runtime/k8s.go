package runtime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/model"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

const (
	defaultNamespace         = "fracta"
	defaultPVCMountPath      = "/workspace"
	configMapMountPath       = "/etc/fracta"
	configMapKey             = "agent-config.yaml"
	defaultAuthSecretMount   = "/var/run/fracta-auth"
	labelApp                 = "fracta"
	labelAgentID             = "fracta-agent-id"
)

var _ Backend = (*KubernetesBackend)(nil)
var _ StreamBackend = (*KubernetesBackend)(nil)

// KubernetesJobConfig holds cluster-level defaults for agent Jobs.
// These are injected at construction time and can be partially overridden
// per-spawn via SpawnOpts. Contains ONLY cluster-level fields — no auth,
// model, or provider fields.
type KubernetesJobConfig struct {
	Image           string
	ImagePullPolicy string // "Never", "IfNotPresent", "Always"
	ServiceAccount  string
	PVC             string
	PVCMountPath    string // default: "/workspace"
	Labels          map[string]string
	Annotations     map[string]string
	Tolerations     []corev1.Toleration
	NodeSelector    map[string]string
	Resources       ResourceRequirements
	JobTTLSeconds   int32
}

// KubernetesBackend submits agents as K8s Jobs via client-go.
type KubernetesBackend struct {
	client    kubernetes.Interface
	namespace string
	config    KubernetesJobConfig
	events    events.Bus // optional; emits job_create on spawn
}

// NewKubernetesBackend creates a KubernetesBackend with the given clientset and config.
// If namespace is empty, "fracta" is used.
func NewKubernetesBackend(client kubernetes.Interface, namespace string, config KubernetesJobConfig) *KubernetesBackend {
	if namespace == "" {
		namespace = defaultNamespace
	}
	return &KubernetesBackend{
		client:    client,
		namespace: namespace,
		config:    config,
	}
}

// SetEventBus attaches an event bus to the K8s backend for emitting lifecycle events.
func (b *KubernetesBackend) SetEventBus(bus events.Bus) {
	b.events = bus
}

// NewEventRecorder returns a K8sEventRecorder that writes Kubernetes Events
// via the backend's clientset and namespace. Called by the control plane's
// attachK8sEventSink to add a K8sEventSink to the FanoutBus at construction time.
func (b *KubernetesBackend) NewEventRecorder() events.K8sEventRecorder {
	return &clientGoEventRecorder{
		client:    b.client,
		namespace: b.namespace,
	}
}

// clientGoEventRecorder implements events.K8sEventRecorder using client-go.
type clientGoEventRecorder struct {
	client    kubernetes.Interface
	namespace string
}

// Record creates a Kubernetes Event via the Events API.
func (r *clientGoEventRecorder) Record(eventType, reason, message string) {
	now := metav1.Now()
	ev := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "fracta-",
			Namespace:    r.namespace,
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:      "Namespace",
			Name:      r.namespace,
			Namespace: r.namespace,
		},
		Reason:         reason,
		Message:        message,
		Type:           eventType,
		FirstTimestamp: now,
		LastTimestamp:  now,
		Source: corev1.EventSource{
			Component: "fracta",
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = r.client.CoreV1().Events(r.namespace).Create(ctx, ev, metav1.CreateOptions{})
}

// k8sHandle implements AgentHandle for a K8s Job.
type k8sHandle struct {
	ctx       context.Context // from Spawn — propagates cancellation/shutdown
	client    kubernetes.Interface
	namespace string
	jobName   string
	startTime time.Time
}

func (h *k8sHandle) Wait() error {
	// Poll until the Job reaches a terminal condition or context is cancelled.
	for {
		select {
		case <-h.ctx.Done():
			return h.ctx.Err()
		default:
		}

		job, err := h.client.BatchV1().Jobs(h.namespace).Get(h.ctx, h.jobName, metav1.GetOptions{})
		if err != nil {
			if h.ctx.Err() != nil {
				return h.ctx.Err()
			}
			return fmt.Errorf("runtime/k8s: getting job %s: %w", h.jobName, err)
		}

		for _, cond := range job.Status.Conditions {
			if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
				return nil
			}
			if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
				return fmt.Errorf("runtime/k8s: job %s failed: %s", h.jobName, cond.Message)
			}
		}

		select {
		case <-h.ctx.Done():
			return h.ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (h *k8sHandle) Output() io.Reader {
	// Fetch logs from the first pod of the Job.
	pods, err := h.client.CoreV1().Pods(h.namespace).List(h.ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s", h.jobName),
	})
	if err != nil || len(pods.Items) == 0 {
		return bytes.NewReader(nil)
	}

	logStream, err := h.client.CoreV1().Pods(h.namespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{}).Stream(h.ctx)
	if err != nil {
		return bytes.NewReader(nil)
	}

	var buf bytes.Buffer
	io.Copy(&buf, logStream)
	logStream.Close()
	return &buf
}

func (h *k8sHandle) ExitCode() int {
	pods, err := h.client.CoreV1().Pods(h.namespace).List(h.ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s", h.jobName),
	})
	if err != nil || len(pods.Items) == 0 {
		return -1
	}

	for _, cs := range pods.Items[0].Status.ContainerStatuses {
		if cs.State.Terminated != nil {
			return int(cs.State.Terminated.ExitCode)
		}
	}
	return -1
}

func (h *k8sHandle) StartTime() time.Time {
	return h.startTime
}

// Spawn creates a K8s Job for the agent. Config defaults are used for all
// cluster-level settings; SpawnOpts can override Image and Resources.
func (b *KubernetesBackend) Spawn(ctx context.Context, opts SpawnOpts) (AgentHandle, error) {
	// Resolve image: SpawnOpts > config > error
	image := opts.Image
	if image == "" {
		image = b.config.Image
	}
	if image == "" {
		return nil, fmt.Errorf("runtime/k8s: Image is required")
	}

	jobName := fmt.Sprintf("fracta-agent-%s", opts.ID)
	ns := opts.Namespace
	if ns == "" {
		ns = b.namespace
	}

	var backoffLimit int32 = 0

	// Resolve TTL: config > default 300
	ttl := b.config.JobTTLSeconds
	if ttl == 0 {
		ttl = 300
	}

	// Resolve resources: SpawnOpts > config
	resources := b.config.Resources
	if opts.Resources != nil {
		resources = *opts.Resources
	}

	// Build env vars: orchestrator env first, then HostEnv overwrites on collision.
	envVars := mergeEnv(opts.Env, opts.HostEnv)

	// Merge labels: config defaults + standard labels
	labels := mergeStringMaps(b.config.Labels, map[string]string{
		"app":        labelApp,
		labelAgentID: opts.ID,
	})

	// Resolve PVC mount path
	mountPath := b.config.PVCMountPath
	if mountPath == "" {
		mountPath = defaultPVCMountPath
	}

	// K8s images use ENTRYPOINT ["/entrypoint.sh"] which handles auth setup
	// (copying user-settings.json → ~/.claude/settings.json) and strategy sidecar
	// startup before exec'ing "$@". We pass the command as Args so the entrypoint
	// runs first, then execs into the agent CLI.
	container := corev1.Container{
		Name:       "agent",
		Image:      image,
		Args:       append([]string{opts.Command}, opts.Args...),
		Env:        envVars,
		WorkingDir: fmt.Sprintf("%s/agents/%s", mountPath, opts.ID),
	}

	// ImagePullPolicy
	if b.config.ImagePullPolicy != "" {
		container.ImagePullPolicy = corev1.PullPolicy(b.config.ImagePullPolicy)
	}

	// Resources
	if resources.CPURequest != "" || resources.CPULimit != "" || resources.MemoryRequest != "" || resources.MemoryLimit != "" {
		container.Resources = buildResourceRequirements(&resources)
	}

	// PVC volume mount
	var volumeMounts []corev1.VolumeMount
	if b.config.PVC != "" {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "workspace",
			MountPath: mountPath,
		})
	}

	// ConfigMap volume mount (config + workspace files injection)
	hasConfigMap := opts.ConfigSnapshot != "" || len(opts.WorkspaceFiles) > 0
	configMapName := ""
	if hasConfigMap {
		configMapName = fmt.Sprintf("fracta-config-%s", opts.ID)
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "agent-config",
			MountPath: configMapMountPath,
			ReadOnly:  true,
		})
	}

	// emptyDir volume mount: ephemeral scratch workspace when no PVC provides one.
	// Used in ConfigMap+emptyDir mode (workspace files injected via initContainer).
	if len(opts.WorkspaceFiles) > 0 && b.config.PVC == "" {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "workspace",
			MountPath: mountPath,
		})
	}

	// Auth secret volume mount (host-seeded token)
	authSecretName := ""
	if len(opts.AuthSecretData) > 0 {
		authSecretName = fmt.Sprintf("fracta-auth-%s", opts.ID)
		authMount := opts.AuthSecretMountPath
		if authMount == "" {
			authMount = defaultAuthSecretMount
		}
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "auth-secret",
			MountPath: authMount,
			ReadOnly:  true,
		})
	}

	container.VolumeMounts = volumeMounts

	podSpec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers:    []corev1.Container{container},
	}

	// ServiceAccount
	if b.config.ServiceAccount != "" {
		podSpec.ServiceAccountName = b.config.ServiceAccount
	}

	// Volumes
	var volumes []corev1.Volume
	if b.config.PVC != "" {
		volumes = append(volumes, corev1.Volume{
			Name: "workspace",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: b.config.PVC,
				},
			},
		})
	} else if len(opts.WorkspaceFiles) > 0 {
		// emptyDir: ephemeral scratch workspace (ConfigMap+emptyDir mode, no PVC).
		volumes = append(volumes, corev1.Volume{
			Name: "workspace",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
	}
	if configMapName != "" {
		volumes = append(volumes, corev1.Volume{
			Name: "agent-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: configMapName,
					},
				},
			},
		})
	}
	if authSecretName != "" {
		volumes = append(volumes, corev1.Volume{
			Name: "auth-secret",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: authSecretName,
				},
			},
		})
	}
	podSpec.Volumes = volumes

	// initContainer: distribute workspace files from ConfigMap to expected paths.
	if len(opts.WorkspaceFiles) > 0 {
		initScript := buildInitScript(container.WorkingDir, opts.WorkspaceFiles)
		initContainer := corev1.Container{
			Name:    "workspace-init",
			Image:   image,
			Command: []string{"sh", "-c", initScript},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "agent-config", MountPath: configMapMountPath, ReadOnly: true},
			},
		}
		// Inherit ImagePullPolicy from main container so initContainer
		// uses the locally loaded image instead of trying to pull from registry.
		initContainer.ImagePullPolicy = container.ImagePullPolicy
		// initContainer needs workspace volume to write into it.
		for _, vm := range volumeMounts {
			if vm.Name == "workspace" {
				initContainer.VolumeMounts = append(initContainer.VolumeMounts, vm)
				break
			}
		}
		podSpec.InitContainers = []corev1.Container{initContainer}
	}

	// Tolerations
	if len(b.config.Tolerations) > 0 {
		podSpec.Tolerations = b.config.Tolerations
	}

	// NodeSelector
	if len(b.config.NodeSelector) > 0 {
		podSpec.NodeSelector = b.config.NodeSelector
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:        jobName,
			Namespace:   ns,
			Labels:      labels,
			Annotations: b.config.Annotations,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: b.config.Annotations,
				},
				Spec: podSpec,
			},
		},
	}

	// Step 1: Create ConfigMap (before Job, no ownerReference yet)
	if configMapName != "" {
		cmData := make(map[string]string)
		if opts.ConfigSnapshot != "" {
			cmData[configMapKey] = opts.ConfigSnapshot
		}
		for _, wf := range opts.WorkspaceFiles {
			cmData[wf.ConfigMapKey] = wf.Content
		}
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: ns,
				Labels:    labels,
			},
			Data: cmData,
		}
		if _, err := b.client.CoreV1().ConfigMaps(ns).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("runtime/k8s: creating config map %s: %w", configMapName, err)
		}
	}

	// Step 1b: Create auth Secret (before Job, no ownerReference yet)
	if authSecretName != "" {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      authSecretName,
				Namespace: ns,
				Labels:    labels,
			},
			Data: opts.AuthSecretData,
		}
		if _, err := b.client.CoreV1().Secrets(ns).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
			// Cleanup orphaned ConfigMap before returning
			if configMapName != "" {
				_ = b.client.CoreV1().ConfigMaps(ns).Delete(ctx, configMapName, metav1.DeleteOptions{})
			}
			return nil, fmt.Errorf("runtime/k8s: creating auth secret %s: %w", authSecretName, err)
		}
	}

	// Step 2: Create Job
	createdJob, err := b.client.BatchV1().Jobs(ns).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		// Cleanup orphaned ConfigMap and Secret on Job creation failure
		if configMapName != "" {
			_ = b.client.CoreV1().ConfigMaps(ns).Delete(ctx, configMapName, metav1.DeleteOptions{})
		}
		if authSecretName != "" {
			_ = b.client.CoreV1().Secrets(ns).Delete(ctx, authSecretName, metav1.DeleteOptions{})
		}
		return nil, fmt.Errorf("runtime/k8s: creating job %s: %w", jobName, err)
	}

	// Step 3: Patch ConfigMap and Secret with ownerReference → Job
	ownerRef := metav1.OwnerReference{
		APIVersion: "batch/v1",
		Kind:       "Job",
		Name:       createdJob.Name,
		UID:        createdJob.UID,
		Controller: boolPtr(true),
	}
	if configMapName != "" {
		cm, err := b.client.CoreV1().ConfigMaps(ns).Get(ctx, configMapName, metav1.GetOptions{})
		if err == nil {
			cm.OwnerReferences = []metav1.OwnerReference{ownerRef}
			_, _ = b.client.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{})
		}
	}
	if authSecretName != "" {
		secret, err := b.client.CoreV1().Secrets(ns).Get(ctx, authSecretName, metav1.GetOptions{})
		if err == nil {
			secret.OwnerReferences = []metav1.OwnerReference{ownerRef}
			_, _ = b.client.CoreV1().Secrets(ns).Update(ctx, secret, metav1.UpdateOptions{})
		}
	}
	// ownerReference patch failures are non-critical — resources will be
	// cleaned up by Job TTL or a periodic janitor.

	// Emit job_create event.
	if b.events != nil {
		e := events.Info("runtime.k8s", "job_create")
		e.Category = "agent"
		e.Resource = "task:" + opts.ID
		e.Outcome = "success"
		e.Attrs = map[string]string{
			"job_name":  jobName,
			"namespace": ns,
		}
		if opts.Model != "" {
			e.Attrs["model"] = opts.Model
		}
		b.events.Emit(ctx, e)
	}

	return &k8sHandle{
		ctx:       ctx,
		client:    b.client,
		namespace: ns,
		jobName:   jobName,
		startTime: time.Now(),
	}, nil
}

// Kill deletes a K8s Job and its pods.
func (b *KubernetesBackend) Kill(ctx context.Context, id string) error {
	jobName := fmt.Sprintf("fracta-agent-%s", id)
	propagation := metav1.DeletePropagationForeground

	err := b.client.BatchV1().Jobs(b.namespace).Delete(ctx, jobName, metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("runtime/k8s: job %s: %w", jobName, ErrNotFound)
		}
		return fmt.Errorf("runtime/k8s: deleting job %s: %w", jobName, err)
	}
	return nil
}

// Status checks the K8s Job status for an agent.
func (b *KubernetesBackend) Status(ctx context.Context, id string) (model.AgentStatus, error) {
	jobName := fmt.Sprintf("fracta-agent-%s", id)

	job, err := b.client.BatchV1().Jobs(b.namespace).Get(ctx, jobName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("runtime/k8s: getting job %s: %w", jobName, err)
	}

	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
			return model.StatusCompleted, nil
		}
		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			return model.StatusFailed, nil
		}
	}

	if job.Status.Active > 0 {
		return model.StatusRunning, nil
	}

	return model.StatusPending, nil
}

// Logs fetches recent log output from the pod backing an agent's Job.
// If tailLines > 0, only the last N lines are returned.
func (b *KubernetesBackend) Logs(ctx context.Context, id string, tailLines int) (string, error) {
	jobName := fmt.Sprintf("fracta-agent-%s", id)

	pods, err := b.client.CoreV1().Pods(b.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s", jobName),
	})
	if err != nil {
		return "", fmt.Errorf("runtime/k8s: listing pods for job %s: %w", jobName, err)
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("runtime/k8s: no pods found for job %s: %w", jobName, ErrNotFound)
	}

	logOpts := &corev1.PodLogOptions{}
	if tailLines > 0 {
		tl := int64(tailLines)
		logOpts.TailLines = &tl
	}

	logStream, err := b.client.CoreV1().Pods(b.namespace).GetLogs(pods.Items[0].Name, logOpts).Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("runtime/k8s: fetching logs for pod %s: %w", pods.Items[0].Name, err)
	}
	defer logStream.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, logStream); err != nil {
		return "", fmt.Errorf("runtime/k8s: reading logs for pod %s: %w", pods.Items[0].Name, err)
	}

	return buf.String(), nil
}

// --- Stream pod support (long-lived pods for streaming runtimes) ---

const (
	streamPodHealthTimeout = 60 * time.Second
	streamPodHealthDelay   = 500 * time.Millisecond
	labelStreamPod         = "fracta-stream-pod"
)

// SpawnStreamPod launches a persistent pod running the runtime's serve command.
// Unlike Spawn (which creates a Job), this creates a standalone Pod that stays
// alive across turns until explicitly killed via Kill().
func (b *KubernetesBackend) SpawnStreamPod(ctx context.Context, opts StreamPodOpts) (*StreamPodInfo, error) {
	image := opts.Image
	if image == "" {
		image = b.config.Image
	}
	if image == "" {
		return nil, fmt.Errorf("runtime/k8s: Image is required for stream pod")
	}

	podName := fmt.Sprintf("fracta-stream-%s", opts.ID)
	ns := opts.Namespace
	if ns == "" {
		ns = b.namespace
	}

	port := opts.Port
	if port == 0 {
		port = 8080
	}

	resources := b.config.Resources
	if opts.Resources != nil {
		resources = *opts.Resources
	}

	envVars := mergeEnv(opts.Env, opts.HostEnv)

	labels := mergeStringMaps(b.config.Labels, map[string]string{
		"app":          labelApp,
		labelAgentID:   opts.ID,
		labelStreamPod: "true",
	})

	mountPath := b.config.PVCMountPath
	if mountPath == "" {
		mountPath = defaultPVCMountPath
	}

	container := corev1.Container{
		Name:       "agent",
		Image:      image,
		Args:       append([]string{opts.Command}, opts.Args...),
		Env:        envVars,
		WorkingDir: fmt.Sprintf("%s/agents/%s", mountPath, opts.ID),
		Ports: []corev1.ContainerPort{
			{
				Name:          "serve",
				ContainerPort: port,
				Protocol:      corev1.ProtocolTCP,
			},
		},
	}

	if b.config.ImagePullPolicy != "" {
		container.ImagePullPolicy = corev1.PullPolicy(b.config.ImagePullPolicy)
	}

	if resources.CPURequest != "" || resources.CPULimit != "" || resources.MemoryRequest != "" || resources.MemoryLimit != "" {
		container.Resources = buildResourceRequirements(&resources)
	}

	// Health probe: TCP for Codex (no HTTP endpoint), HTTP for OpenCode.
	switch opts.RuntimeType {
	case "opencode":
		container.ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/global/health",
					Port: intstr.FromInt32(port),
				},
			},
			InitialDelaySeconds: 3,
			PeriodSeconds:       5,
		}
	default: // codex and others: TCP socket probe
		container.ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt32(port),
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       10,
		}
	}

	// Volume mounts.
	var volumeMounts []corev1.VolumeMount
	var volumes []corev1.Volume

	if b.config.PVC != "" {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name: "workspace", MountPath: mountPath,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "workspace",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: b.config.PVC,
				},
			},
		})
	} else {
		// emptyDir for scratch workspace.
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name: "workspace", MountPath: mountPath,
		})
		volumes = append(volumes, corev1.Volume{
			Name:         "workspace",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
	}

	// ConfigMap volume.
	hasConfigMap := opts.ConfigSnapshot != "" || len(opts.WorkspaceFiles) > 0
	configMapName := ""
	if hasConfigMap {
		configMapName = fmt.Sprintf("fracta-config-%s", opts.ID)
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name: "agent-config", MountPath: configMapMountPath, ReadOnly: true,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "agent-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
				},
			},
		})
	}

	// Auth secret volume.
	authSecretName := ""
	if len(opts.AuthSecretData) > 0 {
		authSecretName = fmt.Sprintf("fracta-auth-%s", opts.ID)
		authMount := opts.AuthSecretMountPath
		if authMount == "" {
			authMount = defaultAuthSecretMount
		}
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name: "auth-secret", MountPath: authMount, ReadOnly: true,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "auth-secret",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: authSecretName},
			},
		})
	}

	container.VolumeMounts = volumeMounts

	podSpec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers:    []corev1.Container{container},
		Volumes:       volumes,
	}

	if b.config.ServiceAccount != "" {
		podSpec.ServiceAccountName = b.config.ServiceAccount
	}
	if len(b.config.Tolerations) > 0 {
		podSpec.Tolerations = b.config.Tolerations
	}
	if len(b.config.NodeSelector) > 0 {
		podSpec.NodeSelector = b.config.NodeSelector
	}

	// initContainer for workspace file distribution.
	if len(opts.WorkspaceFiles) > 0 {
		initScript := buildInitScript(container.WorkingDir, opts.WorkspaceFiles)
		initContainer := corev1.Container{
			Name:    "workspace-init",
			Image:   image,
			Command: []string{"sh", "-c", initScript},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "agent-config", MountPath: configMapMountPath, ReadOnly: true},
			},
			ImagePullPolicy: container.ImagePullPolicy,
		}
		for _, vm := range volumeMounts {
			if vm.Name == "workspace" {
				initContainer.VolumeMounts = append(initContainer.VolumeMounts, vm)
				break
			}
		}
		podSpec.InitContainers = []corev1.Container{initContainer}
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   ns,
			Labels:      labels,
			Annotations: b.config.Annotations,
		},
		Spec: podSpec,
	}

	// Create ConfigMap if needed.
	if configMapName != "" {
		cmData := make(map[string]string)
		if opts.ConfigSnapshot != "" {
			cmData[configMapKey] = opts.ConfigSnapshot
		}
		for _, wf := range opts.WorkspaceFiles {
			cmData[wf.ConfigMapKey] = wf.Content
		}
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: configMapName, Namespace: ns, Labels: labels,
			},
			Data: cmData,
		}
		if _, err := b.client.CoreV1().ConfigMaps(ns).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("runtime/k8s: creating config map %s: %w", configMapName, err)
		}
	}

	// Create auth Secret if needed.
	if authSecretName != "" {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: authSecretName, Namespace: ns, Labels: labels,
			},
			Data: opts.AuthSecretData,
		}
		if _, err := b.client.CoreV1().Secrets(ns).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
			if configMapName != "" {
				_ = b.client.CoreV1().ConfigMaps(ns).Delete(ctx, configMapName, metav1.DeleteOptions{})
			}
			return nil, fmt.Errorf("runtime/k8s: creating auth secret %s: %w", authSecretName, err)
		}
	}

	// Create the Pod.
	createdPod, err := b.client.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		if configMapName != "" {
			_ = b.client.CoreV1().ConfigMaps(ns).Delete(ctx, configMapName, metav1.DeleteOptions{})
		}
		if authSecretName != "" {
			_ = b.client.CoreV1().Secrets(ns).Delete(ctx, authSecretName, metav1.DeleteOptions{})
		}
		return nil, fmt.Errorf("runtime/k8s: creating stream pod %s: %w", podName, err)
	}

	// Wait for the pod to become Ready.
	if err := b.waitForPodReady(ctx, ns, podName); err != nil {
		// Cleanup on failure.
		propagation := metav1.DeletePropagationForeground
		_ = b.client.CoreV1().Pods(ns).Delete(ctx, podName, metav1.DeleteOptions{PropagationPolicy: &propagation})
		if configMapName != "" {
			_ = b.client.CoreV1().ConfigMaps(ns).Delete(ctx, configMapName, metav1.DeleteOptions{})
		}
		if authSecretName != "" {
			_ = b.client.CoreV1().Secrets(ns).Delete(ctx, authSecretName, metav1.DeleteOptions{})
		}
		return nil, fmt.Errorf("runtime/k8s: stream pod %s not ready: %w", podName, err)
	}

	// Get the pod IP for connection metadata.
	readyPod, err := b.client.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("runtime/k8s: getting stream pod IP: %w", err)
	}
	podIP := readyPod.Status.PodIP
	if podIP == "" {
		podIP = createdPod.Status.PodIP
	}

	info := &StreamPodInfo{PodName: podName}

	switch opts.RuntimeType {
	case "codex":
		info.CodexWebSocket = &WebSocketTransport{
			URL:       fmt.Sprintf("ws://%s:%d", podIP, port),
			AuthToken: opts.WebSocketAuthToken,
		}
	case "opencode":
		info.OpenCodeHTTP = &HTTPTransport{
			BaseURL:  fmt.Sprintf("http://%s:%d", podIP, port),
			Password: opts.ServePassword,
		}
	default:
		// Unknown runtime — default to TCP.
		info.CodexWebSocket = &WebSocketTransport{
			URL: fmt.Sprintf("ws://%s:%d", podIP, port),
		}
	}

	// Emit lifecycle event.
	if b.events != nil {
		e := events.Info("runtime.k8s", "stream_pod_create")
		e.Category = "agent"
		e.Resource = "task:" + opts.ID
		e.Outcome = "success"
		e.Attrs = map[string]string{
			"pod_name":     podName,
			"namespace":    ns,
			"runtime_type": opts.RuntimeType,
			"pod_ip":       podIP,
		}
		b.events.Emit(ctx, e)
	}

	return info, nil
}

// waitForPodReady polls until a pod's Ready condition is True or timeout.
func (b *KubernetesBackend) waitForPodReady(ctx context.Context, namespace, podName string) error {
	deadline := time.Now().Add(streamPodHealthTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		pod, err := b.client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			time.Sleep(streamPodHealthDelay)
			continue
		}

		// Check for terminal failure.
		if pod.Status.Phase == corev1.PodFailed {
			return fmt.Errorf("pod entered Failed phase")
		}

		// Check Ready condition.
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return nil
			}
		}

		time.Sleep(streamPodHealthDelay)
	}
	return fmt.Errorf("timed out after %v waiting for pod to become ready", streamPodHealthTimeout)
}

// KillStreamPod deletes a persistent stream pod by agent ID.
// Uses the same naming convention as SpawnStreamPod.
func (b *KubernetesBackend) KillStreamPod(ctx context.Context, id string) error {
	podName := fmt.Sprintf("fracta-stream-%s", id)
	ns := b.namespace
	propagation := metav1.DeletePropagationForeground

	podErr := b.client.CoreV1().Pods(ns).Delete(ctx, podName, metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	})

	// Best-effort cleanup of associated ConfigMap and Secret.
	// Always attempt this regardless of pod deletion result — the pod may
	// already be gone (evicted, manual delete) while resources remain.
	configMapName := fmt.Sprintf("fracta-config-%s", id)
	if err := b.client.CoreV1().ConfigMaps(ns).Delete(ctx, configMapName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		_ = err
	}
	authSecretName := fmt.Sprintf("fracta-auth-%s", id)
	if err := b.client.CoreV1().Secrets(ns).Delete(ctx, authSecretName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		_ = err
	}

	if podErr != nil {
		if apierrors.IsNotFound(podErr) {
			return fmt.Errorf("runtime/k8s: stream pod %s: %w", podName, ErrNotFound)
		}
		return fmt.Errorf("runtime/k8s: deleting stream pod %s: %w", podName, podErr)
	}
	return nil
}

// parseEnvStrings converts "KEY=VALUE" strings to corev1.EnvVar.
func parseEnvStrings(envs []string) []corev1.EnvVar {
	vars := make([]corev1.EnvVar, 0, len(envs))
	for _, e := range envs {
		for i := 0; i < len(e); i++ {
			if e[i] == '=' {
				vars = append(vars, corev1.EnvVar{
					Name:  e[:i],
					Value: e[i+1:],
				})
				break
			}
		}
	}
	return vars
}

// convertHostEnv maps generic EnvEntry values to K8s-native EnvVar.
// Plain values become EnvVar{Name, Value}; SecretRef entries become
// EnvVar{Name, ValueFrom: SecretKeyRef}. No vendor knowledge.
func convertHostEnv(entries []EnvEntry) []corev1.EnvVar {
	vars := make([]corev1.EnvVar, 0, len(entries))
	for _, e := range entries {
		if e.SecretRef != nil {
			vars = append(vars, corev1.EnvVar{
				Name: e.Name,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: e.SecretRef.Name,
						},
						Key: e.SecretRef.Key,
					},
				},
			})
		} else {
			vars = append(vars, corev1.EnvVar{
				Name:  e.Name,
				Value: e.Value,
			})
		}
	}
	return vars
}

// mergeEnv combines orchestrator env (KEY=VALUE strings) and host-contributed
// env ([]EnvEntry) into a deduplicated []corev1.EnvVar list.
// On duplicate names: HostEnv wins (last-writer-wins, explicit).
func mergeEnv(baseEnv []string, hostEnv []EnvEntry) []corev1.EnvVar {
	seen := make(map[string]int) // name -> index in result
	result := make([]corev1.EnvVar, 0, len(baseEnv)+len(hostEnv))

	// Write base env first
	for _, ev := range parseEnvStrings(baseEnv) {
		if idx, ok := seen[ev.Name]; ok {
			result[idx] = ev
		} else {
			seen[ev.Name] = len(result)
			result = append(result, ev)
		}
	}

	// HostEnv overwrites on collision
	for _, ev := range convertHostEnv(hostEnv) {
		if idx, ok := seen[ev.Name]; ok {
			result[idx] = ev
		} else {
			seen[ev.Name] = len(result)
			result = append(result, ev)
		}
	}

	return result
}

// mergeStringMaps merges multiple maps, later values overriding earlier ones.
func mergeStringMaps(maps ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}
	return result
}

// buildInitScript generates a shell script that copies workspace files from
// the ConfigMap mount to their expected paths in the pod workspace directory.
// For each artifact: mkdir -p the parent dir, then cp from ConfigMap.
func buildInitScript(workDir string, artifacts []WorkspaceArtifact) string {
	if len(artifacts) == 0 {
		return ""
	}

	// Collect unique dirs to create. Always include workDir itself —
	// PVC only provides the mount root, not per-agent subdirectories.
	dirs := make(map[string]bool)
	dirs[workDir] = true
	for _, a := range artifacts {
		dir := filepath.Dir(a.DestPath)
		if dir != "." && dir != "" {
			dirs[fmt.Sprintf("%s/%s", workDir, dir)] = true
		}
	}

	var mkdirs []string
	for d := range dirs {
		mkdirs = append(mkdirs, d)
	}
	sort.Strings(mkdirs)

	var parts []string
	parts = append(parts, fmt.Sprintf("mkdir -p %s", strings.Join(mkdirs, " ")))

	for _, a := range artifacts {
		parts = append(parts, fmt.Sprintf(
			"cp %s/%s %s/%s 2>/dev/null || true",
			configMapMountPath, a.ConfigMapKey, workDir, a.DestPath,
		))
	}

	return strings.Join(parts, " && ")
}

func boolPtr(b bool) *bool { return &b }

func buildResourceRequirements(r *ResourceRequirements) corev1.ResourceRequirements {
	reqs := corev1.ResourceRequirements{}

	if r.CPURequest != "" || r.MemoryRequest != "" {
		reqs.Requests = corev1.ResourceList{}
		if r.CPURequest != "" {
			reqs.Requests[corev1.ResourceCPU] = resource.MustParse(r.CPURequest)
		}
		if r.MemoryRequest != "" {
			reqs.Requests[corev1.ResourceMemory] = resource.MustParse(r.MemoryRequest)
		}
	}

	if r.CPULimit != "" || r.MemoryLimit != "" {
		reqs.Limits = corev1.ResourceList{}
		if r.CPULimit != "" {
			reqs.Limits[corev1.ResourceCPU] = resource.MustParse(r.CPULimit)
		}
		if r.MemoryLimit != "" {
			reqs.Limits[corev1.ResourceMemory] = resource.MustParse(r.MemoryLimit)
		}
	}

	return reqs
}
