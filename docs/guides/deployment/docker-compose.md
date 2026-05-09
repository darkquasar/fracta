---
title: Docker Compose Quickstart
description: Run fracta in a self-contained docker compose stack
---

# Quickstart: Docker Compose Mode

Docker Compose runs the full  fracta stack in containers: control plane, gateway, strategy runner, Postgres, FalkorDB, and optionally MCP backend servers (Elastic, Vendor). Your machine runs only a thin client.

```
Your machine                         Docker Compose stack
┌──────────────────┐                ┌──────────────────────────┐
│ Claude / Codex / │                │ controlplane    (:19090) │
│ OpenCode         │                │ gateway         (:8080)  │
│   └─ fracta serve ─┼──── HTTP ────>│ strategy-runner          │
│                  │                │ postgres        (:5432)  │
│ fracta spawn (CLI) │                │ falkordb       (:16379)  │
│ fracta list  (CLI) │                │ elastic-mcp   (optional) │
└──────────────────┘                │ vendor-mcp    (optional) │
                                    └──────────────────────────┘
```

<hr />

## Prerequisites

- **Go 1.25+** — `go version`
- **Docker** with Compose V2 — `docker compose version`
- **Optional**: `op` CLI (1Password) for MCP backend secrets

You do **not** need runtime CLIs (claude, codex, opencode) installed on your host — they're bundled in the Docker image.

<hr />

## 1. Build

Build the Go binary and Docker image:

```bash
make build          # Go binary → bin/fracta
make docker-build   # Docker image → fracta/agent:latest
```

<hr />

## 2. Start the stack

**Without MCP backend secrets** (core services only):

```bash
make compose-up
```

**With 1Password secrets** (full stack including Elastic and Generic MCP):

```bash
make compose-up-op
```

Verify all services are healthy:

```bash
make compose-ps
```

You should see 5-7 services running (falkordb, postgres, controlplane, gateway, strategy-runner, and optionally elastic-mcp, vendor-mcp).

<hr />

## 3. Link your runtime config

Symlink the Docker Compose MCP config to your repo root:

**Claude:**

```bash
ln -sf deployment/docker-compose/runtimes/claude/.mcp.json .mcp.json
```

This config points `fracta serve` at `deployment/docker-compose/client/fracta.yaml`, which sets `control_plane_api.url: http://localhost:19090` — the host-mapped port for the compose controlplane container.

**Codex:**

```bash
mkdir -p .codex
ln -sf ../deployment/docker-compose/runtimes/codex/config.toml .codex/config.toml
```

<hr />

## 4. Credentials setup

### LLM runtime credentials

In Docker Compose mode, LLM credentials are handled **inside the container**. The controlplane config (`deployment/docker-compose/configs/controlplane.yaml`) has the auth profiles baked in:

- **Claude (Bedrock):** The `bedrock` profile uses `fetch-bedrock-token`, a script inside the container image that calls a corporate proxy to get a Bedrock bearer token. The agent container self-authenticates — no host-side LLM credential is needed.
- **OpenCode (Bedrock):** Same corporate proxy mechanism via the `opencode_bedrock` profile.
- **Codex (OpenAI):** Set `OPENAI_API_KEY` as a host env var before `docker compose up`. The compose file interpolates `${OPENAI_API_KEY}` into the controlplane container.

If you're not on corporate network, edit `deployment/docker-compose/configs/controlplane.yaml` and replace the auth profiles with your own credentials.

### MCP server API credentials

These authenticate the Elastic and Generic MCP backend containers to their external APIs. They are injected via environment variables at `docker compose up` time.

**With 1Password:**

```bash
make compose-up-op
# Equivalent to:
# op run --env-file .op-env -- docker compose -f deployment/docker-compose/docker-compose.yml up -d
```

`op run` resolves 1Password references in `.op-env` into real environment variables on the host. Docker Compose inherits them and interpolates `${VAR}` in the YAML to pass values into containers. No secrets touch disk.

**Required variables** (defined in `.op-env`):

| Variable | Used by | Source |
|----------|---------|--------|
| `ELASTIC_URL` | elastic-mcp | Plaintext URL |
| `ELASTIC_API_KEY` | elastic-mcp | 1Password reference |
| `VENDOR_MCP_CONSOLE_BASE_URL` | vendor-mcp | Plaintext URL |
| `VENDOR_MCP_CONSOLE_TOKEN` | vendor-mcp | 1Password reference |

**Without 1Password**, export the variables directly:

```bash
export ELASTIC_URL="https://your-cluster.elastic.co"
export ELASTIC_API_KEY="your-api-key"
export VENDOR_MCP_CONSOLE_BASE_URL="https://your-console.vendor.net"
export VENDOR_MCP_CONSOLE_TOKEN="your-token"
docker compose -f deployment/docker-compose/docker-compose.yml up -d
```

Any secret injector that sets env vars works: `doppler run --`, `vault exec --`, etc.

**Without any secrets**, `make compose-up` starts core services. MCP backends won't connect, but agents will still have graph and strategy tools.

<hr />

## 5. Connect and verify

Restart Claude Code or run `/mcp` to reconnect MCP servers.

The thin client connects to `localhost:19090` (compose maps the controlplane's internal `:9090` to host `:19090` to avoid conflicts with a local daemon on `:9090`).

You should see  fracta tools: `fracta_spawn`, `fracta_list`, `graph_query`, etc. If MCP backends are running, you'll also see tools like `elastic.platform_core_search`, `vendor.list_alerts`, etc.

<hr />

## 6. Spawn your first agent

**From the CLI:**

```bash
bin/fracta spawn \
  --config deployment/docker-compose/client/fracta.yaml \
  --task hello-compose \
  --contract "Say hello and list what MCP tools you can see"
```

**From within Claude Code (via MCP):**

```
fracta_spawn(task="hello-compose", contract="Say hello and list what MCP tools you can see")
```

**Check status and output:**

```bash
bin/fracta list --config deployment/docker-compose/client/fracta.yaml
bin/fracta peek --config deployment/docker-compose/client/fracta.yaml --name hello-compose
```

Or via MCP: `fracta_list()` and `fracta_peek(name="hello-compose")`.

Note: Docker Compose uses `DirectoryWorkspace`, not git worktrees. Agents work in directories under `/workspace/agents/<task>`. Git merge semantics are not available — for git-based workflows, use [local process mode](/guides/deployment/local-process).

<hr />

## 7. View logs

```bash
make compose-logs                    # tail all container logs
docker compose -f deployment/docker-compose/docker-compose.yml logs controlplane  # specific service
docker compose -f deployment/docker-compose/docker-compose.yml logs gateway
```

<hr />

## 8. Stop and clean up

```bash
make compose-down
```

To also remove persistent volumes (Postgres data, FalkorDB data):

```bash
docker compose -f deployment/docker-compose/docker-compose.yml down -v
```

<hr />

## What differs from local process

| | Local Process | Docker Compose |
|---|---|---|
| Control plane | Host daemon (:9090) | Container (:19090 on host) |
| Gateway | Daemon subprocess (:8080) | Container (:8080) |
| State | SQLite | Postgres |
| Queue | In-memory | Postgres |
| Agents | Host subprocesses | Container subprocesses |
| Workspace | Git worktrees | Directories (no git merge) |
| MCP backends | Host subprocesses (podman, uvx) | Containers (HTTP) |
| LLM auth | Host command (`bedrock-auth-helper`) | In-container script (corporate proxy) |
| MCP creds | `op run` wraps `fracta serve` | `op run` wraps `docker compose up` |

The client attachment is identical: both use `RemoteControlPlaneClient` over HTTP.

<hr />

## Next steps

- **Docker Compose file reference**: [deployment/docker-compose/README.md](https://github.com/darkquasar/fracta/blob/main/deployment/docker-compose/README.md)
- **Full architecture reference**: [deployment-modes.md](/guides/deployment/overview) (Section 2)
- **Multi-runtime setup**: [runtime-configuration.md](/guides/authentication/runtime-configuration)
- **Ready for Kubernetes?** Try [Kubernetes Quickstart](/guides/deployment/kubernetes)
