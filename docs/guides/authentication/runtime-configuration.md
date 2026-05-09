---
title: Runtime Configuration
description: Configuring claude, codex, and opencode runtimes
---

# Runtime Configuration Guide

Fracta supports multiple LLM runtimes: **Claude**, **Codex**, and **OpenCode**. This guide covers how to configure each runtime in `fracta.yaml`, how auth is wired for local and K8s deployments, and how to set up K8s manifests for non-Claude runtimes.

For adding a new runtime from scratch, see [host-onboarding.md](/contributing/adding-runtime).
For deployment mode architecture, see [deployment-modes.md](/guides/deployment/overview).

<hr />

## Runtime Capabilities

| Capability | Claude | Codex | OpenCode |
|---|---|---|---|
| Batch mode | Yes | Yes | Yes |
| Stream mode | Yes | Yes (app-server JSON-RPC) | Yes (serve HTTP+SSE) |
| Agent MCP | Yes | Yes (.codex/config.toml) | Yes (opencode.json) |
| Resume token | Yes (session ID) | Yes (thread ID) | Yes (session ID) |
| Tool permissions | Yes (settings.json) | Yes (--full-auto) | Yes (opencode.json) |
| Structured events | Yes | Yes (JSONL) | Yes (nd-JSON) |
| turn/steer | No | Yes (TurnSteerer) | No |
| K8s batch | Job | Job | Job |
| K8s stream | N/A (local only) | Pod (WebSocket) | Pod (HTTP+SSE) |

<hr />

## fracta.yaml Configuration

### Basic: Local Development

```yaml
agents:
  default_runtime: claude   # or "codex" or "opencode"
  default_mode: batch       # or "stream"
  agent_runtimes:
    claude:
      adapter: claude
      auth_profile: bedrock
      model_tiers:
        heavy: opus
        medium: sonnet
        light: haiku

    codex:
      adapter: codex
      env:
        OPENAI_API_KEY: "${OPENAI_API_KEY}"

    opencode:
      adapter: opencode
      env:
        AWS_BEARER_TOKEN_BEDROCK: "${AWS_BEARER_TOKEN_BEDROCK}"
        AWS_REGION: "ap-southeast-2"
```

Older configs with a top-level `runtimes:` map still load for compatibility, but new configs should use `agents.agent_runtimes`.

### Key Fields

| Field | Purpose |
|---|---|
| `adapter` | Which runtime implementation to use (`claude`, `codex`, `opencode`) |
| `model` | Default model for this runtime (overridden by tier or per-spawn) |
| `model_tiers` | Named shortcuts: `heavy`, `medium`, `light` → specific model IDs |
| `env` | Environment variables injected into local agent processes |
| `auth_profile` | References a profile under `auth.credentials.profiles` |
| `auth_binding` | Per-runtime override of the credential binding (rare) |
| `kubernetes.env` | K8s-specific env vars (from Secrets/ConfigMaps) |
| `kubernetes.image` | Per-runtime container image override |

### Spawning with a Specific Runtime

```bash
# CLI
fracta spawn --task my-agent --runtime codex
fracta spawn --task my-agent --runtime opencode --tier heavy

# MCP tool parameter
{"task": "my-agent", "runtime": "codex"}
```

The `--host-type` flag is deprecated but still accepted.

<hr />

## Authentication

Auth configuration lives in the `auth.credentials.profiles` section. Each runtime references a profile by name via `auth_profile`. The credential system is runtime-agnostic — the same profile structure works for all runtimes. What differs is the **binding type** and the **env vars** each runtime expects. For the canonical reference on binding types, credential source types, profile layout, and which names are schema keywords versus local labels versus external env/Secret names, see [credential-pipeline.md](/guides/authentication/credential-pipeline).

### Claude (Bedrock)

Claude Code authenticates via Bedrock. The credential pipeline:
1. A **runtime auth resolver** (command) runs inside the agent to get a bearer token
2. The token is injected via a **claude_api_key_helper** binding into Claude's `settings.json`
3. Required env vars: `CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_SKIP_BEDROCK_AUTH`, `AWS_REGION`
4. Forbidden: `CLAUDE_CODE_SIMPLE` (disables settings.json loading entirely)

```yaml
auth:
  credentials:
    profiles:
      bedrock:
        runtime_auth_resolvers:
          bedrock_token:
            type: command
            command: "bedrock-auth-helper"   # local: host CLI
            ttl_ms: 60000
        env:
          CLAUDE_CODE_USE_BEDROCK: "1"
          CLAUDE_CODE_SKIP_BEDROCK_AUTH: "1"
          AWS_REGION: "ap-southeast-2"
        assertions:
          require_env: [AWS_REGION]
          forbid_env: [CLAUDE_CODE_SIMPLE]
        default_binding:
          type: claude_api_key_helper
          runtime_auth_resolver: bedrock_token

agents:
  agent_runtimes:
    claude:
      adapter: claude
      auth_profile: bedrock
```

### Codex (OpenAI API Key)

Codex authenticates via `OPENAI_API_KEY`. Two approaches:

**Local — env var injection:**
```yaml
agents:
  agent_runtimes:
    codex:
      adapter: codex
      env:
        OPENAI_API_KEY: "${OPENAI_API_KEY}"
```

**K8s — Secret-backed env:**
```yaml
auth:
  credentials:
    profiles:
      openai_secret:
        auth_origins:
          api_key:
            type: secret_env
            scope: any
            env_name: OPENAI_API_KEY
            secret_ref:
              name: openai-api     # K8s Secret name
              key: api-key         # key within the Secret
        runtime_auth_resolvers: {}
        default_binding:
          type: bearer_env
          auth_origin: api_key
          env_name: OPENAI_API_KEY

agents:
  agent_runtimes:
    codex:
      adapter: codex
      auth_profile: openai_secret
```

The `secret_ref` creates a K8s `envFrom` mount in the agent pod. No command resolver needed — the key is static.

### OpenCode (Bedrock Bearer Token)

OpenCode uses Anthropic models via Bedrock. It reads `AWS_BEARER_TOKEN_BEDROCK` directly (no settings.json indirection like Claude).

**Local-process — credential pipeline with command_output:**
```yaml
auth:
  credentials:
    profiles:
      opencode_bedrock:
        auth_origins:
          bedrock_token:
            type: command_output
            scope: any              # materializes at spawn time in all topologies
            command: ["bedrock-auth-helper"]   # MUST be a YAML array, not a string
        env:
          AWS_REGION: "ap-southeast-2"
        default_binding:
          type: bearer_env
          auth_origin: bedrock_token
          env_name: AWS_BEARER_TOKEN_BEDROCK

agents:
  agent_runtimes:
    opencode:
      adapter: opencode
      model: amazon-bedrock/au.anthropic.claude-sonnet-4-6
      model_tiers:
        heavy: amazon-bedrock/au.anthropic.claude-opus-4-6-v1
        medium: amazon-bedrock/au.anthropic.claude-sonnet-4-6
        light: amazon-bedrock/au.anthropic.claude-haiku-4-5-20251001-v1
      auth_profile: opencode_bedrock
```

**K8s — pre-seeded token from secret:**
```yaml
auth:
  credentials:
    profiles:
      opencode_bedrock:
        auth_origins:
          seeded_token:
            type: secret_env
            scope: any
            env_name: AWS_BEARER_TOKEN_BEDROCK
            secret_ref:
              name: fracta-auth
              key: bearer-token
        env:
          AWS_REGION: "ap-southeast-2"
        default_binding:
          type: bearer_env
          auth_origin: seeded_token
          env_name: AWS_BEARER_TOKEN_BEDROCK

agents:
  agent_runtimes:
    opencode:
      adapter: opencode
      auth_profile: opencode_bedrock
```

Both paths materialize the bearer token before spawn and inject it into `AWS_BEARER_TOKEN_BEDROCK`.

**Model ID note:** For Bedrock in ap-southeast-2, OpenCode model IDs must use the `au.` prefix (e.g. `amazon-bedrock/au.anthropic.claude-sonnet-4-6`). OpenCode auto-generates `apac.` for this region which is invalid.

**Important limitation:** OpenCode does not have a Claude-style runtime helper projection. A runtime-only source such as `http_header_token` with `scope: agent_runtime` does not populate `AWS_BEARER_TOKEN_BEDROCK` by itself today. OpenCode currently needs a concrete token value at spawn time.

**Key difference from Claude auth:** Claude uses `claude_api_key_helper` binding and can re-run the helper on TTL. OpenCode uses `bearer_env` binding and gets a point-in-time token value in `AWS_BEARER_TOKEN_BEDROCK`.

### OpenCode (OpenAI Key — Alternative)

OpenCode also supports OpenAI models. Same pattern as Codex:
```yaml
agents:
  agent_runtimes:
    opencode:
      adapter: opencode
      env:
        OPENAI_API_KEY: "${OPENAI_API_KEY}"
```

<hr />

## K8s Deployment

### Batch Mode (Jobs)

All three runtimes work as K8s Jobs out of the box. The controlplane config needs the runtime entries in the ConfigMap:

```yaml
# In the controlplane ConfigMap (deployment/k8s-local-cluster/manifests/fracta-controlplane.yaml)
agents:
  agent_runtimes:
    claude:
      adapter: claude
      model: global.anthropic.claude-sonnet-4-6
      auth_profile: bedrock
    codex:
      adapter: codex
      auth_profile: openai_secret
    opencode:
      adapter: opencode
      auth_profile: opencode_bedrock

# Agent image must include all three CLIs
runtime:
  backend: kubernetes
  kubernetes:
    image: fracta/agent:latest   # must contain claude, codex, and opencode binaries
```

Each runtime's `WriteWorkspace` automatically creates the correct config files in the agent workspace:
- Claude: `.claude/settings.json` (MCP + permissions)
- Codex: `.codex/config.toml` (MCP gateway endpoint)
- OpenCode: `opencode.json` (MCP + permissions + `task:deny`)

### Stream Mode (Long-lived Pods)

Stream mode uses persistent Pods instead of Jobs. The orchestrator automatically:
1. Launches a Pod running the runtime's serve command
2. Waits for readiness (TCP probe for Codex, HTTP probe for OpenCode)
3. Connects the `StreamSession` over the network

**Codex stream pod:**
- Command: `codex app-server --listen ws://0.0.0.0:8080`
- Transport: WebSocket (JSON-RPC)
- Auth: Capability token generated per-session, injected as env var
- Readiness: TCP socket probe on port 8080
- No liveness HTTP endpoint (app-server has none)

**OpenCode stream pod:**
- Command: `opencode serve --port 4096 --hostname 0.0.0.0`
- Transport: HTTP REST + SSE
- Auth: Password generated per-session, basic auth on all HTTP calls
- Readiness: `GET /global/health:4096` returns 200
- Env: `OPENCODE_SERVER_PASSWORD`, `OPENCODE_DB` (emptyDir path), `OPENCODE_CONFIG_CONTENT` (config as env)

Example K8s pod spec for OpenCode serve (for reference — the orchestrator builds this automatically):
```yaml
containers:
- name: opencode
  image: opencode:latest
  command: ["opencode", "serve", "--port", "4096", "--hostname", "0.0.0.0"]
  ports:
  - containerPort: 4096
  env:
  - name: OPENCODE_CONFIG_CONTENT
    valueFrom:
      configMapKeyRef: {name: opencode-config, key: config.json}
  - name: OPENCODE_DB
    value: /data/opencode.db
  - name: OPENCODE_SERVER_PASSWORD
    valueFrom:
      secretKeyRef: {name: opencode-secrets, key: server-password}
  - name: AWS_BEARER_TOKEN_BEDROCK
    valueFrom:
      secretKeyRef: {name: bedrock-token, key: token}
  - name: AWS_REGION
    value: ap-southeast-2
  livenessProbe:
    httpGet: {path: /global/health, port: 4096}
    initialDelaySeconds: 5
    periodSeconds: 10
  readinessProbe:
    httpGet: {path: /global/health, port: 4096}
    initialDelaySeconds: 3
    periodSeconds: 5
  volumeMounts:
  - name: data
    mountPath: /data
  volumes:
  - name: data
    emptyDir: {}
```

### Per-Runtime Container Images

If runtimes need different base images (common when OpenCode/Codex aren't in the shared agent image):

```yaml
agents:
  agent_runtimes:
    claude:
      adapter: claude
      auth_profile: bedrock
      kubernetes:
        image: fracta/agent-claude:latest

    codex:
      adapter: codex
      auth_profile: openai_secret
      kubernetes:
        image: fracta/agent-codex:latest

    opencode:
      adapter: opencode
      auth_profile: opencode_bedrock
      kubernetes:
        image: fracta/agent-opencode:latest
        env:
          - name: OPENCODE_DB
            value: /data/opencode.db
```

<hr />

## OpenCode Safety: Subagent Monitoring

OpenCode has no equivalent to Claude's `CLAUDE_CODE_MAX_TURNS`. To prevent runaway subagent spawning:

1. **`task:deny` permission** — Written by `WriteWorkspace` into `opencode.json`. Blocks the `task` tool (subagent spawning) by default.
2. **Step count monitoring** — The `ServeSession` counts `step_start` SSE events during each `Send()` call. Warnings are logged at milestones (5, 10, 15, 20). If the count exceeds the threshold (default: 20), the session is aborted via `POST /session/:id/abort`.

The step limit is hardcoded at 20 for now. It can be made config-driven via `RuntimeEntry` in a future iteration.

<hr />

## Auth Decision Tree

```
Which runtime?
  ├── Claude
  │     └── Bedrock proxy? ──Yes──→ auth_profile: bedrock
  │                                  binding: claude_api_key_helper
  │                                  env: CLAUDE_CODE_USE_BEDROCK, AWS_REGION
  │
  ├── Codex
  │     └── OpenAI API key ────────→ auth_profile: openai_secret
  │                                  binding: bearer_env → OPENAI_API_KEY
  │                                  K8s: secret_ref on K8s Secret
  │
  └── OpenCode
        ├── Bedrock? ──Yes─────────→ auth_profile: opencode_bedrock
        │                            binding: bearer_env → AWS_BEARER_TOKEN_BEDROCK
        │                            K8s: env passthrough or host-materialized token
        │
        └── OpenAI? ──Yes─────────→ Same as Codex pattern (OPENAI_API_KEY)
```

<hr />

## Complete Example: Three Runtimes in One Config

```yaml
logging:
  file: .fracta/fracta.log
  level: info

runtime:
  backend: local
  state:
    driver: sqlite
    sqlite:
      path: .fracta/state.db

agents:
  default_runtime: claude
  default_mode: batch
  agent_runtimes:
    claude:
      adapter: claude
      auth_profile: bedrock
      model_tiers:
        heavy: opus
        medium: sonnet
        light: haiku

    codex:
      adapter: codex
      env:
        OPENAI_API_KEY: "${OPENAI_API_KEY}"

    opencode:
      adapter: opencode
      env:
        AWS_BEARER_TOKEN_BEDROCK: "${AWS_BEARER_TOKEN_BEDROCK}"
        AWS_REGION: "ap-southeast-2"

auth:
  credentials:
    profiles:
      bedrock:
        runtime_auth_resolvers:
          bedrock_token:
            type: command
            command: "bedrock-auth-helper"
            ttl_ms: 60000
        env:
          CLAUDE_CODE_USE_BEDROCK: "1"
          CLAUDE_CODE_SKIP_BEDROCK_AUTH: "1"
          AWS_REGION: "ap-southeast-2"
        assertions:
          require_env: [AWS_REGION]
          forbid_env: [CLAUDE_CODE_SIMPLE]
        default_binding:
          type: claude_api_key_helper
          runtime_auth_resolver: bedrock_token

project:
  default_base_branch: main
  allowed_tools:
    - Read
    - Edit
    - Write
    - Glob
    - Grep
    - "Bash(git *)"
    - "Bash(go *)"
```

Then spawn agents with any runtime:
```bash
fracta spawn --task research-agent --runtime claude --tier heavy
fracta spawn --task code-agent --runtime codex
fracta spawn --task review-agent --runtime opencode
```
