---
title: Deployment Modes
description: Architecture for local-process, docker-compose, and kubernetes modes
---

# Deployment Modes

Fracta runs in three deployment modes. All modes share the same thin-client architecture: the CLI and MCP server always connect to the control plane via HTTP. Modes differ in where components run and what infrastructure is needed.

## Mode Summary

| | Local Daemon | Docker Compose | Kubernetes |
|---|---|---|---|
| Config file | `deployment/local-process/fracta.yaml` | `deployment/docker-compose/` | `deployment/k8s-local-cluster/` |
| Agents run as | Local subprocesses (git worktrees) | Container subprocesses (directory workspaces) | K8s Jobs |
| Control plane | Local daemon process (:9090) | Container (:9090) | Pod (:9090) |
| Gateway | Subprocess managed by CP daemon (:8080) | Separate container (:8080) | Separate deployment (:8080) |
| State backend | SQLite | Postgres | Postgres |
| Queue | In-process | Postgres | Postgres |
| Client attachment | `RemoteControlPlaneClient` -> localhost:9090 | `RemoteControlPlaneClient` -> localhost:9090 | `RemoteControlPlaneClient` -> port-forward :9090 |
| MCP backends | Local stdio (podman/uvx) | Container stdio or HTTP | In-cluster HTTP services |

## Thin-Client Architecture

All deployment modes share the same client boundary:

```
fracta serve (stdio MCP server)     fracta spawn / fracta say (CLI commands)
       |                                    |
       v                                    v
  RemoteControlPlaneClient           RemoteControlPlaneClient
       |                                    |
       +------------ HTTP -----------------+
                     |
                     v
            Control Plane API (:9090)
            +-----------------------+
            | ControlPlane          |
            |   Backend             |
            |   Store               |
            |   Queue + Workers     |
            |   Reaper              |
            +-----------------------+
                     |
                     v
            Gateway (:8080)
            +-----------------------+
            | MCP Client Pool       |
            | Strategy Runner       |
            | Tool Discovery        |
            +-----------------------+
```

The CLI and MCP server never construct orchestrators, backends, or state stores. They send HTTP requests to the control plane API. `LocalControlPlaneClient` runs only inside the control plane process.

<hr />

## 1. Local Daemon Mode

The simplest mode. A local daemon process runs the full control plane. Agents are local subprocesses with git-worktree isolation.

### Architecture

```
Host
   fracta controlplane start
    |- ControlPlane (SQLite, LocalBackend, in-process workers)
    |- HTTP API on :9090
    |- Gateway subprocess on :8080
    |    |- Strategy runner (subprocess or external socket)
    |    |- MCP client pool (local stdio backends)
    |    '- Tool discovery
    |- Reaper
    '- FalkorDB (via Docker or local)

  fracta serve                          # thin MCP server -> :9090
  fracta spawn --task my-agent          # thin CLI -> :9090
```

### Runtime Mechanics

Local daemon mode has two different  fracta MCP surfaces:

- `fracta serve` is the host-facing MCP server. Claude/Codex/OpenCode on the developer machine starts it over stdio. It is a thin client that forwards lifecycle calls to the control plane API.
- `fracta serve --gateway-mode` is the agent-facing HTTP MCP gateway. Spawned agents connect to it at `gateway.url`, usually `http://localhost:8080/agents/<agent>/mcp`.

The startup chain is:

```text
Claude/Codex/OpenCode host process
  `- fracta serve --config deployment/local-process/fracta.yaml
       `- starts, then detaches:
             fracta controlplane start --foreground --config deployment/local-process/fracta.yaml
              |- HTTP API on :9090
              |- in-process workers
              |- local agent runtime subprocesses
              `- fracta serve --gateway-mode --transport http --listen :8080 --config deployment/local-process/fracta.yaml
```

After `fracta serve` has confirmed that the control plane is healthy, it no longer waits on the daemon. The long-running ownership tree is:

```text
fracta controlplane start --foreground --config deployment/local-process/fracta.yaml
  |- fracta serve --gateway-mode --transport http --listen :8080 --config deployment/local-process/fracta.yaml
  `- Claude/Codex/OpenCode agent subprocesses
```

Protocol boundaries:

```text
Developer host runtime -> fracta serve                  stdio MCP
fracta serve -> control plane API                       HTTP :9090
control plane -> gateway                              child process
spawned agent runtime -> gateway                      HTTP MCP :8080
gateway -> local backend MCP servers                  stdio subprocesses
```

`gateway.url` **must** be set in the config (e.g. `gateway.url: http://localhost:8080`) for spawned agents to discover gateway-proxied MCP tools. Without it, agents get no MCP tools even if the gateway subprocess is running. The per-agent fallback is `fracta serve --agent-mode --root <worktree>`, which starts a restricted stdio MCP server inside each agent workspace.

### Config

`deployment/local-process/fracta.yaml`:
```yaml
runtime:
  backend: local
  state:
    driver: sqlite
    sqlite:
      path: .fracta/state.db
  queue:
    backend: memory   # required for queued dispatch
    workers: 2

gateway:
  url: http://localhost:8080   # required for agent MCP tool discovery
```

Without `queue.backend: memory`, only direct (non-queued) spawns work.

### Auto-start and config forwarding

`fracta serve --config <path>` auto-starts the daemon if not running. The `--config` flag is forwarded to the daemon process. If no config is found, the daemon starts with bare defaults (no queue, no logging, no connections) and logs a warning.

You can also manage the daemon explicitly:

```bash
bin/fracta controlplane start --config deployment/local-process/fracta.yaml
fracta controlplane stop                        # stop daemon
fracta controlplane status                      # check daemon status
```

### Prerequisites

- FalkorDB running locally (`redis://localhost:6379`). The gateway schema seeder blocks on FalkorDB connectivity — if it is unreachable, gateway startup is delayed until the per-server discovery timeout (60s) expires, after which the gateway starts in degraded mode.
- MCP backend tools available locally (podman for elastic, uvx for vendor)
- Environment variables for secrets (typically via `op run --env-file`)

### When to use

- Day-to-day development
- Debugging agent behavior with local logs
- When git-worktree merge semantics are needed
- When container/K8s infrastructure isn't needed

<hr />

## 2. Docker Compose Mode

Same architecture, containerized. Uses `DirectoryWorkspace` (not git) with the host project bind-mounted at `/workspace`.

### Architecture

```
Host
  fracta serve                          # thin MCP server -> localhost:9090
  fracta spawn --task my-agent          # thin CLI -> localhost:9090

Docker Compose
  controlplane:  fracta serve --control-plane-api-only  (:9090)
  gateway:       fracta serve --gateway-mode             (:8080)
  strategy-runner:  python runner.py (shared /tmp socket with gateway)
  postgres:      state + queue
  falkordb:      knowledge graph
```

### Start

```bash
make compose-up-op                    # start with 1Password secrets injected
bin/fracta serve --config deployment/docker-compose/client/fracta.yaml
```

Or without secrets (core services only, no MCP backends):

```bash
make compose-up
```

### Secrets

MCP backend containers (elastic-mcp, vendor-mcp) require API credentials. These are injected via `op run` from the same `.op-env` file used in local-process mode:

```bash
op run --env-file .op-env -- docker compose -f deployment/docker-compose/docker-compose.yml up -d
```

The `make compose-up-op` target wraps this. `op run` resolves 1Password references into environment variables on the host; Docker Compose interpolates `${VAR}` in the YAML to pass them into containers. No secrets are stored on disk.

Any secret injector that sets environment variables works (Doppler, Vault Agent, etc.):

```bash
doppler run -- docker compose -f deployment/docker-compose/docker-compose.yml up -d
```

### Config

Docker Compose mode lives under `deployment/docker-compose/`:

- `docker-compose.yml` -- compose stack (7 services)
- `configs/controlplane.yaml` -- full lifecycle authority config (Postgres state, local backend, queue workers)
- `configs/gateway.yaml` -- gateway config (FalkorDB connection, strategy dir, MCP server entries)
- `client/fracta.yaml` -- host-side thin-client config

The gateway runs `--strategy-socket-mode external` with a shared socket volume to the `strategy-runner` container, mirroring the K8s sidecar pattern.

### Workspace Semantics

Compose uses `DirectoryWorkspace` (not `GitWorkspace`). The host project directory is bind-mounted into containers at `/workspace`. Agent subprocesses create per-agent subdirectories under `/workspace/agents/<id>`. Merge/integration and base-branch semantics are disabled. For git-based workflows, use local daemon mode.

### When to use

- Testing the full multi-service architecture locally
- When you want Postgres state without K8s
- CI/CD environments that support Docker Compose
- Sharing a reproducible dev environment

<hr />

## 3. Kubernetes Mode

The orchestrator runs as an in-cluster deployment. Agents run as K8s Jobs. The host is a thin client that port-forwards to the control plane.

### Architecture

```
Host
  fracta serve
    '- RemoteControlPlaneClient -> localhost:9090 (port-forward)

K8s Cluster (fracta namespace)
  fracta-controlplane  Deployment   <- orchestrator + workers + reaper + CP API (:9090)
  fracta-gateway       Deployment   <- agent-facing HTTP MCP + strategy sidecar (:8080)
  postgres           StatefulSet  <- shared state
  falkordb           StatefulSet  <- knowledge graph
  elastic-mcp        Deployment   <- Elasticsearch MCP backend
  vendor-mcp         Deployment   <- VendorSecurity MCP backend
  fracta-agent-*       Jobs         <- ephemeral agent pods (batch mode)
  fracta-stream-*      Pods         <- persistent agent pods (stream mode, Codex/OpenCode)
```

### Config

The host needs minimal config:
```yaml
control_plane_api:
  url: http://localhost:9090
```

The in-cluster control plane and gateway configs are in ConfigMaps defined in `deployment/k8s-local-cluster/manifests/fracta-controlplane.yaml` and `deployment/k8s-local-cluster/manifests/fracta-gateway.yaml`.

### Port-forwards

Only the control plane needs a port-forward:
```bash
kubectl port-forward -n  fracta svc/fracta-controlplane 9090:9090
```

On Docker Desktop with `type: LoadBalancer`, the service may be directly accessible at `localhost:9090` without port-forwarding.

### Deploy

```bash
# Deploy all infrastructure
kubectl apply -f deployment/k8s-local-cluster/manifests/namespace.yaml
kubectl apply -f deployment/k8s-local-cluster/manifests/rbac.yaml
kubectl apply -f deployment/k8s-local-cluster/manifests/postgres.yaml
kubectl apply -f deployment/k8s-local-cluster/manifests/falkordb.yaml
kubectl apply -f deployment/k8s-local-cluster/manifests/fracta-controlplane.yaml
kubectl apply -f deployment/k8s-local-cluster/manifests/fracta-gateway.yaml
```

### Agent Permissions

Agent tool permissions are controlled by `project.allowed_tools` in the **controlplane config** (`deployment/k8s-local-cluster/manifests/fracta-controlplane.yaml`), not the host-side thin-client config. This is because the controlplane owns agent lifecycle — it bakes permissions into each pod's `.claude/settings.json` at spawn time.

```yaml
# In the controlplane ConfigMap (deployment/k8s-local-cluster/manifests/fracta-controlplane.yaml)
project:
  default_base_branch: main
  allowed_tools:
    - "Bash(*)"
    - "Read(*)"
    - "Write(*)"
    - "Edit(*)"
    - "Glob(*)"
    - "Grep(*)"
```

For Claude agents, these are merged with a hardcoded `PermissionBaseline` (git, go, ls, cat, find, grep) defined in `internal/host/claude/delivery.go`. Without explicit `allowed_tools`, agents only get the baseline — which excludes general `Bash(*)`, `Read(*)`, `Write(*)`, etc.

For Codex agents, permissions are managed by Codex's own `--full-auto` sandbox policy. For OpenCode agents, permissions are written to `opencode.json` with `"task":"deny"` by default (mitigates subagent overuse). See [runtime-configuration.md](/guides/authentication/runtime-configuration) for details.

Each deployment mode has its own config, so permissions are independent:
- **Local process**: `deployment/local-process/fracta.yaml`
- **Docker Compose**: `deployment/docker-compose/configs/controlplane.yaml`
- **Kubernetes**: `deployment/k8s-local-cluster/manifests/fracta-controlplane.yaml` (ConfigMap)

### Auth

Auth is runtime-specific. Each runtime references a credential profile. See [runtime-configuration.md](/guides/authentication/runtime-configuration) for the full auth guide.

**Claude (Bedrock):** Agent pods self-authenticate via the corporate proxy using the `proxy` auth origin, resolved by `bedrock_helper` → `fetch-bedrock-token`. Required: `AWS_REGION`. Forbidden: `CLAUDE_CODE_SIMPLE`.

**Codex (OpenAI):** API key from a K8s Secret mounted as `OPENAI_API_KEY` env var via `secret_ref`.

**OpenCode (Bedrock):** Bearer token injected as `AWS_BEARER_TOKEN_BEDROCK` via `bearer_env`. In the current implementation this must be a concrete token at spawn time, typically from env passthrough or a host-materialized `command_output` source.

RBAC: controlplane deployment needs `serviceAccountName: fracta-agent` with permissions for configmaps, secrets, jobs, and pods.

### When to use

- Production-like local testing
- When you need K8s Job isolation for agents
- When iterating on agent/gateway behavior without rebuilding orchestrator

<hr />

## Comparison: What Runs Where

| Component | Local Daemon | Docker Compose | Kubernetes |
|---|---|---|---|
| MCP stdio server | Host | Host | Host |
| Control plane | Host (daemon) | Container | Pod |
| Queue workers | Daemon (in-process) | Container (in-process) | Pod (in-process) |
| Reaper | Daemon (in-process) | Container (in-process) | Pod (in-process) |
| Agent execution | Subprocess | Container subprocess | K8s Job |
| Gateway | Daemon subprocess | Container | Pod |
| Strategy runner | Gateway subprocess | Sidecar container (shared socket) | Sidecar container (shared socket) |
| State store | SQLite | Postgres (container) | Postgres (pod) |
| Graph | Host Docker / local | Container | Pod |
| MCP backends | Host stdio | Container stdio | Pod HTTP |
| Workspace type | GitWorkspace | DirectoryWorkspace | DirectoryWorkspace |

<hr />

## Config File Reference

| File | Used by | Purpose |
|------|---------|---------|
| `deployment/local-process/fracta.yaml` | Local daemon | Full local config |
| `deployment/docker-compose/configs/controlplane.yaml` | Compose controlplane | CP config with Postgres DSN |
| `deployment/docker-compose/configs/gateway.yaml` | Compose gateway | Gateway config with FalkorDB |
| `deployment/k8s-local-cluster/manifests/fracta-controlplane.yaml` | K8s CP ConfigMap + Deployment | In-cluster CP config |
| `deployment/k8s-local-cluster/manifests/fracta-gateway.yaml` | K8s Gateway ConfigMap + Deployment | In-cluster gateway config |

<hr />

## Migration Path

The three modes form a progression:

```
Local Daemon  ->  Docker Compose  ->  Kubernetes
  local-process   make compose-up      kubectl apply
```

Each step moves more responsibility into containers/cluster while keeping the same domain model and thin-client boundary. The client attachment is identical across all modes: `RemoteControlPlaneClient` -> HTTP -> control plane API.
