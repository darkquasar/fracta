---
title: Deployment Modes
description: Architecture for local-process, docker-compose, and kubernetes modes
---

# Deployment Modes

Fracta runs in three deployment modes. All modes share the same thin-client architecture: the CLI and MCP server always connect to the control plane via HTTP. Modes differ in where components run and what infrastructure is needed.

To start a project in any mode, run `fracta init --scaffold <mode>` from your own git repository. The scaffold drops a complete deployment tree under `deployment/` plus a top-level `fracta.yaml` — see the [`fracta init` reference](../../reference/cli/init.md) and the per-mode guides linked below.

## Mode Summary

| | Local Daemon | Docker Compose | Kubernetes |
|---|---|---|---|
| Scaffold command | `fracta init --scaffold local` | `fracta init --scaffold docker-compose` | `fracta init --scaffold k8s` |
| Operator config | `fracta.yaml` (`runtime.backend: local`) | `fracta.yaml` (thin client) + `deployment/configs/` | `fracta.yaml` (thin client) + `deployment/k8s/manifests/` |
| Agents run as | Local subprocesses (git worktrees) | Container subprocesses (directory workspaces) | K8s Jobs |
| Control plane | Local daemon process (:9090) | Container (:9090) | Pod (:9090) |
| Gateway | Subprocess managed by CP daemon (:8080) | Separate container (:8080) | Separate deployment (:8080) |
| State backend | SQLite | Postgres | Postgres |
| Queue | In-process | Postgres | Postgres |
| Client attachment | `RemoteControlPlaneClient` → localhost:9090 | `RemoteControlPlaneClient` → localhost:19090 | `RemoteControlPlaneClient` → in-cluster Service :9090 (via port-forward / LoadBalancer / Ingress) |
| MCP backends | Host stdio (your own MCP servers) | Add to `deployment/docker-compose.yml` | Add to `deployment/k8s/manifests/` |

> **One mode per project.** `fracta init --scaffold` refuses to re-scaffold an existing project as a different mode (the resulting tree would be incoherent). See [Switching modes](#switching-modes) below.

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

### Scaffold

```sh
fracta init --scaffold local
```

Produces:

```
my-project/
├── fracta.yaml                      # runtime.backend: local
├── .fracta/                         # gitignored runtime state
│   ├── state.db                     # SQLite
│   └── logs/
└── deployment/
    └── auth-helpers/
        └── README.md                # PATH conventions for in-container auth helpers
```

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

Local daemon mode has two different fracta MCP surfaces:

- `fracta serve` is the host-facing MCP server. Claude/Codex/OpenCode on the developer machine starts it over stdio. It is a thin client that forwards lifecycle calls to the control plane API.
- `fracta serve --gateway-mode` is the agent-facing HTTP MCP gateway. Spawned agents connect to it at `gateway.url`, usually `http://localhost:8080/agents/<agent>/mcp`.

The startup chain is:

```text
Claude/Codex/OpenCode host process
  `- fracta serve --config fracta.yaml
       `- starts, then detaches:
             fracta controlplane start --foreground --config fracta.yaml
              |- HTTP API on :9090
              |- in-process workers
              |- local agent runtime subprocesses
              `- fracta serve --gateway-mode --transport http --listen :8080 --config fracta.yaml
```

After `fracta serve` has confirmed that the control plane is healthy, it no longer waits on the daemon. The long-running ownership tree is:

```text
fracta controlplane start --foreground --config fracta.yaml
  |- fracta serve --gateway-mode --transport http --listen :8080 --config fracta.yaml
  `- Claude/Codex/OpenCode agent subprocesses
```

`gateway.url` **must** be set in `fracta.yaml` (e.g. `gateway.url: http://localhost:8080`) for spawned agents to discover gateway-proxied MCP tools.

### Config

The scaffolded `fracta.yaml` works out of the box for local development. The relevant fields:

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

### Auto-start and config forwarding

`fracta serve` auto-starts the daemon if not running. The `--config` flag is forwarded to the daemon process. If no config is found, the daemon starts with bare defaults (no queue, no logging, no connections) and logs a warning.

You can also manage the daemon explicitly:

```bash
fracta controlplane start --config fracta.yaml
fracta controlplane stop
fracta controlplane status
```

### Prerequisites

- FalkorDB running locally (`redis://localhost:6379`).
- Any MCP backend tools you reference in your config available locally (often via `podman` or `uvx`).
- Environment variables for secrets (typically via `op run --env-file`, `doppler run`, or similar).

### When to use

- Day-to-day development.
- Debugging agent behavior with local logs.
- When git-worktree merge semantics are needed.
- When container/K8s infrastructure isn't needed.

See the full [local-process guide](./local-process.md).

<hr />

## 2. Docker Compose Mode

Same architecture, containerized. Uses `DirectoryWorkspace` (not git) with the host project bind-mounted at `/workspace`.

### Scaffold

```sh
fracta init --scaffold docker-compose
```

Produces:

```
my-project/
├── fracta.yaml                              # thin-client config
├── .fracta/                                 # gitignored runtime state (logs only)
└── deployment/
    ├── docker-compose.yml                   # falkordb, postgres, controlplane, gateway, strategy-runner
    ├── configs/
    │   ├── controlplane.yaml                # server-side controlplane config
    │   └── gateway.yaml                     # gateway config
    └── auth-helpers/
        ├── README.md
        └── fetch-token-example              # 0755 generic starter; edit before use
```

### Architecture

```
Host
  fracta serve                          # thin MCP server -> localhost:19090
  fracta spawn --task my-agent          # thin CLI -> localhost:19090

Docker Compose
  controlplane:    fracta serve --control-plane-api-only  (:9090, mapped to host :19090)
  gateway:         fracta serve --gateway-mode             (:8080)
  strategy-runner: python runner.py (shared /tmp socket with gateway)
  postgres:        state + queue
  falkordb:        knowledge graph
```

### Start

```bash
docker compose -f deployment/docker-compose.yml up -d
fracta serve                                 # uses ./fracta.yaml by default
```

### Secrets

Inject API credentials and other secrets into the compose stack with whatever secret manager you already use. Compose interpolates `${VAR}` from the host environment, so any tool that sets env vars works (1Password `op run`, Doppler, Vault Agent, sops, plain `.env` for dev):

```bash
op run --env-file .op-env -- docker compose -f deployment/docker-compose.yml up -d
doppler run -- docker compose -f deployment/docker-compose.yml up -d
```

### Auth helpers

Edit (or replace) `deployment/auth-helpers/fetch-token-example` to fetch a token for your provider. The file's header comments include reference snippets for AWS Bedrock STS, Vertex AI via gcloud, mounted Anthropic API keys, and custom HTTP token proxies.

The compose file bind-mounts `./deployment/auth-helpers/` into every fracta service container at `/opt/fracta/auth-helpers/`, so resolver `command:` references find your helper on PATH.

### Workspace Semantics

Compose uses `DirectoryWorkspace` (not `GitWorkspace`). The host project directory is bind-mounted into containers at `/workspace`. Agent subprocesses create per-agent subdirectories under `/workspace/agents/<id>`. Merge/integration and base-branch semantics are disabled. For git-based workflows, use local daemon mode.

### When to use

- Testing the full multi-service architecture locally.
- When you want Postgres state without K8s.
- CI/CD environments that support Docker Compose.
- Sharing a reproducible dev environment.

See the full [docker-compose guide](./docker-compose.md).

<hr />

## 3. Kubernetes Mode

The orchestrator runs as an in-cluster Deployment. Agents run as K8s Jobs. The control plane is exposed as a Kubernetes Service on `:9090`; the host's thin client talks HTTP to that Service. The **golden path** is to run `fracta serve` on the host as an MCP server in your AI CLI's config — your CLI talks MCP to it, it talks HTTP to the in-cluster control plane Service. The same thin client also works from the command line (`fracta spawn`, `fracta list`, …).

### Scaffold

```sh
fracta init --scaffold k8s
```

Produces:

```
my-project/
├── fracta.yaml                                         # thin-client config + extra_volumes block
├── .fracta/                                            # gitignored runtime state (logs)
└── deployment/
    ├── k8s/
    │   └── manifests/
    │       ├── namespace.yaml
    │       ├── rbac.yaml
    │       ├── pvc.yaml
    │       ├── workspace-pvc.yaml
    │       ├── postgres.yaml
    │       ├── falkordb.yaml
    │       ├── fracta-controlplane.yaml                # ConfigMap + Deployment + Service
    │       ├── fracta-gateway.yaml                     # ConfigMap + Deployment + Service
    │       ├── auth-helpers-configmap.yaml             # empty stub; populated from auth-helpers/
    │       └── agent-job-template.yaml                 # reference, not applied directly
    └── auth-helpers/
        ├── README.md
        └── fetch-token-example                         # 0755 generic starter; edit before use
```

### Architecture

```
Host
  fracta serve  (MCP server in AI CLI config — golden path)
  fracta CLI    (commandline — same thin client)
    '- RemoteControlPlaneClient -> fracta-controlplane Service :9090 in-cluster
       (transport: kubectl port-forward / LoadBalancer / Ingress, depending on cluster)

K8s Cluster (fracta namespace)
  fracta-controlplane  Deployment   <- orchestrator + workers + reaper + CP API (:9090)
  fracta-gateway       Deployment   <- agent-facing HTTP MCP + strategy sidecar (:8080)
  postgres             StatefulSet  <- shared state
  falkordb             StatefulSet  <- knowledge graph
  fracta-agent-*       Jobs         <- ephemeral agent pods (batch mode)
  fracta-stream-*      Pods         <- persistent agent pods (stream mode, Codex/OpenCode)
```

### Config

The scaffolded `fracta.yaml` is the host-side thin-client config:

```yaml
control_plane_api:
  url: http://localhost:9090

runtime:
  backend: kubernetes
  kubernetes:
    namespace: fracta
    image: ghcr.io/darkquasar/fracta:latest
    extra_volumes:
      - name: auth-helpers
        configMap:
          name: fracta-auth-helpers
          defaultMode: 0755
    extra_volume_mounts:
      - name: auth-helpers
        mountPath: /opt/fracta/auth-helpers
        readOnly: true
```

The in-cluster control plane and gateway configs live inside ConfigMaps in `deployment/k8s/manifests/fracta-controlplane.yaml` and `deployment/k8s/manifests/fracta-gateway.yaml`.

### Reaching the control plane Service from the host

The host needs to reach the in-cluster `fracta-controlplane` Service on `:9090`. The transport depends on your cluster:

```bash
# Local dev: port-forward
kubectl port-forward -n fracta svc/fracta-controlplane 9090:9090
```

On Docker Desktop with `type: LoadBalancer`, the Service may already be directly accessible at `localhost:9090`. For real clusters, expose the Service via an Ingress and point `control_plane_api.url` in `fracta.yaml` at that address. None of these change the architecture — they're just transports for the same `host → Service :9090` hop.

### Deploy

```bash
kubectl apply -f deployment/k8s/manifests/namespace.yaml
kubectl apply -f deployment/k8s/manifests/rbac.yaml
kubectl apply -f deployment/k8s/manifests/postgres.yaml
kubectl apply -f deployment/k8s/manifests/falkordb.yaml
kubectl apply -f deployment/k8s/manifests/auth-helpers-configmap.yaml
kubectl apply -f deployment/k8s/manifests/fracta-controlplane.yaml
kubectl apply -f deployment/k8s/manifests/fracta-gateway.yaml
```

Or apply the whole tree:

```bash
kubectl apply -f deployment/k8s/manifests/
```

### Auth helpers

Edit (or replace) `deployment/auth-helpers/fetch-token-example`, then package the directory into the `fracta-auth-helpers` ConfigMap:

```sh
kubectl create configmap fracta-auth-helpers \
  --from-file=deployment/auth-helpers/ \
  -n fracta \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl rollout restart deployment/fracta-controlplane -n fracta
```

The controlplane's `runtime.kubernetes.extra_volumes` block (already in the scaffolded ConfigMap) mounts that ConfigMap into every spawned agent pod at `/opt/fracta/auth-helpers/`, so `command:` references resolve on PATH. See [Kubernetes runtime configuration](../../configuration/kubernetes.md) for the full ConfigMap workflow.

### Agent Permissions

Agent tool permissions are controlled by `project.allowed_tools` in the **controlplane config** (`deployment/k8s/manifests/fracta-controlplane.yaml`), not the host-side thin-client `fracta.yaml`. This is because the controlplane owns agent lifecycle — it bakes permissions into each pod's `.claude/settings.json` at spawn time.

```yaml
# In the controlplane ConfigMap (deployment/k8s/manifests/fracta-controlplane.yaml)
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

For Claude agents, these are merged with a hardcoded `PermissionBaseline` (git, go, ls, cat, find, grep). Without explicit `allowed_tools`, agents only get the baseline.

For Codex agents, permissions are managed by Codex's own `--full-auto` sandbox policy. For OpenCode agents, permissions are written to `opencode.json` with `"task":"deny"` by default. See [runtime configuration](../authentication/runtime-configuration.md) for details.

Permissions are independent per project — each `fracta.yaml` (or controlplane ConfigMap, in k8s mode) declares its own `allowed_tools`.

### Auth

Auth is runtime-specific. Each runtime references a credential profile defined in the controlplane ConfigMap. See [authentication & credentials](../authentication/credential-pipeline.md) for the full guide.

The scaffolded controlplane config ships an `example` profile that points at `fetch-token-example` — replace it with profiles for your real auth provider (Bedrock STS, Vertex, Anthropic API key, etc.).

RBAC: the controlplane Deployment uses `serviceAccountName: fracta-agent` with permissions for configmaps, secrets, jobs, and pods (defined in `deployment/k8s/manifests/rbac.yaml`).

### When to use

- Production-like local testing.
- When you need K8s Job isolation for agents.
- When iterating on agent/gateway behavior without rebuilding the orchestrator.

See the full [Kubernetes guide](./kubernetes.md) and the [Kubernetes runbook](./kubernetes-runbook.md).

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
| MCP backends | Host stdio | Container stdio / HTTP | Pod HTTP |
| Workspace type | GitWorkspace | DirectoryWorkspace | DirectoryWorkspace |

<hr />

## Switching modes

`fracta init --scaffold` is **single-mode per project**. A given project declares one `runtime.backend` in `fracta.yaml` and ships one `deployment/` tree (matching that mode). `fracta init` refuses to re-scaffold a project as a different mode, because the result would be a tree where the manifests, configs, and `fracta.yaml` disagree about what's running.

If you try, you'll see:

```
Error: this project is already scaffolded as local; cannot re-init as k8s
without losing customizations.
  - To switch modes destructively: rm -rf deployment/ fracta.yaml .fracta/
    && fracta init --scaffold k8s
  - To keep your existing local setup: re-run fracta init --scaffold local
```

If you genuinely need multiple modes side by side:

1. **Separate projects** — one repo per mode. `fracta.yaml` and `deployment/` live independently. Organizations often factor out the shared config into a custom scaffold and pull both projects from `--source github:org/my-app-scaffolds@v1`.
2. **Separate worktrees** — same git repo, different branches per mode. Less ceremony than separate repos, but CI must be aware that different branches deploy to different targets.

<hr />

## Migration Path

The three modes form a progression:

```
Local Daemon   ->   Docker Compose   ->   Kubernetes
  fracta init       fracta init             fracta init
  --scaffold local  --scaffold              --scaffold k8s
                    docker-compose
```

Each step moves more responsibility into containers/cluster while keeping the same domain model and thin-client boundary. The client attachment is identical across all modes: `RemoteControlPlaneClient` → HTTP → control plane API.

To migrate, scaffold a fresh project at the new mode (in a separate directory or branch) and copy over your `agents.agent_runtimes`, `auth.credentials.profiles`, and `project.allowed_tools` settings.
