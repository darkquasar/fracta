// Package runtime defines the Backend interface for agent execution
// and concrete implementations for local (exec.Cmd) and Kubernetes (Jobs).
package runtime

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/darkquasar/fracta/internal/model"
)

// ErrNotFound indicates the agent/process/job was not found (already exited or never created).
var ErrNotFound = errors.New("agent not found")

// WorkspaceArtifact represents a single file to inject into a K8s pod workspace
// via ConfigMap. ConfigMapKey is a flat, K8s-safe key (alnum/-/_/. only).
// DestPath is the relative path inside the pod workspace (e.g. ".codex/config.toml").
type WorkspaceArtifact struct {
	ConfigMapKey string
	DestPath     string
	Content      string
}

// EnvEntry describes an environment variable and its value source.
// This is execution-source metadata (where the value comes from),
// not host metadata (which host it belongs to). The runtime renders
// these generically without knowing which host they serve.
type EnvEntry struct {
	Name      string
	Value     string     // plain value; mutually exclusive with SecretRef
	SecretRef *SecretRef // secret-backed value; nil for plain values
}

// SecretRef identifies a secret-backed value by name and key.
// In K8s: rendered as secretKeyRef. In local: causes spawn rejection.
type SecretRef struct {
	Name string // secret store name (K8s Secret, Vault path, etc.)
	Key  string // key within the secret
}

// SpawnOpts contains everything a Backend needs to launch an agent.
type SpawnOpts struct {
	// ID is a unique identifier for this agent (used for naming K8s Jobs, log files, etc.).
	ID string

	// Command is the binary to execute (e.g. "claude").
	Command string

	// Args are the command-line arguments (built by buildClaudeArgs or buildStreamArgs).
	Args []string

	// WorkDir is the working directory for the process.
	WorkDir string

	// Env is a list of "KEY=VALUE" environment variables to set.
	Env []string

	// HostEnv contains host-contributed env vars (may include SecretRef for K8s).
	// Per-spawn, produced by config.BuildHostEnv before Backend.Spawn is called.
	HostEnv []EnvEntry

	// Model is the resolved model ID (informational; already baked into Args).
	Model string

	// ConfigSnapshot is the resolved agent config YAML to mount as a ConfigMap in K8s.
	// When non-empty, the K8s backend creates a ConfigMap and mounts it at /etc/fracta/.
	ConfigSnapshot string

	// WorkspaceFiles carries workspace artifacts for K8s ConfigMap injection.
	// Each artifact specifies a ConfigMapKey (flat, K8s-safe), a DestPath
	// (relative path in the pod workspace), and Content. When non-empty, the
	// K8s backend includes these in the ConfigMap, adds an emptyDir volume for
	// scratch workspace (when no PVC is configured), and adds an initContainer
	// to distribute files to their expected paths.
	WorkspaceFiles []WorkspaceArtifact

	// AuthSecretData carries host-seeded auth data for per-spawn K8s Secret creation.
	// Key = filename (Secret key), Value = secret data bytes.
	// When non-empty, the K8s backend creates a Secret and mounts it as a volume.
	AuthSecretData map[string][]byte

	// AuthSecretMountPath is the pod-side mount path for the auth secret volume.
	// Default: "/var/run/fracta-auth"
	AuthSecretMountPath string

	// Image is the container image to use (K8s backend only; ignored by local).
	Image string

	// Namespace is the Kubernetes namespace (K8s backend only; ignored by local).
	Namespace string

	// Resources holds K8s resource requests/limits (K8s backend only).
	Resources *ResourceRequirements
}

// ResourceRequirements mirrors K8s resource specs without importing client-go types.
type ResourceRequirements struct {
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
}

// AgentHandle is returned by Backend.Spawn and lets callers wait for
// completion, read output, and check whether the process is still alive.
type AgentHandle interface {
	// Wait blocks until the agent process completes and returns the exit error (nil on success).
	Wait() error

	// Output returns a reader for the agent's stdout.
	// The returned reader is valid after Wait returns; reading before Wait
	// may return partial output depending on the backend.
	Output() io.Reader

	// ExitCode returns the process exit code. Only meaningful after Wait returns.
	// Returns -1 if the exit code is unavailable.
	ExitCode() int

	// StartTime returns when the agent process started.
	StartTime() time.Time
}

// Backend abstracts the execution environment for agents.
// LocalBackend runs agents as local OS processes; KubernetesBackend
// submits them as K8s Jobs.
type Backend interface {
	// Spawn starts an agent with the given options and returns a handle.
	Spawn(ctx context.Context, opts SpawnOpts) (AgentHandle, error)

	// Kill terminates a running agent identified by its ID.
	Kill(ctx context.Context, id string) error

	// Status returns the current execution status of an agent.
	Status(ctx context.Context, id string) (model.AgentStatus, error)

	// Logs returns recent log output for an agent. tailLines controls how
	// many lines to return (0 = all available). In K8s this fetches pod logs;
	// locally it reads the agent's log file.
	Logs(ctx context.Context, id string, tailLines int) (string, error)
}

// StreamBackend is an optional interface that Backends may implement to support
// long-lived streaming pods. Unlike Spawn (which creates a batch Job that exits),
// SpawnStreamPod creates a persistent Pod running a serve command.
type StreamBackend interface {
	// SpawnStreamPod launches a persistent pod running the runtime's serve command.
	// The pod stays alive across turns until explicitly killed. Returns connection
	// metadata for the orchestrator to construct a StreamSession.
	SpawnStreamPod(ctx context.Context, opts StreamPodOpts) (*StreamPodInfo, error)

	// KillStreamPod deletes a persistent stream pod by agent ID.
	// Must be called when killing or cleaning up a stream-mode agent to prevent
	// pod leaks. Returns ErrNotFound if the pod does not exist (already gone).
	KillStreamPod(ctx context.Context, id string) error
}

// StreamPodOpts contains everything needed to launch a persistent streaming pod.
type StreamPodOpts struct {
	SpawnOpts // embeds base spawn config (ID, Image, Env, HostEnv, etc.)

	// Port is the container port the serve command listens on.
	Port int32

	// RuntimeType identifies which runtime is running (e.g., "codex", "opencode").
	// Used to select health check strategy and connection metadata format.
	RuntimeType string

	// ServePassword is the basic auth password for OpenCode serve.
	// Empty for runtimes that don't use password auth.
	ServePassword string

	// WebSocketAuthToken is the capability token for Codex WebSocket auth.
	// Empty for runtimes that don't use WebSocket auth.
	WebSocketAuthToken string
}

// StreamPodInfo carries the connection metadata returned by SpawnStreamPod.
// Exactly one of CodexWebSocket or OpenCodeHTTP is set, depending on runtime.
type StreamPodInfo struct {
	// PodName is the Kubernetes pod name (for status, logs, kill).
	PodName string

	// CodexWebSocket is set when the runtime is Codex app-server over WebSocket.
	CodexWebSocket *WebSocketTransport

	// OpenCodeHTTP is set when the runtime is OpenCode serve over HTTP.
	OpenCodeHTTP *HTTPTransport
}

// WebSocketTransport holds connection info for a Codex app-server WebSocket endpoint.
type WebSocketTransport struct {
	URL       string // e.g., "ws://10.0.0.5:8080"
	AuthToken string
}

// HTTPTransport holds connection info for an OpenCode serve HTTP endpoint.
type HTTPTransport struct {
	BaseURL  string // e.g., "http://10.0.0.5:4096"
	Password string
}
