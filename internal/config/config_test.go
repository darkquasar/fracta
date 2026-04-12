package config

import (
	"os"
	"testing"
)

func TestParseConfig(t *testing.T) {
	yaml := `
connections:
  elastic_main:
    type: elasticsearch
    url: https://elastic.example.com:9200
    api_key: test-key-123
  falkordb:
    type: redis
    url: redis://localhost:6379
`
	cfg, err := ParseConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	if len(cfg.Connections) != 2 {
		t.Fatalf("expected 2 connections, got %d", len(cfg.Connections))
	}

	elastic := cfg.Connections["elastic_main"]
	if elastic.Type != "elasticsearch" {
		t.Errorf("elastic type = %q, want %q", elastic.Type, "elasticsearch")
	}
	if elastic.URL != "https://elastic.example.com:9200" {
		t.Errorf("elastic url = %q", elastic.URL)
	}
	if elastic.APIKey != "test-key-123" {
		t.Errorf("elastic api_key = %q", elastic.APIKey)
	}

	falkor := cfg.Connections["falkordb"]
	if falkor.Type != "redis" {
		t.Errorf("falkordb type = %q, want %q", falkor.Type, "redis")
	}
	if falkor.URL != "redis://localhost:6379" {
		t.Errorf("falkordb url = %q", falkor.URL)
	}
}

func TestParseConfigEnvVarResolution(t *testing.T) {
	os.Setenv("TEST_ELASTIC_URL", "https://resolved.example.com")
	os.Setenv("TEST_API_KEY", "secret-key")
	defer os.Unsetenv("TEST_ELASTIC_URL")
	defer os.Unsetenv("TEST_API_KEY")

	yaml := `
connections:
  elastic_main:
    type: elasticsearch
    url: ${TEST_ELASTIC_URL}
    api_key: ${TEST_API_KEY}
`
	cfg, err := ParseConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	elastic := cfg.Connections["elastic_main"]
	if elastic.URL != "https://resolved.example.com" {
		t.Errorf("url = %q, want resolved value", elastic.URL)
	}
	if elastic.APIKey != "secret-key" {
		t.Errorf("api_key = %q, want resolved value", elastic.APIKey)
	}
}

func TestParseConfigUnsetEnvVar(t *testing.T) {
	os.Unsetenv("DEFINITELY_UNSET_VAR")

	yaml := `
connections:
  test:
    type: elasticsearch
    url: ${DEFINITELY_UNSET_VAR}
`
	cfg, err := ParseConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	if cfg.Connections["test"].URL != "" {
		t.Errorf("unset env var should resolve to empty string, got %q", cfg.Connections["test"].URL)
	}
}

func TestParseConfigEmptyConnections(t *testing.T) {
	yaml := `
connections: {}
`
	cfg, err := ParseConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(cfg.Connections) != 0 {
		t.Errorf("expected 0 connections, got %d", len(cfg.Connections))
	}
}

func TestParseConfigProjectSection(t *testing.T) {
	input := `
project:
  default_base_branch: develop
  allowed_tools:
    - Read
    - Edit
    - Bash(git *)
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Project.DefaultBaseBranch != "develop" {
		t.Errorf("DefaultBaseBranch = %q, want %q", cfg.Project.DefaultBaseBranch, "develop")
	}
	if len(cfg.Project.AllowedTools) != 3 {
		t.Fatalf("AllowedTools count = %d, want 3", len(cfg.Project.AllowedTools))
	}
	if cfg.Project.AllowedTools[0] != "Read" {
		t.Errorf("AllowedTools[0] = %q, want %q", cfg.Project.AllowedTools[0], "Read")
	}
}

func TestParseConfigAgentsSection(t *testing.T) {
	input := `
agents:
  default_host_type: codex
  default_mode: stream
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Agents.EffectiveDefaultRuntime() != "codex" {
		t.Errorf("DefaultRuntime = %q, want %q", cfg.Agents.EffectiveDefaultRuntime(), "codex")
	}
	if cfg.Agents.DefaultMode != "stream" {
		t.Errorf("DefaultMode = %q, want %q", cfg.Agents.DefaultMode, "stream")
	}
}

func TestParseConfigAgentRuntimesSection(t *testing.T) {
	input := `
agents:
  default_runtime: claude
  agent_runtimes:
    claude:
      adapter: claude
      model: claude-sonnet-4-5
      model_tiers:
        heavy: claude-opus
        medium: claude-sonnet-4-5
        light: claude-haiku
      env:
        LOCAL_VAR: some-value
      kubernetes:
        env:
          - name: AUTH_TOKEN
            secret_ref:
              name: my-secret
              key: token
          - name: REGION
            value: us-east-1
    codex:
      adapter: codex
      model: codex-default
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	if len(cfg.Agents.AgentRuntimes) != 2 {
		t.Fatalf("AgentRuntimes count = %d, want 2", len(cfg.Agents.AgentRuntimes))
	}

	claude := cfg.EffectiveRuntimes()["claude"]
	if claude.Adapter != "claude" {
		t.Errorf("claude.Adapter = %q, want %q", claude.Adapter, "claude")
	}
	if claude.Model != "claude-sonnet-4-5" {
		t.Errorf("claude.Model = %q, want %q", claude.Model, "claude-sonnet-4-5")
	}
	if len(claude.ModelTiers) != 3 {
		t.Fatalf("claude.ModelTiers count = %d, want 3", len(claude.ModelTiers))
	}
	if claude.ModelTiers["heavy"] != "claude-opus" {
		t.Errorf("claude.ModelTiers[heavy] = %q", claude.ModelTiers["heavy"])
	}

	if claude.Env["LOCAL_VAR"] != "some-value" {
		t.Errorf("claude.Env[LOCAL_VAR] = %q", claude.Env["LOCAL_VAR"])
	}

	k8sEnv := claude.Kubernetes.Env
	if len(k8sEnv) != 2 {
		t.Fatalf("claude.Kubernetes.Env count = %d, want 2", len(k8sEnv))
	}
	if k8sEnv[0].Name != "AUTH_TOKEN" {
		t.Errorf("k8sEnv[0].Name = %q, want %q", k8sEnv[0].Name, "AUTH_TOKEN")
	}
	if k8sEnv[0].SecretRef == nil {
		t.Fatal("k8sEnv[0].SecretRef is nil")
	}
	if k8sEnv[0].SecretRef.Name != "my-secret" {
		t.Errorf("SecretRef.Name = %q, want %q", k8sEnv[0].SecretRef.Name, "my-secret")
	}
	if k8sEnv[0].SecretRef.Key != "token" {
		t.Errorf("SecretRef.Key = %q, want %q", k8sEnv[0].SecretRef.Key, "token")
	}
	if k8sEnv[1].Name != "REGION" {
		t.Errorf("k8sEnv[1].Name = %q, want %q", k8sEnv[1].Name, "REGION")
	}
	if k8sEnv[1].Value != "us-east-1" {
		t.Errorf("k8sEnv[1].Value = %q, want %q", k8sEnv[1].Value, "us-east-1")
	}

	codex := cfg.EffectiveRuntimes()["codex"]
	if codex.Adapter != "codex" {
		t.Errorf("codex.Adapter = %q", codex.Adapter)
	}
	if codex.Model != "codex-default" {
		t.Errorf("codex.Model = %q", codex.Model)
	}
}

func TestParseConfigHostsSection(t *testing.T) {
	input := `
hosts:
  claude:
    adapter: claude
    model: claude-sonnet-4-5
    model_tiers:
      heavy: claude-opus
      medium: claude-sonnet-4-5
      light: claude-haiku
    env:
      LOCAL_VAR: some-value
    kubernetes:
      env:
        - name: AUTH_TOKEN
          secret_ref:
            name: my-secret
            key: token
        - name: REGION
          value: us-east-1
  codex:
    adapter: codex
    model: codex-default
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	if len(cfg.Hosts) != 2 {
		t.Fatalf("Hosts count = %d, want 2", len(cfg.Hosts))
	}

	claude := cfg.EffectiveRuntimes()["claude"]
	if claude.Adapter != "claude" {
		t.Errorf("claude.Adapter = %q, want %q", claude.Adapter, "claude")
	}
	if claude.Model != "claude-sonnet-4-5" {
		t.Errorf("claude.Model = %q, want %q", claude.Model, "claude-sonnet-4-5")
	}
	if len(claude.ModelTiers) != 3 {
		t.Fatalf("claude.ModelTiers count = %d, want 3", len(claude.ModelTiers))
	}
	if claude.ModelTiers["heavy"] != "claude-opus" {
		t.Errorf("claude.ModelTiers[heavy] = %q", claude.ModelTiers["heavy"])
	}

	// Local env
	if claude.Env["LOCAL_VAR"] != "some-value" {
		t.Errorf("claude.Env[LOCAL_VAR] = %q", claude.Env["LOCAL_VAR"])
	}

	// K8s env
	k8sEnv := claude.Kubernetes.Env
	if len(k8sEnv) != 2 {
		t.Fatalf("claude.Kubernetes.Env count = %d, want 2", len(k8sEnv))
	}

	// Secret ref entry
	if k8sEnv[0].Name != "AUTH_TOKEN" {
		t.Errorf("k8sEnv[0].Name = %q, want %q", k8sEnv[0].Name, "AUTH_TOKEN")
	}
	if k8sEnv[0].SecretRef == nil {
		t.Fatal("k8sEnv[0].SecretRef is nil")
	}
	if k8sEnv[0].SecretRef.Name != "my-secret" {
		t.Errorf("SecretRef.Name = %q, want %q", k8sEnv[0].SecretRef.Name, "my-secret")
	}
	if k8sEnv[0].SecretRef.Key != "token" {
		t.Errorf("SecretRef.Key = %q, want %q", k8sEnv[0].SecretRef.Key, "token")
	}

	// Plain value entry
	if k8sEnv[1].Name != "REGION" {
		t.Errorf("k8sEnv[1].Name = %q, want %q", k8sEnv[1].Name, "REGION")
	}
	if k8sEnv[1].Value != "us-east-1" {
		t.Errorf("k8sEnv[1].Value = %q, want %q", k8sEnv[1].Value, "us-east-1")
	}
	if k8sEnv[1].SecretRef != nil {
		t.Error("k8sEnv[1].SecretRef should be nil for plain value")
	}

	// Codex host
	codex := cfg.EffectiveRuntimes()["codex"]
	if codex.Adapter != "codex" {
		t.Errorf("codex.Adapter = %q", codex.Adapter)
	}
	if codex.Model != "codex-default" {
		t.Errorf("codex.Model = %q", codex.Model)
	}
}

func TestParseConfigFullCanonical(t *testing.T) {
	input := `
project:
  default_base_branch: main
  allowed_tools:
    - Read
    - Edit

agents:
  default_runtime: claude
  default_mode: batch
  agent_runtimes:
    claude:
      adapter: claude
      model: claude-sonnet
      model_tiers:
        heavy: claude-opus
        medium: claude-sonnet
        light: claude-haiku

runtime:
  backend: local
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	if cfg.Project.DefaultBaseBranch != "main" {
		t.Errorf("Project.DefaultBaseBranch = %q", cfg.Project.DefaultBaseBranch)
	}
	if cfg.Agents.EffectiveDefaultRuntime() != "claude" {
		t.Errorf("Agents.DefaultRuntime = %q", cfg.Agents.EffectiveDefaultRuntime())
	}
	if cfg.Agents.DefaultMode != "batch" {
		t.Errorf("Agents.DefaultMode = %q", cfg.Agents.DefaultMode)
	}
	if len(cfg.Agents.AgentRuntimes) != 1 {
		t.Errorf("AgentRuntimes count = %d, want 1", len(cfg.Agents.AgentRuntimes))
	}
	if cfg.Runtime.Backend != "local" {
		t.Errorf("Runtime.Backend = %q", cfg.Runtime.Backend)
	}
}

func TestEffectiveRuntimes_Precedence(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			AgentRuntimes: map[string]RuntimeEntry{
				"claude": {Adapter: "claude", Model: "agent-runtime-model"},
			},
		},
		Runtimes: map[string]RuntimeEntry{
			"claude": {Adapter: "claude", Model: "deprecated-runtime-model"},
		},
		Hosts: map[string]RuntimeEntry{
			"claude": {Adapter: "claude", Model: "deprecated-host-model"},
		},
	}

	got := cfg.EffectiveRuntimes()["claude"]
	if got.Model != "agent-runtime-model" {
		t.Errorf("EffectiveRuntimes()[claude].Model = %q, want agent-runtime-model", got.Model)
	}
}

func TestEffectiveRuntimes_DeprecatedRuntimesFallback(t *testing.T) {
	cfg := &Config{
		Runtimes: map[string]RuntimeEntry{
			"claude": {Adapter: "claude", Model: "deprecated-runtime-model"},
		},
		Hosts: map[string]RuntimeEntry{
			"claude": {Adapter: "claude", Model: "deprecated-host-model"},
		},
	}

	got := cfg.EffectiveRuntimes()["claude"]
	if got.Model != "deprecated-runtime-model" {
		t.Errorf("EffectiveRuntimes()[claude].Model = %q, want deprecated-runtime-model", got.Model)
	}
}

func TestParseConfigDeprecatedRuntimesFallback(t *testing.T) {
	input := `
runtimes:
  claude:
    adapter: claude
    model: deprecated-runtime-model
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	got := cfg.EffectiveRuntimes()["claude"]
	if got.Model != "deprecated-runtime-model" {
		t.Errorf("EffectiveRuntimes()[claude].Model = %q, want deprecated-runtime-model", got.Model)
	}
}

func TestParseConfigAgentRuntimesWinsOverDeprecatedRuntimes(t *testing.T) {
	input := `
agents:
  agent_runtimes:
    claude:
      adapter: claude
      model: agent-runtime-model
runtimes:
  claude:
    adapter: claude
    model: deprecated-runtime-model
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	got := cfg.EffectiveRuntimes()["claude"]
	if got.Model != "agent-runtime-model" {
		t.Errorf("EffectiveRuntimes()[claude].Model = %q, want agent-runtime-model", got.Model)
	}
}

func TestParseConfigEmptyHostsSection(t *testing.T) {
	input := `
hosts: {}
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(cfg.Hosts) != 0 {
		t.Errorf("Hosts count = %d, want 0", len(cfg.Hosts))
	}
}

func TestParseConfigOmittedSections(t *testing.T) {
	input := `
runtime:
  backend: local
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Project.DefaultBaseBranch != "" {
		t.Errorf("Project.DefaultBaseBranch should be empty, got %q", cfg.Project.DefaultBaseBranch)
	}
	if cfg.Agents.EffectiveDefaultRuntime() != "" {
		t.Errorf("Agents.DefaultRuntime should be empty, got %q", cfg.Agents.EffectiveDefaultRuntime())
	}
	if cfg.Hosts != nil {
		t.Errorf("Hosts should be nil, got %v", cfg.Hosts)
	}
}

func TestParseMCPServerRemoteSection(t *testing.T) {
	input := `
mcp_servers:
  servers:
    elastic:
      remote:
        url: http://elastic-mcp:8000/mcp
        transport: streamable-http
        headers:
          Authorization: Bearer token
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	entry := cfg.MCPServers.Servers["elastic"]
	if entry.Remote == nil {
		t.Fatal("Remote is nil")
	}
	if entry.Remote.URL != "http://elastic-mcp:8000/mcp" {
		t.Errorf("Remote.URL = %q", entry.Remote.URL)
	}
	if entry.Remote.Transport != "streamable-http" {
		t.Errorf("Remote.Transport = %q", entry.Remote.Transport)
	}
	if entry.Remote.Headers["Authorization"] != "Bearer token" {
		t.Errorf("Remote.Headers[Authorization] = %q", entry.Remote.Headers["Authorization"])
	}
}

func TestBuildHostEnv_KubernetesBackend(t *testing.T) {
	hc := HostConfig{
		Kubernetes: HostKubernetesConfig{
			Env: []HostEnvVar{
				{Name: "TEST_TOKEN", SecretRef: &HostSecretRef{Name: "my-secret", Key: "token"}},
				{Name: "TEST_REGION", Value: "us-west-2"},
			},
		},
	}

	entries, err := BuildHostEnv(hc, "kubernetes")
	if err != nil {
		t.Fatalf("BuildHostEnv: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries count = %d, want 2", len(entries))
	}

	// SecretRef entry
	if entries[0].Name != "TEST_TOKEN" {
		t.Errorf("entries[0].Name = %q", entries[0].Name)
	}
	if entries[0].SecretRef == nil {
		t.Fatal("entries[0].SecretRef is nil")
	}
	if entries[0].SecretRef.Name != "my-secret" {
		t.Errorf("SecretRef.Name = %q", entries[0].SecretRef.Name)
	}

	// Plain value entry
	if entries[1].Name != "TEST_REGION" || entries[1].Value != "us-west-2" {
		t.Errorf("entries[1] = {%q, %q}", entries[1].Name, entries[1].Value)
	}
	if entries[1].SecretRef != nil {
		t.Error("plain value should have nil SecretRef")
	}
}

func TestBuildHostEnv_LocalBackend(t *testing.T) {
	hc := HostConfig{
		Env: map[string]string{
			"TEST_VAR": "local-value",
		},
	}

	entries, err := BuildHostEnv(hc, "local")
	if err != nil {
		t.Fatalf("BuildHostEnv: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries count = %d, want 1", len(entries))
	}
	if entries[0].Name != "TEST_VAR" || entries[0].Value != "local-value" {
		t.Errorf("entry = {%q, %q}", entries[0].Name, entries[0].Value)
	}
}

func TestBuildHostEnv_LocalRejectsKubernetesEnv(t *testing.T) {
	hc := HostConfig{
		Env: map[string]string{"TEST_OK": "fine"},
		Kubernetes: HostKubernetesConfig{
			Env: []HostEnvVar{
				{Name: "TEST_K8S_ONLY", Value: "should-not-be-here"},
			},
		},
	}

	_, err := BuildHostEnv(hc, "local")
	if err == nil {
		t.Fatal("BuildHostEnv should error when kubernetes.env is populated for local backend")
	}
}

func TestBuildHostEnv_EmptyConfig(t *testing.T) {
	entries, err := BuildHostEnv(HostConfig{}, "local")
	if err != nil {
		t.Fatalf("BuildHostEnv: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries count = %d, want 0", len(entries))
	}
}

func TestEffectiveProjectConfig_UsesYAML(t *testing.T) {
	cfg := &Config{
		Project: ProjectConfig{
			DefaultBaseBranch: "develop",
			AllowedTools:      []string{"Read"},
		},
	}
	pc := cfg.EffectiveProjectConfig("/nonexistent")
	if pc.DefaultBaseBranch != "develop" {
		t.Errorf("DefaultBaseBranch = %q, want develop", pc.DefaultBaseBranch)
	}
	if len(pc.AllowedTools) != 1 || pc.AllowedTools[0] != "Read" {
		t.Errorf("AllowedTools = %v", pc.AllowedTools)
	}
}

func TestEffectiveProjectConfig_NoFallbackWhenMissing(t *testing.T) {
	cfg := &Config{}
	pc := cfg.EffectiveProjectConfig("/nonexistent/path")
	if pc.DefaultBaseBranch != "" {
		t.Errorf("DefaultBaseBranch should be empty, got %q", pc.DefaultBaseBranch)
	}
}

func TestEffectiveHostConfig_UsesYAML(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			AgentRuntimes: map[string]RuntimeEntry{
				"claude": {Adapter: "claude", Model: "test-model"},
			},
		},
	}
	hc, ok := cfg.EffectiveHostConfig("claude", "/nonexistent")
	if !ok {
		t.Fatal("should return true for configured host")
	}
	if hc.Model != "test-model" {
		t.Errorf("Model = %q", hc.Model)
	}
}

func TestEffectiveHostConfig_MissingHostNoFallback(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			AgentRuntimes: map[string]RuntimeEntry{
				"claude": {Adapter: "claude"},
			},
		},
	}
	_, ok := cfg.EffectiveHostConfig("codex", "/nonexistent/path")
	if ok {
		t.Error("should return false for unconfigured host without legacy config")
	}
}

func TestEffectiveProjectConfig_FallbackToLegacy(t *testing.T) {
	// Create a temp dir with .fracta/config.json
	dir := t.TempDir()
	fractaDir := dir + "/.fracta"
	os.MkdirAll(fractaDir, 0755)
	os.WriteFile(fractaDir+"/config.json", []byte(`{
		"default_base_branch": "legacy-main",
		"allowed_tools": ["Bash"]
	}`), 0644)

	cfg := &Config{} // empty YAML config
	pc := cfg.EffectiveProjectConfig(dir)
	if pc.DefaultBaseBranch != "legacy-main" {
		t.Errorf("DefaultBaseBranch = %q, want legacy-main", pc.DefaultBaseBranch)
	}
	if len(pc.AllowedTools) != 1 || pc.AllowedTools[0] != "Bash" {
		t.Errorf("AllowedTools = %v", pc.AllowedTools)
	}
}

func TestEffectiveHostConfig_FallbackToLegacy(t *testing.T) {
	dir := t.TempDir()
	fractaDir := dir + "/.fracta"
	os.MkdirAll(fractaDir, 0755)
	os.WriteFile(fractaDir+"/config.json", []byte(`{
		"model": "legacy-model",
		"model_tiers": {"heavy": "legacy-heavy"}
	}`), 0644)

	cfg := &Config{} // no hosts configured in YAML
	hc, ok := cfg.EffectiveHostConfig("claude", dir)
	if !ok {
		t.Fatal("should return true with legacy fallback")
	}
	if hc.Model != "legacy-model" {
		t.Errorf("Model = %q, want legacy-model", hc.Model)
	}
	if hc.ModelTiers["heavy"] != "legacy-heavy" {
		t.Errorf("ModelTiers[heavy] = %q", hc.ModelTiers["heavy"])
	}
	if hc.Adapter != "claude" {
		t.Errorf("Adapter = %q, want claude (from hostType arg)", hc.Adapter)
	}
}

func TestParseKubernetesImage(t *testing.T) {
	input := `
agents:
  agent_runtimes:
    claude:
      adapter: claude
      kubernetes:
        image: fracta/agent-claude:latest
    codex:
      adapter: codex
      kubernetes:
        image: fracta/agent-codex:latest
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	if cfg.EffectiveRuntimes()["claude"].Kubernetes.Image != "fracta/agent-claude:latest" {
		t.Errorf("claude.Kubernetes.Image = %q", cfg.EffectiveRuntimes()["claude"].Kubernetes.Image)
	}
	if cfg.EffectiveRuntimes()["codex"].Kubernetes.Image != "fracta/agent-codex:latest" {
		t.Errorf("codex.Kubernetes.Image = %q", cfg.EffectiveRuntimes()["codex"].Kubernetes.Image)
	}
}

// --- New Credential Pipeline Tests (spec-33) ---

func TestParseCredentialProfile(t *testing.T) {
	input := `
auth:
  credentials:
    profiles:
      bedrock:
        auth_origins:
          proxy:
            type: http_header_token
            scope: agent_runtime
            request:
              method: HEAD
              url: https://proxy.example.com
              headers:
                X-AWS-Region: ap-southeast-2
            extract:
              header: x-bedrock-token
          host_fallback:
            type: command_output
            scope: host_edge
            command: ["bedrock-auth-helper"]
            delivery: file_mount
            path: /var/run/fracta-auth/bedrock-token
            required: false
        runtime_auth_resolvers:
          bedrock_helper:
            type: command
            command: /usr/local/bin/fetch-bedrock-token
            ttl_ms: 60000
            order: [proxy, host_fallback]
        env:
          AWS_REGION: ap-southeast-2
          CLAUDE_CODE_USE_BEDROCK: "1"
        assertions:
          require_env: [AWS_REGION]
          forbid_env: [CLAUDE_CODE_SIMPLE]
          require_source: [proxy]
          warn_if_missing_env: [FRACTA_CREDENTIALS_DEBUG]
        default_binding:
          type: claude_api_key_helper
          runtime_auth_resolver: bedrock_helper

agents:
  agent_runtimes:
    claude:
      adapter: claude
      auth_profile: bedrock
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	profiles := cfg.Auth.Credentials.Profiles
	if len(profiles) != 1 {
		t.Fatalf("profiles count = %d, want 1", len(profiles))
	}

	bp := profiles["bedrock"]

	// Sources
	if len(bp.AuthOrigins) != 2 {
		t.Fatalf("sources count = %d, want 2", len(bp.AuthOrigins))
	}
	proxy := bp.AuthOrigins["proxy"]
	if proxy.Type != "http_header_token" {
		t.Errorf("proxy.Type = %q", proxy.Type)
	}
	if proxy.Scope != "agent_runtime" {
		t.Errorf("proxy.Scope = %q", proxy.Scope)
	}
	if proxy.Request == nil {
		t.Fatal("proxy.Request is nil")
	}
	if proxy.Request.Method != "HEAD" {
		t.Errorf("proxy.Request.Method = %q", proxy.Request.Method)
	}
	if proxy.Request.Headers["X-AWS-Region"] != "ap-southeast-2" {
		t.Errorf("proxy.Request.Headers = %v", proxy.Request.Headers)
	}
	if proxy.Extract == nil || proxy.Extract.Header != "x-bedrock-token" {
		t.Errorf("proxy.Extract = %v", proxy.Extract)
	}

	fallback := bp.AuthOrigins["host_fallback"]
	if fallback.Type != "command_output" {
		t.Errorf("fallback.Type = %q", fallback.Type)
	}
	if fallback.Scope != "host_edge" {
		t.Errorf("fallback.Scope = %q", fallback.Scope)
	}
	if len(fallback.Command) != 1 || fallback.Command[0] != "bedrock-auth-helper" {
		t.Errorf("fallback.Command = %v", fallback.Command)
	}
	if fallback.Delivery != "file_mount" {
		t.Errorf("fallback.Delivery = %q", fallback.Delivery)
	}
	if fallback.Path != "/var/run/fracta-auth/bedrock-token" {
		t.Errorf("fallback.Path = %q", fallback.Path)
	}
	if fallback.IsRequired() {
		t.Error("fallback.IsRequired() should be false when explicitly set")
	}

	// Resolvers
	if len(bp.RuntimeAuthResolvers) != 1 {
		t.Fatalf("resolvers count = %d, want 1", len(bp.RuntimeAuthResolvers))
	}
	res := bp.RuntimeAuthResolvers["bedrock_helper"]
	if res.Type != "command" {
		t.Errorf("resolver.Type = %q", res.Type)
	}
	if res.Command != "/usr/local/bin/fetch-bedrock-token" {
		t.Errorf("resolver.Command = %q", res.Command)
	}
	if res.TTLMs != 60000 {
		t.Errorf("resolver.TTLMs = %d", res.TTLMs)
	}
	if len(res.Order) != 2 || res.Order[0] != "proxy" || res.Order[1] != "host_fallback" {
		t.Errorf("resolver.Order = %v", res.Order)
	}

	// Env
	if bp.Env["AWS_REGION"] != "ap-southeast-2" {
		t.Errorf("Env[AWS_REGION] = %q", bp.Env["AWS_REGION"])
	}
	if bp.Env["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Errorf("Env[CLAUDE_CODE_USE_BEDROCK] = %q", bp.Env["CLAUDE_CODE_USE_BEDROCK"])
	}

	// Assertions
	if bp.Assertions == nil {
		t.Fatal("Assertions is nil")
	}
	if len(bp.Assertions.RequireEnv) != 1 || bp.Assertions.RequireEnv[0] != "AWS_REGION" {
		t.Errorf("RequireEnv = %v", bp.Assertions.RequireEnv)
	}
	if len(bp.Assertions.ForbidEnv) != 1 || bp.Assertions.ForbidEnv[0] != "CLAUDE_CODE_SIMPLE" {
		t.Errorf("ForbidEnv = %v", bp.Assertions.ForbidEnv)
	}
	if len(bp.Assertions.RequireSource) != 1 || bp.Assertions.RequireSource[0] != "proxy" {
		t.Errorf("RequireSource = %v", bp.Assertions.RequireSource)
	}
	if len(bp.Assertions.WarnIfMissing) != 1 || bp.Assertions.WarnIfMissing[0] != "FRACTA_CREDENTIALS_DEBUG" {
		t.Errorf("WarnIfMissing = %v", bp.Assertions.WarnIfMissing)
	}

	// Default binding
	if bp.DefaultBinding == nil {
		t.Fatal("DefaultBinding is nil")
	}
	if bp.DefaultBinding.Type != "claude_api_key_helper" {
		t.Errorf("DefaultBinding.Type = %q", bp.DefaultBinding.Type)
	}
	if bp.DefaultBinding.RuntimeAuthResolver != "bedrock_helper" {
		t.Errorf("DefaultBinding.RuntimeAuthResolver = %q", bp.DefaultBinding.RuntimeAuthResolver)
	}

	// Host reference
	if cfg.EffectiveRuntimes()["claude"].AuthProfile != "bedrock" {
		t.Errorf("claude.AuthProfile = %q", cfg.EffectiveRuntimes()["claude"].AuthProfile)
	}
}

func TestParseCredentialProfile_HostBindingOverride(t *testing.T) {
	input := `
auth:
  credentials:
    profiles:
      bedrock:
        auth_origins:
          proxy:
            type: http_header_token
            scope: agent_runtime
            request:
              method: HEAD
              url: https://proxy.example.com
            extract:
              header: x-bedrock-token
        runtime_auth_resolvers:
          helper:
            type: command
            command: /usr/local/bin/fetch
            order: [proxy]
        default_binding:
          type: claude_api_key_helper
          runtime_auth_resolver: helper

agents:
  agent_runtimes:
    future_host:
      adapter: future
      auth_profile: bedrock
      auth_binding:
        type: bearer_env
        auth_origin: proxy
        env_name: FUTURE_API_TOKEN
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	hc := cfg.EffectiveRuntimes()["future_host"]
	if hc.AuthBinding == nil {
		t.Fatal("AuthBinding is nil")
	}
	if hc.AuthBinding.Type != "bearer_env" {
		t.Errorf("AuthBinding.Type = %q", hc.AuthBinding.Type)
	}
	if hc.AuthBinding.AuthOrigin != "proxy" {
		t.Errorf("AuthBinding.AuthOrigin = %q", hc.AuthBinding.AuthOrigin)
	}
	if hc.AuthBinding.EnvName != "FUTURE_API_TOKEN" {
		t.Errorf("AuthBinding.EnvName = %q", hc.AuthBinding.EnvName)
	}
}

func TestParseCredentialProfile_SecretEnvSource(t *testing.T) {
	input := `
auth:
  credentials:
    profiles:
      openai:
        auth_origins:
          api_key:
            type: secret_env
            scope: any
            env_name: OPENAI_API_KEY
            secret_ref:
              name: openai-api
              key: api-key
        runtime_auth_resolvers: {}
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	p := cfg.Auth.Credentials.Profiles["openai"]
	src := p.AuthOrigins["api_key"]
	if src.Type != "secret_env" {
		t.Errorf("Type = %q", src.Type)
	}
	if src.EnvName != "OPENAI_API_KEY" {
		t.Errorf("EnvName = %q", src.EnvName)
	}
	if src.SecretRef == nil {
		t.Fatal("SecretRef is nil")
	}
	if src.SecretRef.Name != "openai-api" {
		t.Errorf("SecretRef.Name = %q", src.SecretRef.Name)
	}
}

func TestValidateCredentialProfile_RejectsInvalid(t *testing.T) {
	tests := []struct {
		name  string
		yaml  string
		match string
	}{
		{
			name: "empty source type",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins:
          s1:
            type: ""
            scope: agent_runtime
        runtime_auth_resolvers: {}
`,
			match: "type must not be empty",
		},
		{
			name: "unknown source type",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins:
          s1:
            type: magic
            scope: agent_runtime
        runtime_auth_resolvers: {}
`,
			match: "unknown source type",
		},
		{
			name: "empty scope",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins:
          s1:
            type: command_output
            scope: ""
            command: /bin/true
        runtime_auth_resolvers: {}
`,
			match: "scope must not be empty",
		},
		{
			name: "http_header_token missing request",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins:
          s1:
            type: http_header_token
            scope: agent_runtime
            extract:
              header: x-token
        runtime_auth_resolvers: {}
`,
			match: "requires request",
		},
		{
			name: "http_header_token missing extract",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins:
          s1:
            type: http_header_token
            scope: agent_runtime
            request:
              method: HEAD
              url: https://proxy.example.com
        runtime_auth_resolvers: {}
`,
			match: "requires extract",
		},
		{
			name: "command_output missing command",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins:
          s1:
            type: command_output
            scope: host_edge
        runtime_auth_resolvers: {}
`,
			match: "requires command",
		},
		{
			name: "secret_env missing env_name",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins:
          s1:
            type: secret_env
            scope: any
            secret_ref:
              name: my-secret
              key: k
        runtime_auth_resolvers: {}
`,
			match: "requires env_name",
		},
		{
			name: "secret_env missing secret_ref",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins:
          s1:
            type: secret_env
            scope: any
            env_name: KEY
        runtime_auth_resolvers: {}
`,
			match: "requires secret_ref",
		},
		{
			name: "resolver references undefined source",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins:
          s1:
            type: command_output
            scope: host_edge
            command: /bin/true
        runtime_auth_resolvers:
          r1:
            type: command
            command: /bin/resolve
            order: [s1, nonexistent]
`,
			match: "auth_origin \"nonexistent\" which is not defined",
		},
		{
			name: "resolver empty command",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins: {}
        runtime_auth_resolvers:
          r1:
            type: command
            command: ""
            order: []
`,
			match: "command must not be empty",
		},
		{
			name: "claude_api_key_helper binding missing resolver",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins: {}
        runtime_auth_resolvers: {}
        default_binding:
          type: claude_api_key_helper
`,
			match: "claude_api_key_helper binding requires runtime_auth_resolver",
		},
		{
			name: "claude_api_key_helper binding references undefined resolver",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins: {}
        runtime_auth_resolvers: {}
        default_binding:
          type: claude_api_key_helper
          runtime_auth_resolver: nonexistent
`,
			match: "resolver \"nonexistent\" which is not defined",
		},
		{
			name: "bearer_env both source and resolver",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins: {}
        runtime_auth_resolvers: {}
        default_binding:
          type: bearer_env
          auth_origin: s1
          runtime_auth_resolver: r1
          env_name: TOKEN
`,
			match: "exactly one of auth_origin or runtime_auth_resolver",
		},
		{
			name: "bearer_env references undefined source",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins: {}
        runtime_auth_resolvers: {}
        default_binding:
          type: bearer_env
          auth_origin: missing
          env_name: TOKEN
`,
			match: "auth_origin \"missing\" which is not defined",
		},
		{
			name: "bearer_env references undefined resolver",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins: {}
        runtime_auth_resolvers: {}
        default_binding:
          type: bearer_env
          runtime_auth_resolver: missing
          env_name: TOKEN
`,
			match: "resolver \"missing\" which is not defined",
		},
		{
			name: "bearer_env missing env_name",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins: {}
        runtime_auth_resolvers: {}
        default_binding:
          type: bearer_env
          auth_origin: s1
`,
			match: "requires env_name",
		},
		{
			name: "token_file missing source",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins: {}
        runtime_auth_resolvers: {}
        default_binding:
          type: token_file
`,
			match: "token_file binding requires auth_origin",
		},
		{
			name: "empty binding type",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins: {}
        runtime_auth_resolvers: {}
        default_binding:
          type: ""
`,
			match: "binding type must not be empty",
		},
		{
			name: "unknown binding type",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins: {}
        runtime_auth_resolvers: {}
        default_binding:
          type: magic
`,
			match: "unknown binding type",
		},
		{
			name: "assertion references undefined source",
			yaml: `
auth:
  credentials:
    profiles:
      bad:
        auth_origins:
          s1:
            type: command_output
            scope: host_edge
            command: /bin/true
        runtime_auth_resolvers: {}
        assertions:
          require_source: [nonexistent]
`,
			match: "auth_origin \"nonexistent\" which is not defined",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseConfig([]byte(tt.yaml))
			if err == nil {
				t.Fatal("expected error")
			}
			if !contains(err.Error(), tt.match) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.match)
			}
		})
	}
}

func TestValidateCredentialProfile_AcceptsBearerEnvPassthrough(t *testing.T) {
	input := `
auth:
  credentials:
    profiles:
      openai:
        env:
          OPENAI_API_KEY: ${OPENAI_API_KEY}
        assertions:
          require_env: [OPENAI_API_KEY]
        default_binding:
          type: bearer_env
          env_name: OPENAI_API_KEY
`

	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	binding := cfg.Auth.Credentials.Profiles["openai"].DefaultBinding
	if binding == nil {
		t.Fatal("DefaultBinding is nil")
	}
	if binding.Type != "bearer_env" {
		t.Errorf("binding.Type = %q, want bearer_env", binding.Type)
	}
	if binding.EnvName != "OPENAI_API_KEY" {
		t.Errorf("binding.EnvName = %q", binding.EnvName)
	}
	if binding.AuthOrigin != "" {
		t.Errorf("binding.AuthOrigin = %q, want empty", binding.AuthOrigin)
	}
	if binding.RuntimeAuthResolver != "" {
		t.Errorf("binding.RuntimeAuthResolver = %q, want empty", binding.RuntimeAuthResolver)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestResolveCredentialProfile_Found(t *testing.T) {
	cfg := &Config{
		Auth: AuthConfig{
			Credentials: CredentialsConfig{
				Profiles: map[string]CredentialProfile{
					"bedrock": {
						AuthOrigins: map[string]CredentialSource{
							"proxy": {Type: "http_header_token", Scope: "agent_runtime"},
						},
						RuntimeAuthResolvers: map[string]CredentialResolver{
							"helper": {Type: "command", Command: "/bin/fetch", Order: []string{"proxy"}},
						},
						DefaultBinding: &CredentialBinding{Type: "claude_api_key_helper", RuntimeAuthResolver: "helper"},
					},
				},
			},
		},
		Agents: AgentsConfig{
			AgentRuntimes: map[string]RuntimeEntry{
				"claude": {
					Adapter:     "claude",
					AuthProfile: "bedrock",
				},
			},
		},
	}

	profile, binding, err := ResolveCredentialProfile(cfg, "claude")
	if err != nil {
		t.Fatalf("ResolveCredentialProfile: %v", err)
	}
	if profile == nil {
		t.Fatal("profile is nil")
	}
	if _, ok := profile.AuthOrigins["proxy"]; !ok {
		t.Error("profile should have proxy source")
	}
	if binding == nil {
		t.Fatal("binding is nil")
	}
	if binding.Type != "claude_api_key_helper" {
		t.Errorf("binding.Type = %q", binding.Type)
	}
}

func TestResolveCredentialProfile_HostOverrideBinding(t *testing.T) {
	cfg := &Config{
		Auth: AuthConfig{
			Credentials: CredentialsConfig{
				Profiles: map[string]CredentialProfile{
					"bedrock": {
						AuthOrigins:          map[string]CredentialSource{"proxy": {Type: "http_header_token", Scope: "agent_runtime"}},
						RuntimeAuthResolvers: map[string]CredentialResolver{},
						DefaultBinding:       &CredentialBinding{Type: "claude_api_key_helper", RuntimeAuthResolver: "helper"},
					},
				},
			},
		},
		Agents: AgentsConfig{
			AgentRuntimes: map[string]RuntimeEntry{
				"future": {
					Adapter:     "future",
					AuthProfile: "bedrock",
					AuthBinding: &CredentialBinding{Type: "bearer_env", AuthOrigin: "proxy", EnvName: "TOKEN"},
				},
			},
		},
	}

	_, binding, err := ResolveCredentialProfile(cfg, "future")
	if err != nil {
		t.Fatalf("ResolveCredentialProfile: %v", err)
	}
	if binding == nil {
		t.Fatal("binding is nil")
	}
	if binding.Type != "bearer_env" {
		t.Errorf("binding.Type = %q, want bearer_env (host override)", binding.Type)
	}
	if binding.EnvName != "TOKEN" {
		t.Errorf("binding.EnvName = %q", binding.EnvName)
	}
}

func TestResolveCredentialProfile_NoProfileConfigured(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			AgentRuntimes: map[string]RuntimeEntry{
				"claude": {Adapter: "claude"},
			},
		},
	}

	profile, binding, err := ResolveCredentialProfile(cfg, "claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile != nil {
		t.Error("expected nil profile")
	}
	if binding != nil {
		t.Error("expected nil binding")
	}
}

func TestResolveCredentialProfile_UndefinedProfile(t *testing.T) {
	cfg := &Config{
		Auth: AuthConfig{Credentials: CredentialsConfig{Profiles: map[string]CredentialProfile{}}},
		Agents: AgentsConfig{
			AgentRuntimes: map[string]RuntimeEntry{
				"claude": {
					Adapter:     "claude",
					AuthProfile: "nonexistent",
				},
			},
		},
	}

	_, _, err := ResolveCredentialProfile(cfg, "claude")
	if err == nil {
		t.Fatal("expected error for undefined profile")
	}
}

func TestResolveCredentialProfile_NoBindingDefined(t *testing.T) {
	cfg := &Config{
		Auth: AuthConfig{
			Credentials: CredentialsConfig{
				Profiles: map[string]CredentialProfile{
					"simple": {
						AuthOrigins:          map[string]CredentialSource{},
						RuntimeAuthResolvers: map[string]CredentialResolver{},
						// no default_binding
					},
				},
			},
		},
		Agents: AgentsConfig{
			AgentRuntimes: map[string]RuntimeEntry{
				"claude": {
					Adapter:     "claude",
					AuthProfile: "simple",
				},
			},
		},
	}

	profile, binding, err := ResolveCredentialProfile(cfg, "claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile == nil {
		t.Fatal("profile should not be nil")
	}
	if binding != nil {
		t.Error("binding should be nil when neither host nor profile defines one")
	}
}

func TestValidate_K8sStagingDirRequired(t *testing.T) {
	// kubernetes profile without staging_dir → error
	input := `
profile: kubernetes
runtime:
  backend: kubernetes
`
	_, err := ParseConfig([]byte(input))
	if err == nil {
		t.Fatal("expected error for kubernetes profile with empty staging_dir")
	}
	if want := "runtime.staging_dir"; !contains(err.Error(), want) {
		t.Errorf("error = %q, want to contain %q", err.Error(), want)
	}
}

func TestValidate_K8sStagingDirSet(t *testing.T) {
	// kubernetes profile with staging_dir → no error
	input := `
profile: kubernetes
runtime:
  backend: kubernetes
  staging_dir: /data/staging
`
	_, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_LocalProfileNoStagingDir(t *testing.T) {
	// local profile without staging_dir → no error (validation only for k8s)
	input := `
profile: local
runtime:
  backend: local
`
	_, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_InferredK8sProfile(t *testing.T) {
	// No explicit profile but runtime.backend = kubernetes → inferred kubernetes profile
	input := `
runtime:
  backend: kubernetes
`
	_, err := ParseConfig([]byte(input))
	if err == nil {
		t.Fatal("expected error for inferred kubernetes profile with empty staging_dir")
	}
}

func TestParseConfigAuthBearer(t *testing.T) {
	input := `
mcp_servers:
  servers:
    notion:
      remote:
        url: https://mcp.notion.so
        auth:
          type: bearer
          token:
            env: NOTION_TOKEN
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	remote, ok := cfg.MCPServers.Servers["notion"].EffectiveRemote()
	if !ok {
		t.Fatal("expected remote config")
	}
	if remote.Auth == nil {
		t.Fatal("expected auth config")
	}
	if remote.Auth.Type != "bearer" {
		t.Errorf("type = %q, want bearer", remote.Auth.Type)
	}
	if remote.Auth.Token.Env != "NOTION_TOKEN" {
		t.Errorf("token.env = %q", remote.Auth.Token.Env)
	}
}

func TestParseConfigAuthHeader(t *testing.T) {
	input := `
mcp_servers:
  servers:
    custom:
      remote:
        url: https://example.com/mcp
        auth:
          type: header
          header_name: X-API-Key
          header_value:
            value: my-key-123
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	remote, _ := cfg.MCPServers.Servers["custom"].EffectiveRemote()
	if remote.Auth.Type != "header" {
		t.Errorf("type = %q, want header", remote.Auth.Type)
	}
	if remote.Auth.HeaderName != "X-API-Key" {
		t.Errorf("header_name = %q", remote.Auth.HeaderName)
	}
	if remote.Auth.HeaderValue.Value != "my-key-123" {
		t.Errorf("header_value = %q", remote.Auth.HeaderValue.Value)
	}
}

func TestParseConfigAuthBasic(t *testing.T) {
	input := `
mcp_servers:
  servers:
    legacy:
      remote:
        url: https://example.com/mcp
        auth:
          type: basic
          username:
            value: admin
          password:
            env: LEGACY_PASSWORD
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	remote, _ := cfg.MCPServers.Servers["legacy"].EffectiveRemote()
	if remote.Auth.Type != "basic" {
		t.Errorf("type = %q, want basic", remote.Auth.Type)
	}
	if remote.Auth.Username.Value != "admin" {
		t.Errorf("username = %q", remote.Auth.Username.Value)
	}
	if remote.Auth.Password.Env != "LEGACY_PASSWORD" {
		t.Errorf("password.env = %q", remote.Auth.Password.Env)
	}
}

func TestParseConfigAuthOAuth(t *testing.T) {
	input := `
mcp_servers:
  servers:
    notion:
      remote:
        url: https://mcp.notion.so
        auth:
          type: oauth
          client_id:
            env: NOTION_CLIENT_ID
          scopes:
            - read
            - write
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	remote, _ := cfg.MCPServers.Servers["notion"].EffectiveRemote()
	if remote.Auth.Type != "oauth" {
		t.Errorf("type = %q, want oauth", remote.Auth.Type)
	}
	if remote.Auth.ClientID.Env != "NOTION_CLIENT_ID" {
		t.Errorf("client_id.env = %q", remote.Auth.ClientID.Env)
	}
	if len(remote.Auth.Scopes) != 2 {
		t.Errorf("scopes len = %d", len(remote.Auth.Scopes))
	}
	if !remote.Auth.EffectivePKCE() {
		t.Error("expected PKCE default true")
	}
}

func TestParseConfigAuthOAuthClientCredentials(t *testing.T) {
	input := `
mcp_servers:
  servers:
    service:
      remote:
        url: https://mcp.internal.io
        auth:
          type: oauth
          grant_type: client_credentials
          client_id:
            value: svc-id
          client_secret:
            env: SVC_SECRET
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	remote, _ := cfg.MCPServers.Servers["service"].EffectiveRemote()
	if remote.Auth.GrantType != "client_credentials" {
		t.Errorf("grant_type = %q", remote.Auth.GrantType)
	}
}

func TestParseConfigAuthRejectsInvalidType(t *testing.T) {
	input := `
mcp_servers:
  servers:
    bad:
      remote:
        url: https://example.com
        auth:
          type: magic
`
	_, err := ParseConfig([]byte(input))
	if err == nil {
		t.Fatal("expected error for unknown auth type")
	}
}

func TestParseConfigAuthRejectsBearerWithoutToken(t *testing.T) {
	input := `
mcp_servers:
  servers:
    bad:
      remote:
        url: https://example.com
        auth:
          type: bearer
`
	_, err := ParseConfig([]byte(input))
	if err == nil {
		t.Fatal("expected error for bearer without token")
	}
}

func TestParseConfigAuthRejectsSecretValueMultipleSources(t *testing.T) {
	input := `
mcp_servers:
  servers:
    bad:
      remote:
        url: https://example.com
        auth:
          type: bearer
          token:
            value: abc
            env: ALSO_SET
`
	_, err := ParseConfig([]byte(input))
	if err == nil {
		t.Fatal("expected error for multiple SecretValue sources")
	}
}

func TestParseConfigAuthRejectsAuthAndAuthorizationHeader(t *testing.T) {
	input := `
mcp_servers:
  servers:
    conflict:
      remote:
        url: https://example.com
        headers:
          Authorization: "Bearer old"
        auth:
          type: bearer
          token:
            value: new
`
	_, err := ParseConfig([]byte(input))
	if err == nil {
		t.Fatal("expected error for auth + Authorization header")
	}
}

func TestParseConfigAuthRejectsClientCredentialsWithoutSecret(t *testing.T) {
	input := `
mcp_servers:
  servers:
    bad:
      remote:
        url: https://example.com
        auth:
          type: oauth
          grant_type: client_credentials
          client_id:
            value: id
`
	_, err := ParseConfig([]byte(input))
	if err == nil {
		t.Fatal("expected error for client_credentials without client_secret")
	}
}

func TestParseConfigTokenStoreConfig(t *testing.T) {
	input := `
token_store:
  driver: file
  password: my-secret
`
	cfg, err := ParseConfig([]byte(input))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.TokenStore.Driver != "file" {
		t.Errorf("driver = %q", cfg.TokenStore.Driver)
	}
	if cfg.TokenStore.Password != "my-secret" {
		t.Errorf("password = %q", cfg.TokenStore.Password)
	}
}

func TestParseConfigAuthRejectsHeaderCollision(t *testing.T) {
	input := `
mcp_servers:
  servers:
    collision:
      remote:
        url: https://example.com
        headers:
          X-API-Key: "old-key"
        auth:
          type: header
          header_name: X-API-Key
          header_value:
            value: new-key
`
	_, err := ParseConfig([]byte(input))
	if err == nil {
		t.Fatal("expected error for header name collision")
	}
}

func TestParseConfigAuthRejectsAccessTokenAndTokenFile(t *testing.T) {
	input := `
mcp_servers:
  servers:
    bad:
      remote:
        url: https://example.com
        auth:
          type: oauth
          access_token:
            value: tok123
          token_file: /run/secrets/token.json
`
	_, err := ParseConfig([]byte(input))
	if err == nil {
		t.Fatal("expected error for access_token + token_file")
	}
}
