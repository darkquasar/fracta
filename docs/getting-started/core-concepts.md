---
title: Core Concepts
description: The components and ideas that make up fracta
---

# Getting Started with Fracta

Fracta is a multi-agent orchestration system. It lets you spawn parallel AI agents from Claude Code, Codex, or OpenCode — each working on a separate task — and coordinate their output. You keep using your preferred AI CLI as you normally would;  fracta adds the ability to fan work out to multiple agents and bring it back together.

This guide covers the core concepts, how credentials flow, and which deployment mode to pick. Each mode has its own quickstart with step-by-step instructions.

<hr />

## Architecture at a Glance

Every deployment mode shares the same thin-client architecture. Your AI runtime talks to `fracta serve` over stdio; `fracta serve` forwards requests to the control plane over HTTP. The control plane owns agent lifecycle, and the gateway provides MCP tools to agents.

```
Your machine                              Server-side (local daemon, container, or pod)
┌───────────────────────┐                ┌──────────────────────────────────────┐
│                       │                │                                      │
│ Claude / Codex /      │                │  Control Plane (:9090)               │
│ OpenCode              │     stdio      │    Agent lifecycle (spawn/kill)      │
│   └─ fracta serve  ────┼────────┐       │    State store (SQLite or Postgres)  │
│                       │        │ HTTP  │    Mission queue + workers           │
│                       │        ├──────>│    Reaper (auto-cleanup)             │
│ fracta spawn (CLI)  ────┼────────┘       │                                      │
│ fracta list  (CLI)      │                │  Gateway (:8080)                     │
│ fracta say   (CLI)      │                │    MCP tool proxy (Elastic, Vendor)  │
│                       │                │    Knowledge graph (FalkorDB)        │
└───────────────────────┘                │    Strategy engine (Python DAGs)     │
                                         │    Tool discovery                    │
                                         └──────────────────────────────────────┘
                                                         │
                                                         │ spawns
                                                         v
                                         ┌──────────────────────────┐
                                         │ Agent (subprocess or pod) │
                                         │   Claude / Codex / OCode │
                                         │   connects to gateway    │
                                         │   via HTTP MCP           │
                                         └──────────────────────────┘
```

The thin-client boundary is the key insight: whether the control plane runs as a local daemon, a Docker container, or a Kubernetes pod, the client side is identical. Only the infrastructure behind the HTTP API changes.

<hr />

## How Your AI CLI Connects to Fracta

Each runtime reads its MCP server config from a specific file:

| Runtime   | Config file          | Config key         |
|-----------|---------------------|--------------------|
| Claude    | `.mcp.json`         | `mcpServers.fracta`  |
| Codex     | `.codex/config.toml`| `[mcp_servers.fracta]`|
| OpenCode  | `opencode.json`     | `mcp.fracta`         |

All three point to the same command: `bin/fracta serve --config <path>`. The config path determines which deployment mode the thin client connects to.

Each deployment mode ships pre-built runtime configs under:

```
deployment/<mode>/runtimes/<runtime>/
```

To switch modes, symlink the right config to your repo root:

```bash
# Claude — pick one:
ln -sf deployment/local-process/runtimes/claude/.mcp.json .mcp.json
ln -sf deployment/docker-compose/runtimes/claude/.mcp.json .mcp.json
ln -sf deployment/k8s-local-cluster/runtimes/claude/.mcp.json .mcp.json

# Codex — pick one:
mkdir -p .codex
ln -sf ../deployment/local-process/runtimes/codex/config.toml .codex/config.toml
ln -sf ../deployment/docker-compose/runtimes/codex/config.toml .codex/config.toml
```

After symlinking, restart your AI CLI (or `/mcp` in Claude Code) to reconnect.

<hr />

## How Credentials Work

There are two separate credential flows. Confusing them is the most common setup mistake.

### 1. LLM Runtime Credentials

These authenticate **agents** to their LLM provider (Bedrock, OpenAI). They are configured in `fracta.yaml` and resolved at spawn time.

```
fracta.yaml                    At spawn time              Agent process
┌──────────────────┐         ┌─────────────────┐        ┌────────────────────┐
│ agents:          │         │ Credential      │        │ Claude / Codex /   │
│   agent_runtimes:│ refers  │ Planner         │ injects│ OpenCode           │
│     claude:      ├────────>│ resolves tokens ├───────>│ authenticates to   │
│       bedrock    │         │ from profile    │ env +  │ Bedrock / OpenAI   │
│                  │         │                 │ files  │                    │
└──────────────────┘         └─────────────────┘        └────────────────────┘
```

Each runtime authenticates differently:

| Runtime  | LLM Provider | Mechanism | Key config |
|----------|-------------|-----------|------------|
| Claude   | Bedrock     | `auth_profile: bedrock` — a runtime auth resolver runs a command to get a bearer token, injected via `claude_api_key_helper` into `.fracta/user-settings.json` | Required env: `CLAUDE_CODE_USE_BEDROCK`, `AWS_REGION` |
| Codex    | OpenAI      | `OPENAI_API_KEY` env var, from host env or K8s Secret | Set in `agents.agent_runtimes.codex.env` |
| OpenCode | Bedrock     | Bearer token materialized at spawn time, injected as `AWS_BEARER_TOKEN_BEDROCK` env var | `auth_profile: opencode_bedrock` with `bearer_env` binding |

Where the token command runs depends on the deployment mode:
- **Local process**: on your machine (e.g. `bedrock-auth-helper`)
- **Docker Compose / K8s**: inside the container/pod (e.g. `fetch-bedrock-token` script calling a corporate proxy)

For the full credential pipeline reference, see [credential-pipeline.md](/guides/authentication/credential-pipeline).

### 2. MCP Server API Credentials

These authenticate **MCP backend tools** (Elasticsearch, VendorSecurity, etc.) to their external APIs. They are completely separate from LLM credentials.

The injection pattern differs by deployment mode:

```
Mode              How MCP server creds are injected
──────────────    ──────────────────────────────────────────────────────────────

Local process     op run --env-file .op-env -- bin/fracta serve ...
                  1Password resolves op:// refs into env vars
                  fracta.yaml interpolates ${ELASTIC_URL}, ${ELASTIC_API_KEY}
                  Gateway passes env to MCP backend subprocesses

Docker Compose    op run --env-file .op-env -- docker compose up
                  1Password resolves → host env vars
                  docker-compose.yml interpolates ${VAR} into container env
                  MCP backend containers read env vars directly

Kubernetes        make k8s-secrets  (creates K8s Secrets from 1Password)
                  Secrets mounted as env vars in MCP backend pods
```

The key variables:

| Variable | Used by | Purpose |
|----------|---------|---------|
| `ELASTIC_URL` | elastic-mcp | Elasticsearch cluster URL |
| `ELASTIC_API_KEY` | elastic-mcp | Elasticsearch API key |
| `VENDOR_MCP_CONSOLE_BASE_URL` | vendor-mcp | VendorSecurity console URL |
| `VENDOR_MCP_CONSOLE_TOKEN` | vendor-mcp | VendorSecurity API token |

Any secret injector that sets environment variables works: `op run`, `doppler run`, `vault exec`, or plain `export`. The repo defaults to 1Password (`op`) but nothing in  fracta requires it.

Without MCP server credentials, agents still get graph tools, strategy tools, and  fracta lifecycle tools — they just can't query Elasticsearch or VendorSecurity.

<hr />

## Deployment Modes

Fracta runs in three modes. All share the thin-client architecture above.

```
                      Local Process     Docker Compose      Kubernetes
                      ─────────────     ──────────────      ──────────
Complexity            Lowest            Medium              Highest
Setup time            ~10 min           ~15 min             ~20 min

Agents run as         Subprocesses      Container procs     K8s Jobs
State store           SQLite            Postgres            Postgres
Workspace type        Git worktrees     Directories         Directories
Queue                 In-memory         Postgres            Postgres

Prerequisites         Go, Docker*       Go, Docker          Go, Docker, kubectl
                      Runtime CLIs**    Compose V2          Local K8s cluster

Best for              Daily dev         Full-stack test     Prod-like test
                      Quick iteration   Team sharing        K8s Job isolation
```

\* Docker is only needed for FalkorDB in local-process mode.
\*\* Runtime CLIs (claude, codex, opencode) are needed on the host for local-process mode. In Compose/K8s the container image bundles them.

### Which mode should I use?

```
Start here
    │
    ├─ Just want to try  fracta quickly?
    │  └──> Local Process (quickstart-local-process.md)
    │
    ├─ Want the full multi-service stack without K8s?
    │  └──> Docker Compose (quickstart-docker-compose.md)
    │
    └─ Need K8s Job isolation or testing K8s deployment?
       └──> Kubernetes (quickstart-kubernetes.md)
```

For detailed architecture and configuration of each mode, see [deployment-modes.md](/guides/deployment/overview).

<hr />

## Prerequisites

| Prerequisite | Local Process | Docker Compose | Kubernetes |
|---|---|---|---|
| Go 1.25+ | Yes | Yes | Yes |
| Docker | For FalkorDB | Yes (with Compose V2) | Yes |
| kubectl | No | No | Yes |
| Local K8s cluster | No | No | Yes |
| `op` CLI (1Password) | Optional | Optional | Optional |
| Runtime CLI (claude/codex/opencode) | Yes (on host) | No (in container) | No (in container) |

<hr />

## Quickstarts

Follow these in order of complexity:

1. **[Local Process Quickstart](/guides/deployment/local-process)** — Build fracta, start FalkorDB, spawn your first agent. Everything runs on your machine. (~10 min)

2. **[Docker Compose Quickstart](/guides/deployment/docker-compose)** — Build the Docker image, start 7 services, spawn agents through the compose stack. (~15 min)

3. **[Kubernetes Quickstart](/guides/deployment/kubernetes)** — Deploy to a local K8s cluster, spawn agents as K8s Jobs. (~20 min)

<hr />

## Reference Documentation

Once you're up and running, these references cover the full depth:

| Doc | What it covers |
|-----|---------------|
| [Deployment Modes](/guides/deployment/overview) | Architecture, config, and comparison for all three modes |
| [Runtime Configuration](/guides/authentication/runtime-configuration) | Claude, Codex, OpenCode adapter setup and K8s deployment |
| [Credential Pipeline](/guides/authentication/credential-pipeline) | Auth profiles, resolvers, bindings, and the three-layer model |
| [Strategies](/strategies/overview) | Python DAG pipelines for reusable investigation techniques |
| [Contracts & Bindings](/strategies/contracts) | Strategy data requirements and MCP data source mapping |
| [Local K8s Guide](/guides/deployment/kubernetes-runbook) | Complete K8s runbook with troubleshooting |
| [Event Bus](/reference/events) | Internal event architecture |
| [Logging](/reference/configuration/logging) | Structured JSON logging via fractalog |

<hr />

## Glossary

| Term | Definition |
|------|-----------|
| **Control plane** | The server that owns agent lifecycle: spawn, kill, queue, state, reaper |
| **Gateway** | HTTP MCP server that proxies backend tools (Elastic, Vendor) and provides graph/strategy tools to agents |
| **Thin client** | `fracta serve` running on your machine — a lightweight stdio-to-HTTP bridge |
| **MCP** | Model Context Protocol — the standard for connecting AI tools to LLM runtimes |
| **Runtime** | An LLM CLI adapter (Claude, Codex, OpenCode) that  fracta can spawn agents with |
| **Adapter** | The Go implementation that translates  fracta operations into runtime-specific commands |
| **Auth profile** | A named credential configuration under `auth.credentials.profiles` in fracta.yaml |
| **Binding** | How a resolved credential is delivered to the agent (env var, file, settings.json helper) |
| **Strategy** | A reusable Python DAG pipeline that fetches data via MCP, transforms it, and produces findings |
| **Reaper** | Background process that cleans up stale/completed agents |
