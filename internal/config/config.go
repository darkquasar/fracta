package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/darkquasar/fracta/internal/fractalog"
	"gopkg.in/yaml.v3"
)

var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ConnectionConfig describes a single service connection.
type ConnectionConfig struct {
	Type   string `yaml:"type"`
	URL    string `yaml:"url,omitempty"`
	APIKey string `yaml:"api_key,omitempty"`

	// Snowflake-specific
	Account  string `yaml:"account,omitempty"`
	User     string `yaml:"user,omitempty"`
	Password string `yaml:"password,omitempty"`

	// FalkorDB-specific
	GraphName string `yaml:"graph_name,omitempty"`

	// Bedrock-specific
	Region string `yaml:"region,omitempty"`
}

// ResourceConfig specifies CPU/memory resource requests and limits.
type ResourceConfig struct {
	CPURequest    string `yaml:"cpu_request,omitempty"`
	CPULimit      string `yaml:"cpu_limit,omitempty"`
	MemoryRequest string `yaml:"memory_request,omitempty"`
	MemoryLimit   string `yaml:"memory_limit,omitempty"`
}

// KubernetesConfig holds all K8s-specific runtime settings.
type KubernetesConfig struct {
	Namespace       string            `yaml:"namespace"`
	Image           string            `yaml:"image"`
	ImagePullPolicy string            `yaml:"image_pull_policy"` // "Never", "IfNotPresent", "Always"
	ServiceAccount  string            `yaml:"service_account"`
	PVC             string            `yaml:"pvc"`
	PVCMountPath    string            `yaml:"pvc_mount_path"` // pod-side PVC mount (default: "/workspace")
	Labels          map[string]string `yaml:"labels"`
	Annotations     map[string]string `yaml:"annotations"`
	Tolerations     []string          `yaml:"tolerations"`
	NodeSelector    map[string]string `yaml:"node_selector"`
	Resources       ResourceConfig    `yaml:"resources"`
	JobTTLSeconds   int               `yaml:"job_ttl_seconds"`
}

// StateConfig controls where fracta persists agent/session state.
type StateConfig struct {
	Driver   string         `yaml:"driver,omitempty"` // "sqlite" (default) or "postgres"
	SQLite   SQLiteConfig   `yaml:"sqlite,omitempty"`
	Postgres PostgresConfig `yaml:"postgres,omitempty"`

	// Deprecated: use SQLite.Path instead. Kept for backward compatibility.
	Path string `yaml:"path,omitempty"`
}

// EffectiveDriver returns the resolved driver, defaulting to "sqlite".
func (sc StateConfig) EffectiveDriver() string {
	if sc.Driver != "" {
		return sc.Driver
	}
	return "sqlite"
}

// EffectiveSQLitePath returns the resolved SQLite path with backward compat.
func (sc StateConfig) EffectiveSQLitePath() string {
	if sc.SQLite.Path != "" {
		return sc.SQLite.Path
	}
	if sc.Path != "" {
		return sc.Path // backward compat
	}
	return ".fracta/state.db"
}

// SQLiteConfig holds SQLite-specific settings.
type SQLiteConfig struct {
	Path string `yaml:"path,omitempty"` // default: ".fracta/state.db"
}

// PostgresConfig holds Postgres connection and pool settings.
type PostgresConfig struct {
	DSN             string `yaml:"dsn,omitempty"`
	MaxConns        int32  `yaml:"max_conns,omitempty"`
	MinConns        int32  `yaml:"min_conns,omitempty"`
	MaxConnLifetime string `yaml:"max_conn_lifetime,omitempty"`
	MaxConnIdleTime string `yaml:"max_conn_idle_time,omitempty"`
}

// Duration wraps time.Duration for YAML unmarshalling from strings like "4h", "30s".
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = parsed
	return nil
}

func (d Duration) MarshalYAML() (interface{}, error) {
	return d.Duration.String(), nil
}

// ReaperConfig controls automatic cleanup of completed/stale agent jobs.
type ReaperConfig struct {
	Interval      Duration `yaml:"interval"`       // How often the reaper runs (e.g. "30s")
	MaxAge        Duration `yaml:"max_age"`        // Kill agents older than this (e.g. "4h")
	MaxConcurrent int      `yaml:"max_concurrent"` // Max simultaneous running agents (0 = no limit)
}

// QueueConfig controls the optional mission queue for batch hunt execution.
type QueueConfig struct {
	Backend       string   `yaml:"backend,omitempty"`        // "memory" | "postgres" — must be explicitly set
	Workers       int      `yaml:"workers,omitempty"`        // in-process worker count (default: 2)
	LeaseTimeout  Duration `yaml:"lease_timeout,omitempty"`  // mission claim expiry (default: "30m")
	WorkspaceBase string   `yaml:"workspace_base,omitempty"` // worker workspace dir override
}

// RuntimeConfig controls how agents are spawned.
type RuntimeConfig struct {
	Backend    string           `yaml:"backend"` // "local" or "kubernetes"
	Kubernetes KubernetesConfig `yaml:"kubernetes"`
	State      StateConfig      `yaml:"state"`
	Queue      QueueConfig      `yaml:"queue,omitempty"`
	StagingDir string           `yaml:"staging_dir,omitempty"` // default: /tmp/fracta-staging
	Transport  string           `yaml:"transport,omitempty"`   // "stdio" (default) or "http"
	ListenAddr string           `yaml:"listen_addr,omitempty"` // HTTP listen address (default ":8080")
}

// GatewayConfig holds settings for the centralized MCP gateway.
type GatewayConfig struct {
	URL    string `yaml:"url,omitempty"`    // gateway URL for agent MCP configs (e.g. "http://fracta-gateway:8080")
	Listen string `yaml:"listen,omitempty"` // listen address when running as gateway (e.g. ":8080")
}

// ControlPlaneAPIConfig holds settings for the host-facing control-plane API.
type ControlPlaneAPIConfig struct {
	Listen     string `yaml:"listen,omitempty"`      // server bind address (e.g. ":9090")
	URL        string `yaml:"url,omitempty"`         // client connection URL (e.g. "http://fracta-controlplane.fracta.svc:9090")
	ClientMode string `yaml:"client_mode,omitempty"` // "auto" (default), "local", "remote"
}

// --- Credential Pipeline Types (spec-33) ---

// HTTPRequest describes an HTTP request for credential extraction.
type HTTPRequest struct {
	Method  string            `yaml:"method"`
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers,omitempty"`
}

// ExtractConfig describes how to extract a credential from an HTTP response.
type ExtractConfig struct {
	Header string `yaml:"header,omitempty"` // extract from response header
}

// CredentialSource describes a named credential origin.
// Some sources are materialized before spawn; others are only available to a
// runtime helper inside the agent environment.
type CredentialSource struct {
	Type      string         `yaml:"type"`                 // "http_header_token", "command_output", "secret_env"
	Scope     string         `yaml:"scope"`                // "agent_runtime", "host_edge", "any"
	Command   FlexCommand    `yaml:"command,omitempty"`    // for command_output
	Delivery  string         `yaml:"delivery,omitempty"`   // "file_mount", "env", "staged_secret"
	Path      string         `yaml:"path,omitempty"`       // pod-side file path
	Required  *bool          `yaml:"required,omitempty"`   // fail if source fails (default: true)
	Request   *HTTPRequest   `yaml:"request,omitempty"`    // for http_header_token
	Extract   *ExtractConfig `yaml:"extract,omitempty"`    // for http_header_token
	EnvName   string         `yaml:"env_name,omitempty"`   // for secret_env
	SecretRef *HostSecretRef `yaml:"secret_ref,omitempty"` // for secret_env
}

// IsRequired returns true if the source should fail the plan when it fails.
// Defaults to true when Required is not explicitly set.
func (s *CredentialSource) IsRequired() bool {
	return s.Required == nil || *s.Required
}

// CredentialResolver describes a runtime helper command.
// In K8s-style profiles it may try named sources in order; in local-process
// mode it can also stand alone with no source references.
type CredentialResolver struct {
	Type    string   `yaml:"type"`             // "command"
	Command string   `yaml:"command"`          // resolver command path
	TTLMs   int      `yaml:"ttl_ms,omitempty"` // cache TTL in ms
	Order   []string `yaml:"order,omitempty"`  // deprecated: legacy source preference for compatibility only
}

// CredentialBinding describes how a host adapter consumes credentials.
// bearer_env supports three shapes:
//   - auth_origin + env_name: inject a prepared source into an env var
//   - runtime_auth_resolver + env_name: inject the first materialized source used by the helper
//   - env_name only: expect the env var to already exist in merged host/profile env
type CredentialBinding struct {
	Type                string `yaml:"type"`                            // "claude_api_key_helper", "bearer_env", "token_file"
	RuntimeAuthResolver string `yaml:"runtime_auth_resolver,omitempty"` // required for claude_api_key_helper
	AuthOrigin          string `yaml:"auth_origin,omitempty"`           // for direct source consumption
	EnvName             string `yaml:"env_name,omitempty"`              // for bearer_env
}

// CredentialAssertions defines declarative validation rules for the final merged environment.
type CredentialAssertions struct {
	RequireEnv    []string `yaml:"require_env,omitempty"`
	ForbidEnv     []string `yaml:"forbid_env,omitempty"`
	RequireSource []string `yaml:"require_source,omitempty"`
	WarnIfMissing []string `yaml:"warn_if_missing_env,omitempty"`
}

// CredentialProfile describes a reusable credential configuration with sources, resolvers, and bindings.
type CredentialProfile struct {
	AuthOrigins          map[string]CredentialSource   `yaml:"auth_origins"`
	RuntimeAuthResolvers map[string]CredentialResolver `yaml:"runtime_auth_resolvers"`
	Env                  map[string]string             `yaml:"env,omitempty"`
	Assertions           *CredentialAssertions         `yaml:"assertions,omitempty"`
	DefaultBinding       *CredentialBinding            `yaml:"default_binding,omitempty"`
}

// CredentialsConfig holds the set of credential profiles.
type CredentialsConfig struct {
	Profiles map[string]CredentialProfile `yaml:"profiles"`
}

// AuthConfig holds the auth.credentials configuration block.
type AuthConfig struct {
	Credentials CredentialsConfig `yaml:"credentials"`
}

// ValidateCredentialBinding checks that a binding's type-specific requirements are met.
func ValidateCredentialBinding(b *CredentialBinding, profileName string, profile *CredentialProfile) error {
	if b == nil {
		return nil
	}
	switch b.Type {
	case "claude_api_key_helper":
		if b.RuntimeAuthResolver == "" {
			return fmt.Errorf("auth.credentials.profiles.%s: claude_api_key_helper binding requires runtime_auth_resolver", profileName)
		}
		if profile != nil {
			if _, ok := profile.RuntimeAuthResolvers[b.RuntimeAuthResolver]; !ok {
				return fmt.Errorf("auth.credentials.profiles.%s: binding references runtime_auth_resolver %q which is not defined", profileName, b.RuntimeAuthResolver)
			}
		}
	case "bearer_env":
		hasAuthOrigin := b.AuthOrigin != ""
		hasResolver := b.RuntimeAuthResolver != ""
		if hasAuthOrigin && hasResolver {
			return fmt.Errorf("auth.credentials.profiles.%s: bearer_env binding must have exactly one of auth_origin or runtime_auth_resolver", profileName)
		}
		if b.EnvName == "" {
			return fmt.Errorf("auth.credentials.profiles.%s: bearer_env binding requires env_name", profileName)
		}
		if profile != nil {
			if hasAuthOrigin {
				if _, ok := profile.AuthOrigins[b.AuthOrigin]; !ok {
					return fmt.Errorf("auth.credentials.profiles.%s: binding references auth_origin %q which is not defined", profileName, b.AuthOrigin)
				}
			}
			if hasResolver {
				if _, ok := profile.RuntimeAuthResolvers[b.RuntimeAuthResolver]; !ok {
					return fmt.Errorf("auth.credentials.profiles.%s: binding references runtime_auth_resolver %q which is not defined", profileName, b.RuntimeAuthResolver)
				}
			}
		}
	case "token_file":
		if b.AuthOrigin == "" {
			return fmt.Errorf("auth.credentials.profiles.%s: token_file binding requires auth_origin", profileName)
		}
		if profile != nil {
			if _, ok := profile.AuthOrigins[b.AuthOrigin]; !ok {
				return fmt.Errorf("auth.credentials.profiles.%s: binding references auth_origin %q which is not defined", profileName, b.AuthOrigin)
			}
		}
	case "":
		return fmt.Errorf("auth.credentials.profiles.%s: binding type must not be empty", profileName)
	default:
		return fmt.Errorf("auth.credentials.profiles.%s: unknown binding type %q", profileName, b.Type)
	}
	return nil
}

// ValidateCredentialProfile checks that a credential profile is well-formed.
func (p *CredentialProfile) ValidateCredentialProfile(profileName string) error {
	// Validate auth_origins
	for name, src := range p.AuthOrigins {
		switch src.Type {
		case "http_header_token":
			if src.Request == nil {
				return fmt.Errorf("auth.credentials.profiles.%s.auth_origins.%s: http_header_token requires request", profileName, name)
			}
			if src.Extract == nil {
				return fmt.Errorf("auth.credentials.profiles.%s.auth_origins.%s: http_header_token requires extract", profileName, name)
			}
		case "command_output":
			if len(src.Command) == 0 {
				return fmt.Errorf("auth.credentials.profiles.%s.auth_origins.%s: command_output requires command", profileName, name)
			}
		case "secret_env":
			if src.EnvName == "" {
				return fmt.Errorf("auth.credentials.profiles.%s.auth_origins.%s: secret_env requires env_name", profileName, name)
			}
			if src.SecretRef == nil {
				return fmt.Errorf("auth.credentials.profiles.%s.auth_origins.%s: secret_env requires secret_ref", profileName, name)
			}
		case "":
			return fmt.Errorf("auth.credentials.profiles.%s.auth_origins.%s: type must not be empty", profileName, name)
		default:
			return fmt.Errorf("auth.credentials.profiles.%s.auth_origins.%s: unknown source type %q", profileName, name, src.Type)
		}

		switch src.Scope {
		case "agent_runtime", "host_edge", "any":
			// valid
		case "":
			return fmt.Errorf("auth.credentials.profiles.%s.auth_origins.%s: scope must not be empty", profileName, name)
		default:
			return fmt.Errorf("auth.credentials.profiles.%s.auth_origins.%s: unknown scope %q", profileName, name, src.Scope)
		}
	}

	// Validate runtime_auth_resolvers reference existing auth_origins
	for name, res := range p.RuntimeAuthResolvers {
		if res.Command == "" {
			return fmt.Errorf("auth.credentials.profiles.%s.runtime_auth_resolvers.%s: command must not be empty", profileName, name)
		}
		for _, srcName := range res.Order {
			if _, ok := p.AuthOrigins[srcName]; !ok {
				return fmt.Errorf("auth.credentials.profiles.%s.runtime_auth_resolvers.%s: order references auth_origin %q which is not defined", profileName, name, srcName)
			}
		}
	}

	// Validate default binding
	if p.DefaultBinding != nil {
		if err := ValidateCredentialBinding(p.DefaultBinding, profileName, p); err != nil {
			return err
		}
	}

	// Validate assertions reference existing auth_origins
	if p.Assertions != nil {
		for _, srcName := range p.Assertions.RequireSource {
			if _, ok := p.AuthOrigins[srcName]; !ok {
				return fmt.Errorf("auth.credentials.profiles.%s.assertions: require_source references auth_origin %q which is not defined", profileName, srcName)
			}
		}
	}

	return nil
}

// ResolveCredentialProfile looks up the credential profile for a given runtime type.
// It resolves the effective binding: runtime-level AuthBinding override takes priority
// over the profile's DefaultBinding.
// Returns (nil, nil, nil) if no credential profile is configured for the runtime.
func ResolveCredentialProfile(cfg *Config, runtimeType string) (*CredentialProfile, *CredentialBinding, error) {
	runtimes := cfg.EffectiveRuntimes()
	if runtimes == nil {
		return nil, nil, nil
	}
	hc, ok := runtimes[runtimeType]
	if !ok {
		return nil, nil, nil
	}
	profileName := hc.AuthProfile
	if profileName == "" {
		return nil, nil, nil
	}

	profile, ok := cfg.Auth.Credentials.Profiles[profileName]
	if !ok {
		return nil, nil, fmt.Errorf("runtime %q references auth_profile %q which is not defined in auth.credentials.profiles", runtimeType, profileName)
	}

	// Resolve effective binding: runtime override > default_binding
	var binding *CredentialBinding
	if hc.AuthBinding != nil {
		binding = hc.AuthBinding
	} else if profile.DefaultBinding != nil {
		binding = profile.DefaultBinding
	}

	return &profile, binding, nil
}

// MCPServerLocal describes an MCP server launched as a local subprocess.
type MCPServerLocal struct {
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env,omitempty"` // env vars for subprocess and agent delivery
}

// EnvSlice converts the Env map to []string ("KEY=VALUE") for os/exec.
func (l MCPServerLocal) EnvSlice() []string {
	if len(l.Env) == 0 {
		return nil
	}
	result := make([]string, 0, len(l.Env))
	for k, v := range l.Env {
		result = append(result, k+"="+v)
	}
	return result
}

// SecretValue holds a single secret resolvable from one of three sources.
// Exactly one of Value, Env, or File must be set when the field is required.
type SecretValue struct {
	Value string `yaml:"value,omitempty"`
	Env   string `yaml:"env,omitempty"`
	File  string `yaml:"file,omitempty"`
}

// Validate checks that exactly one source is set.
func (sv SecretValue) Validate(field string) error {
	n := 0
	if sv.Value != "" {
		n++
	}
	if sv.Env != "" {
		n++
	}
	if sv.File != "" {
		n++
	}
	if n == 0 {
		return fmt.Errorf("%s: exactly one of value, env, or file must be set", field)
	}
	if n > 1 {
		return fmt.Errorf("%s: only one of value, env, or file may be set (got %d)", field, n)
	}
	return nil
}

// IsZero returns true if no source is configured.
func (sv SecretValue) IsZero() bool {
	return sv.Value == "" && sv.Env == "" && sv.File == ""
}

// MCPServerAuth configures authentication for a remote MCP server.
type MCPServerAuth struct {
	Type string `yaml:"type"` // none, bearer, header, basic, oauth

	// bearer
	Token *SecretValue `yaml:"token,omitempty"`

	// header
	HeaderName  string       `yaml:"header_name,omitempty"`
	HeaderValue *SecretValue `yaml:"header_value,omitempty"`

	// basic
	Username *SecretValue `yaml:"username,omitempty"`
	Password *SecretValue `yaml:"password,omitempty"`

	// oauth
	ClientID     *SecretValue `yaml:"client_id,omitempty"`
	ClientSecret *SecretValue `yaml:"client_secret,omitempty"`
	Scopes       []string     `yaml:"scopes,omitempty"`
	RedirectURI  string       `yaml:"redirect_uri,omitempty"`
	PKCEEnabled  *bool        `yaml:"pkce,omitempty"`       // defaults to true
	GrantType    string       `yaml:"grant_type,omitempty"` // authorization_code (default), client_credentials, device_code

	// oauth pre-authorized tokens (mutually exclusive with interactive flow)
	AccessToken            *SecretValue `yaml:"access_token,omitempty"`
	RefreshToken           *SecretValue `yaml:"refresh_token,omitempty"`
	TokenFile              string       `yaml:"token_file,omitempty"`
	ClientRegistrationFile string       `yaml:"client_registration_file,omitempty"`
	MetadataURL            string       `yaml:"metadata_url,omitempty"`
}

// EffectivePKCE returns the PKCE setting, defaulting to true.
func (a MCPServerAuth) EffectivePKCE() bool {
	if a.PKCEEnabled != nil {
		return *a.PKCEEnabled
	}
	return true
}

// EffectiveRedirectURI returns the redirect URI, defaulting to localhost:9876/callback.
func (a MCPServerAuth) EffectiveRedirectURI() string {
	if a.RedirectURI != "" {
		return a.RedirectURI
	}
	return "http://localhost:9876/callback"
}

// Validate checks per-type required fields and constraints.
func (a MCPServerAuth) Validate(server string) error {
	switch a.Type {
	case "", "none":
		return nil
	case "bearer":
		if a.Token == nil {
			return fmt.Errorf("mcp_servers.%s.auth: bearer type requires 'token'", server)
		}
		return a.Token.Validate(fmt.Sprintf("mcp_servers.%s.auth.token", server))
	case "header":
		if a.HeaderName == "" {
			return fmt.Errorf("mcp_servers.%s.auth: header type requires 'header_name'", server)
		}
		if a.HeaderValue == nil {
			return fmt.Errorf("mcp_servers.%s.auth: header type requires 'header_value'", server)
		}
		return a.HeaderValue.Validate(fmt.Sprintf("mcp_servers.%s.auth.header_value", server))
	case "basic":
		if a.Username == nil {
			return fmt.Errorf("mcp_servers.%s.auth: basic type requires 'username'", server)
		}
		if a.Password == nil {
			return fmt.Errorf("mcp_servers.%s.auth: basic type requires 'password'", server)
		}
		if err := a.Username.Validate(fmt.Sprintf("mcp_servers.%s.auth.username", server)); err != nil {
			return err
		}
		return a.Password.Validate(fmt.Sprintf("mcp_servers.%s.auth.password", server))
	case "oauth":
		return a.validateOAuth(server)
	default:
		return fmt.Errorf("mcp_servers.%s.auth: unknown type %q (must be none, bearer, header, basic, or oauth)", server, a.Type)
	}
}

func (a MCPServerAuth) validateOAuth(server string) error {
	prefix := fmt.Sprintf("mcp_servers.%s.auth", server)
	gt := a.GrantType
	if gt == "" {
		gt = "authorization_code"
	}
	switch gt {
	case "authorization_code", "client_credentials", "device_code":
	default:
		return fmt.Errorf("%s: unknown grant_type %q", prefix, gt)
	}
	if gt == "client_credentials" {
		if a.ClientID == nil {
			return fmt.Errorf("%s: client_credentials requires 'client_id'", prefix)
		}
		if a.ClientSecret == nil {
			return fmt.Errorf("%s: client_credentials requires 'client_secret'", prefix)
		}
	}
	if a.ClientID != nil {
		if err := a.ClientID.Validate(prefix + ".client_id"); err != nil {
			return err
		}
	}
	if a.ClientSecret != nil {
		if err := a.ClientSecret.Validate(prefix + ".client_secret"); err != nil {
			return err
		}
	}
	if a.AccessToken != nil {
		if err := a.AccessToken.Validate(prefix + ".access_token"); err != nil {
			return err
		}
	}
	if a.RefreshToken != nil {
		if err := a.RefreshToken.Validate(prefix + ".refresh_token"); err != nil {
			return err
		}
	}
	if a.AccessToken != nil && a.TokenFile != "" {
		return fmt.Errorf("%s: access_token and token_file are mutually exclusive", prefix)
	}
	return nil
}

// TokenStoreConfig controls OAuth token persistence.
type TokenStoreConfig struct {
	Driver   string `yaml:"driver,omitempty"`   // auto (default), keyring, file
	Password string `yaml:"password,omitempty"` // for file backend encryption (or FRACTA_TOKEN_PASSWORD env)
}

// MCPServerRemote describes an already-running MCP server accessed by URL.
type MCPServerRemote struct {
	URL       string            `yaml:"url"`
	Headers   map[string]string `yaml:"headers,omitempty"`   // custom HTTP headers (auth, routing)
	Transport string            `yaml:"transport,omitempty"` // "streamable_http" (default) or "sse"
	Auth      *MCPServerAuth    `yaml:"auth,omitempty"`
}

// MCPServerKubernetes is a deprecated alias for MCPServerRemote.
type MCPServerKubernetes = MCPServerRemote

// MCPServerEntry describes a single MCP server with local or remote connection variants.
type MCPServerEntry struct {
	Local      MCPServerLocal   `yaml:"local"`
	Remote     *MCPServerRemote `yaml:"remote,omitempty" json:"Remote,omitempty"`
	Kubernetes MCPServerRemote  `yaml:"kubernetes"` // Deprecated: use remote.
}

// EffectiveRemote returns the explicit remote server config, falling back to
// the deprecated kubernetes key for compatibility.
func (e MCPServerEntry) EffectiveRemote() (MCPServerRemote, bool) {
	if e.Remote != nil && e.Remote.URL != "" {
		return *e.Remote, true
	}
	if e.Kubernetes.URL != "" {
		return e.Kubernetes, true
	}
	return MCPServerRemote{}, false
}

// MCPServersConfig holds the set of MCP servers available to agents.
type MCPServersConfig struct {
	Servers map[string]MCPServerEntry `yaml:"servers"`
}

// RegistryConfig controls the MCP registry store.
type RegistryConfig struct {
	Driver              string         `yaml:"driver,omitempty"`                // "" = inherit from runtime.state.driver
	Postgres            PostgresConfig `yaml:"postgres,omitempty"`              // explicit Postgres config; falls back to runtime.state.postgres
	ReconcileInterval   string         `yaml:"reconcile_interval,omitempty"`    // default: "60s"
	BootstrapFromConfig bool           `yaml:"bootstrap_from_config,omitempty"` // default: true (zero value is false, handled at resolve time)
}

// AuthzConfig controls authorization defaults.
type AuthzConfig struct {
	DefaultRole string `yaml:"default_role,omitempty"` // default: "admin"
}

// StrategyConfig controls strategy engine behavior.
type StrategyConfig struct {
	PoolSize    int    `yaml:"pool_size,omitempty"`    // number of sidecar subprocesses (default: 2)
	Dir         string `yaml:"dir,omitempty"`          // strategy directory path
	AutoPromote bool   `yaml:"auto_promote,omitempty"` // enable auto validated->promoted (default: false)
}

// EffectivePoolSize returns the pool size, defaulting to 2.
func (sc StrategyConfig) EffectivePoolSize() int {
	if sc.PoolSize > 0 {
		return sc.PoolSize
	}
	return 2
}

// ObservabilityConfig controls agent observability settings.
type ObservabilityConfig struct {
	HeartbeatInterval Duration `yaml:"heartbeat_interval"` // Worker heartbeat interval (default: "15s")
	RingSize          int      `yaml:"ring_size"`          // Events per agent ring buffer (default: 100)
}

// ProjectConfig holds project-level defaults (replaces .fracta/config.json project fields).
type ProjectConfig struct {
	DefaultBaseBranch string   `yaml:"default_base_branch,omitempty"`
	AllowedTools      []string `yaml:"allowed_tools,omitempty"`
}

// AgentsConfig holds agent routing defaults.
type AgentsConfig struct {
	DefaultRuntime  string                  `yaml:"default_runtime,omitempty"`   // default: "claude"
	DefaultHostType string                  `yaml:"default_host_type,omitempty"` // Deprecated: use DefaultRuntime
	DefaultMode     string                  `yaml:"default_mode,omitempty"`      // "batch" (default) or "stream"
	AgentRuntimes   map[string]RuntimeEntry `yaml:"agent_runtimes,omitempty"`
}

// EffectiveDefaultRuntime returns the resolved default runtime, preferring
// DefaultRuntime over the deprecated DefaultHostType.
func (a AgentsConfig) EffectiveDefaultRuntime() string {
	if a.DefaultRuntime != "" {
		return a.DefaultRuntime
	}
	return a.DefaultHostType
}

// HostEnvVar describes a single env var for K8s host execution.
type HostEnvVar struct {
	Name      string         `yaml:"name"`
	Value     string         `yaml:"value,omitempty"`
	SecretRef *HostSecretRef `yaml:"secret_ref,omitempty"`
}

// HostSecretRef references a key in a K8s Secret.
type HostSecretRef struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"`
}

// FlexCommand accepts a YAML string or string list, normalizing to []string.
// Allows pod_script to use a bare string path and host_seed to use a list with args.
type FlexCommand []string

// UnmarshalYAML handles both scalar strings and sequence nodes.
func (f *FlexCommand) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var s string
		if err := value.Decode(&s); err != nil {
			return err
		}
		*f = FlexCommand{s}
		return nil
	case yaml.SequenceNode:
		var ss []string
		if err := value.Decode(&ss); err != nil {
			return err
		}
		*f = FlexCommand(ss)
		return nil
	default:
		return fmt.Errorf("command must be a string or list of strings")
	}
}

// RuntimeKubernetesConfig holds K8s-specific runtime/transport settings for one runtime.
// Credential selection (auth_profile, auth_binding) lives on RuntimeEntry, not here.
type RuntimeKubernetesConfig struct {
	Env   []HostEnvVar `yaml:"env,omitempty"`
	Image string       `yaml:"image,omitempty"` // per-runtime container image override
}

// HostKubernetesConfig is a deprecated alias for RuntimeKubernetesConfig.
type HostKubernetesConfig = RuntimeKubernetesConfig

// RuntimeEntry describes one execution runtime (e.g., "claude", "codex").
// AuthProfile and AuthBinding apply across all deployment modes (local, K8s host, K8s in-cluster).
type RuntimeEntry struct {
	Adapter     string                  `yaml:"adapter"`
	Model       string                  `yaml:"model,omitempty"`
	ModelTiers  map[string]string       `yaml:"model_tiers,omitempty"`
	Env         map[string]string       `yaml:"env,omitempty"`
	AuthProfile string                  `yaml:"auth_profile,omitempty"` // references auth.credentials.profiles.<name>
	AuthBinding *CredentialBinding      `yaml:"auth_binding,omitempty"` // per-runtime binding override
	Kubernetes  RuntimeKubernetesConfig `yaml:"kubernetes,omitempty"`
}

// HostConfig is a deprecated alias for RuntimeEntry.
type HostConfig = RuntimeEntry

// OntologySchemaEntry points to a single schema set directory.
type OntologySchemaEntry struct {
	Path string `yaml:"path"`
}

// OntologyConfig controls multi-schema graph ontology loading.
type OntologyConfig struct {
	Schemas []OntologySchemaEntry `yaml:"schemas"`
}

// LoggingConfig controls where and how fracta logs.
type LoggingConfig struct {
	File  string `yaml:"file,omitempty"`  // Log file path (absolute, relative to CWD, or bare filename)
	Level string `yaml:"level,omitempty"` // "debug", "info", "warn", "error" (default: FRACTA_LOG_LEVEL or "info")
}

// Config is the top-level fracta configuration.
type Config struct {
	Project         ProjectConfig               `yaml:"project"`
	Agents          AgentsConfig                `yaml:"agents"`
	Runtimes        map[string]RuntimeEntry     `yaml:"runtimes,omitempty"` // Deprecated: use agents.agent_runtimes
	Hosts           map[string]RuntimeEntry     `yaml:"hosts,omitempty"`    // Deprecated: use agents.agent_runtimes
	Connections     map[string]ConnectionConfig `yaml:"connections"`
	Profile         string                      `yaml:"profile,omitempty"` // "local" or "kubernetes"
	Logging         LoggingConfig               `yaml:"logging"`
	Runtime         RuntimeConfig               `yaml:"runtime"`
	Gateway         GatewayConfig               `yaml:"gateway"`
	ControlPlaneAPI ControlPlaneAPIConfig       `yaml:"control_plane_api,omitempty"`
	Auth            AuthConfig                  `yaml:"auth"`
	Reaper          ReaperConfig                `yaml:"reaper"`
	MCPServers      MCPServersConfig            `yaml:"mcp_servers"`
	Registry        RegistryConfig              `yaml:"registry"`
	Authz           AuthzConfig                 `yaml:"authz"`
	Ontology        OntologyConfig              `yaml:"ontology"`
	Strategy        StrategyConfig              `yaml:"strategy"`
	Observability   ObservabilityConfig         `yaml:"observability"`
	TokenStore      TokenStoreConfig            `yaml:"token_store,omitempty"`
}

// EffectiveRuntimes returns the resolved agent runtime map.
func (c *Config) EffectiveRuntimes() map[string]RuntimeEntry {
	if len(c.Agents.AgentRuntimes) > 0 {
		return c.Agents.AgentRuntimes
	}
	if len(c.Runtimes) > 0 {
		return c.Runtimes
	}
	return c.Hosts
}

// ResolvedProfile returns the effective profile name.
// Priority: explicit Profile field > inferred from Runtime.Backend > "local".
func (c *Config) ResolvedProfile() string {
	if c.Profile != "" {
		return c.Profile
	}
	if c.Runtime.Backend == "kubernetes" {
		return "kubernetes"
	}
	return "local"
}

// DefaultRuntime returns a RuntimeConfig for local development.
func DefaultRuntime() RuntimeConfig {
	return RuntimeConfig{
		Backend: "local",
		Kubernetes: KubernetesConfig{
			Namespace:     "fracta",
			Image:         "fracta/agent:latest",
			JobTTLSeconds: 300,
		},
		State: StateConfig{
			Path: ".fracta/state.db",
		},
	}
}

// LoadConfig reads a YAML config file and resolves ${VAR} references from
// environment variables. Returns an error if the file can't be read or parsed.
// Unset environment variables resolve to empty strings.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	return ParseConfig(data)
}

// ParseConfig parses YAML bytes and resolves ${VAR} references from
// environment variables.
func ParseConfig(data []byte) (*Config, error) {
	resolved := resolveEnvVars(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(resolved), &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if len(cfg.Agents.AgentRuntimes) > 0 && len(cfg.Runtimes) > 0 {
		fractalog.Component("config").Warn("'runtimes:' config key is deprecated and ignored because 'agents.agent_runtimes' is set")
	} else if len(cfg.Runtimes) > 0 {
		fractalog.Component("config").Warn("'runtimes:' config key is deprecated — move entries to 'agents.agent_runtimes'")
	}
	if len(cfg.Hosts) > 0 {
		fractalog.Component("config").Warn("'hosts:' config key is deprecated — move entries to 'agents.agent_runtimes'")
	}
	// Dual-read for agents.default_runtime / agents.default_host_type.
	if cfg.Agents.DefaultRuntime == "" && cfg.Agents.DefaultHostType != "" {
		fractalog.Component("config").Warn("'agents.default_host_type' is deprecated — use 'agents.default_runtime'")
		cfg.Agents.DefaultRuntime = cfg.Agents.DefaultHostType
	}

	// Validate credential profiles eagerly so malformed configs fail at load, not at runtime.
	for name, profile := range cfg.Auth.Credentials.Profiles {
		if err := profile.ValidateCredentialProfile(name); err != nil {
			return nil, err
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks profile-specific configuration constraints.
func (c *Config) Validate() error {
	if c.ResolvedProfile() == "kubernetes" && c.Runtime.StagingDir == "" {
		return fmt.Errorf("runtime.staging_dir must be set when profile is kubernetes")
	}
	for name, entry := range c.MCPServers.Servers {
		remote, ok := entry.EffectiveRemote()
		if !ok || remote.Auth == nil {
			continue
		}
		if err := remote.Auth.Validate(name); err != nil {
			return err
		}
		if remote.Auth.Type != "" && remote.Auth.Type != "none" {
			if hasAuthorizationHeader(remote.Headers) {
				return fmt.Errorf("mcp_servers.%s: 'auth' and 'Authorization' header are mutually exclusive", name)
			}
		}
		if remote.Auth.Type == "header" && remote.Auth.HeaderName != "" {
			for k := range remote.Headers {
				if strings.EqualFold(k, remote.Auth.HeaderName) {
					return fmt.Errorf("mcp_servers.%s: auth.header_name %q collides with existing header", name, remote.Auth.HeaderName)
				}
			}
		}
	}
	return nil
}

func hasAuthorizationHeader(headers map[string]string) bool {
	for k := range headers {
		if strings.EqualFold(k, "authorization") {
			return true
		}
	}
	return false
}

// resolveEnvVars replaces all ${VAR} references with os.Getenv(VAR).
func resolveEnvVars(s string) string {
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		varName := envVarPattern.FindStringSubmatch(match)[1]
		return os.Getenv(varName)
	})
}

// legacyConfig mirrors the JSON shape of .fracta/config.json for backward compat.
type legacyConfig struct {
	DefaultBaseBranch string            `json:"default_base_branch"`
	Model             string            `json:"model,omitempty"`
	Mode              string            `json:"mode,omitempty"`
	ModelTiers        map[string]string `json:"model_tiers,omitempty"`
	AllowedTools      []string          `json:"allowed_tools"`
}

// readLegacyConfig reads .fracta/config.json from root. Returns zero value on any error.
func readLegacyConfig(root string) (legacyConfig, bool) {
	path := filepath.Join(root, ".fracta", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return legacyConfig{}, false
	}
	var lc legacyConfig
	if err := json.Unmarshal(data, &lc); err != nil {
		return legacyConfig{}, false
	}
	return lc, true
}

// EffectiveProjectConfig returns the project config from fracta.yaml,
// falling back to .fracta/config.json with a deprecation warning.
func (c *Config) EffectiveProjectConfig(root string) ProjectConfig {
	pc := c.Project
	if pc.DefaultBaseBranch != "" || len(pc.AllowedTools) > 0 {
		return pc
	}
	lc, ok := readLegacyConfig(root)
	if !ok {
		return pc
	}
	fractalog.Component("config").Warn("using .fracta/config.json for project config — migrate to fracta.yaml project section")
	if pc.DefaultBaseBranch == "" && lc.DefaultBaseBranch != "" {
		pc.DefaultBaseBranch = lc.DefaultBaseBranch
	}
	if len(pc.AllowedTools) == 0 && len(lc.AllowedTools) > 0 {
		pc.AllowedTools = lc.AllowedTools
	}
	return pc
}

// EffectiveRuntimeConfig returns the runtime config from fracta.yaml
// agents.agent_runtimes, falling back to deprecated runtime maps or
// .fracta/config.json model fields.
func (c *Config) EffectiveRuntimeConfig(runtimeType, root string) (RuntimeEntry, bool) {
	runtimes := c.EffectiveRuntimes()
	if runtimes != nil {
		hc, ok := runtimes[runtimeType]
		if ok {
			return hc, true
		}
	}
	lc, ok := readLegacyConfig(root)
	if !ok {
		return RuntimeEntry{}, false
	}
	fractalog.Component("config").Warn("using .fracta/config.json for runtime config — migrate to fracta.yaml agents.agent_runtimes",
		"runtime", runtimeType)
	return RuntimeEntry{
		Adapter:    runtimeType,
		Model:      lc.Model,
		ModelTiers: lc.ModelTiers,
	}, true
}

// EffectiveHostConfig is a deprecated alias for EffectiveRuntimeConfig.
func (c *Config) EffectiveHostConfig(hostType, root string) (RuntimeEntry, bool) {
	return c.EffectiveRuntimeConfig(hostType, root)
}
