// Package credentials implements a three-layer credential model:
// sources (credential origins), resolvers (runtime helpers), and
// bindings (how a host adapter consumes credentials). The engine is
// topology-aware, config-driven, and host-agnostic — host adapters
// project generic CredentialOutput into host-specific formats.
package credentials

import (
	"github.com/darkquasar/fracta/internal/runtime"
)

// ---------------------------------------------------------------------------
// Scope & Phase enums
// ---------------------------------------------------------------------------

// Scope describes where a credential source can execute.
type Scope string

const (
	ScopeAgentRuntime Scope = "agent_runtime"
	ScopeHostEdge     Scope = "host_edge"
	ScopeAny          Scope = "any"
)

// ExecutionPhase describes when a source will be materialized.
type ExecutionPhase string

const (
	// PhasePrepareNow means the source scope matches the current topology
	// and should be materialized immediately during plan execution.
	PhasePrepareNow ExecutionPhase = "prepare_now"

	// PhaseRuntimeOnly means the source scope is agent_runtime and will be
	// attempted later by the runtime helper (no action at plan time).
	PhaseRuntimeOnly ExecutionPhase = "runtime_only"

	// PhaseUnavailable means the source scope doesn't match the current
	// topology and has no runtime path.
	PhaseUnavailable ExecutionPhase = "unavailable"
)

// Topology describes where the credential engine is currently running.
type Topology string

const (
	TopologyHostEdge  Topology = "host_edge"
	TopologyInCluster Topology = "in_cluster"
)

// ---------------------------------------------------------------------------
// Diagnostic
// ---------------------------------------------------------------------------

// DiagnosticSeverity indicates severity of a diagnostic entry.
type DiagnosticSeverity string

const (
	SeverityInfo    DiagnosticSeverity = "info"
	SeverityWarning DiagnosticSeverity = "warning"
	SeverityError   DiagnosticSeverity = "error"
)

// Diagnostic captures a single event during plan building or execution
// for observability, debugging, and the `fracta auth diagnose` command.
type Diagnostic struct {
	Severity DiagnosticSeverity
	Stage    string // e.g. "plan.source", "source.prepare", "assertion.fail"
	Message  string
	Fields   map[string]string // structured metadata (source_name, phase, etc.)
}

// ---------------------------------------------------------------------------
// Config types (temporary — will be replaced by config.Credential* imports
// once B1 lands on the config branch)
// ---------------------------------------------------------------------------

// HostSecretRef references a key in a K8s Secret.
type HostSecretRef struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"`
}

// HTTPRequest describes an HTTP request for http_header_token sources.
type HTTPRequest struct {
	Method  string            `yaml:"method"`
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers,omitempty"`
}

// ExtractConfig describes how to extract a credential from an HTTP response.
type ExtractConfig struct {
	Header string `yaml:"header"`
}

// CredentialSource describes a named credential origin.
// Some sources are materialized before spawn; others are only available to a
// runtime helper inside the agent environment.
type CredentialSource struct {
	Type      string         `yaml:"type"`               // "http_header_token", "command_output", "secret_env"
	Scope     string         `yaml:"scope"`              // "agent_runtime", "host_edge", "any"
	Command   []string       `yaml:"command,omitempty"`  // for command_output
	Delivery  string         `yaml:"delivery,omitempty"` // "file_mount", "env", "staged_secret"
	Path      string         `yaml:"path,omitempty"`
	Required  *bool          `yaml:"required,omitempty"`
	Request   *HTTPRequest   `yaml:"request,omitempty"`
	Extract   *ExtractConfig `yaml:"extract,omitempty"`
	EnvName   string         `yaml:"env_name,omitempty"`
	SecretRef *HostSecretRef `yaml:"secret_ref,omitempty"`
}

// IsRequired returns true if the source is required (defaults to true).
func (s *CredentialSource) IsRequired() bool {
	return s.Required == nil || *s.Required
}

// CredentialResolver describes a runtime helper command.
// In K8s-style profiles it may try named sources in order; in local-process
// mode it can also stand alone with no source references.
type CredentialResolver struct {
	Type    string   `yaml:"type"` // "command"
	Command string   `yaml:"command"`
	TTLMs   int      `yaml:"ttl_ms,omitempty"`
	Order   []string `yaml:"order,omitempty"` // deprecated: legacy source preference for compatibility only
}

// CredentialBinding describes how a host adapter consumes credentials.
// bearer_env supports three shapes:
//   - source + env_name: inject a prepared source into an env var
//   - resolver + env_name: inject the first materialized source used by the helper
//   - env_name only: expect the env var to already exist in merged host/profile env
type CredentialBinding struct {
	Type     string `yaml:"type"`               // "claude_api_key_helper", "bearer_env", "token_file"
	RuntimeAuthResolver string `yaml:"runtime_auth_resolver,omitempty"` // required for claude_api_key_helper
	AuthOrigin          string `yaml:"auth_origin,omitempty"`           // for direct source consumption
	EnvName  string `yaml:"env_name,omitempty"` // for bearer_env
}

// CredentialAssertions are declarative validation rules for the final merged env.
type CredentialAssertions struct {
	RequireEnv    []string `yaml:"require_env,omitempty"`
	ForbidEnv     []string `yaml:"forbid_env,omitempty"`
	RequireSource []string `yaml:"require_source,omitempty"`
	WarnIfMissing []string `yaml:"warn_if_missing_env,omitempty"`
}

// CredentialProfile is a complete credential configuration referenced by hosts.
type CredentialProfile struct {
	AuthOrigins          map[string]CredentialSource   `yaml:"auth_origins"`
	RuntimeAuthResolvers map[string]CredentialResolver `yaml:"runtime_auth_resolvers"`
	Env            map[string]string             `yaml:"env,omitempty"`
	Assertions     *CredentialAssertions         `yaml:"assertions,omitempty"`
	DefaultBinding *CredentialBinding            `yaml:"default_binding,omitempty"`
}

// ---------------------------------------------------------------------------
// Core plan & output types
// ---------------------------------------------------------------------------

// AnnotatedSource pairs a config source with its resolved execution phase
// and optional pre-materialized data (from host-edge preparation or
// staged credential rehydration).
type AnnotatedSource struct {
	AuthOrigin       *CredentialSource
	Name             string
	Phase            ExecutionPhase
	MaterializedData []byte // non-nil when pre-materialized (staged or prepared)
}

// CredentialPlan is the output of BuildCredentialPlan: a fully annotated,
// topology-aware plan describing which sources to prepare, which resolver
// to use, which binding to apply, and what env/assertions to validate.
// ALL sources stay in the plan regardless of phase — none are filtered out.
type CredentialPlan struct {
	Profile    string
	AuthOrigins          []AnnotatedSource
	RuntimeAuthResolver  *CredentialResolver
	Binding    *CredentialBinding
	Env        map[string]string
	Assertions *CredentialAssertions
}

// CredentialOutput carries generic materialized artifacts produced by
// ExecuteCredentialPlan. Host adapters receive this PLUS the plan's
// Binding and Resolver to project into host-specific formats (e.g.,
// Claude's user-settings.json).
type CredentialOutput struct {
	Plan        *CredentialPlan    // the plan that produced this output
	EnvEntries  []runtime.EnvEntry // pod env vars
	SecretData  map[string][]byte  // for K8s Secret creation
	MountPath   string             // secret mount path
	Diagnostics []Diagnostic       // what happened (for dry-run + logging)
}
