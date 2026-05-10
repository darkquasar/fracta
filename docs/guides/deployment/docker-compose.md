---
title: Docker Compose Quickstart
description: Run fracta in a self-contained docker compose stack
---

# Quickstart: Docker Compose Mode

Docker Compose runs the full fracta stack in containers: control plane, gateway, strategy runner, Postgres, FalkorDB. Your machine runs only a thin client. MCP backend servers (Elastic, Vendor, your own) are added by extending the scaffolded compose file.

```
Your machine                         Docker Compose stack
┌──────────────────┐                ┌──────────────────────────┐
│ Claude / Codex / │                │ controlplane    (:19090) │
│ OpenCode         │                │ gateway         (:8080)  │
│   └─ fracta serve ─┼──── HTTP ────>│ strategy-runner          │
│                  │                │ postgres        (:5432)  │
│ fracta spawn (CLI)│                │ falkordb       (:16379)  │
│ fracta list  (CLI)│                │ + your MCP backends      │
└──────────────────┘                └──────────────────────────┘
```

<hr />

## Prerequisites

- **fracta CLI** installed and on PATH (`fracta --help` works). See [installation](/getting-started/installation).
- **Docker** with Compose V2 — `docker compose version`.
- **A git repository** to scaffold into. `fracta init` runs in your own project root.

You do **not** need runtime CLIs (claude, codex, opencode) installed on your host — they're bundled in the fracta image.

<hr />

## 1. Initialize fracta in your project

From the root of any git repository:

```bash
fracta init --scaffold docker-compose
```

You'll see:

```
Fracta initialized successfully.
  scaffold: docker-compose
  source:   embedded (fracta vX.Y.Z)
  files:    N written, 0 skipped
```

This drops the docker-compose scaffold:

```
your-project/
├── fracta.yaml                              # thin-client config
├── .fracta/                                 # gitignored runtime state (logs)
└── deployment/
    ├── docker-compose.yml                   # full stack: falkordb, postgres, controlplane, gateway, strategy-runner
    ├── configs/
    │   ├── controlplane.yaml                # server-side config inside the controlplane container
    │   └── gateway.yaml                     # gateway config
    └── auth-helpers/
        ├── README.md
        └── fetch-token-example              # 0755 generic helper template; edit before use
```

`fracta.yaml` and everything under `deployment/` are yours to edit.

<hr />

## 2. Set up auth helpers

The scaffolded `deployment/auth-helpers/fetch-token-example` is a deliberately non-functional template that fails loudly until you edit it. Open the file — its header comments include reference snippets for AWS Bedrock STS, Vertex AI via gcloud, mounted Anthropic API keys, and custom HTTP token proxies. Pick the one matching your provider.

For example, for AWS Bedrock STS:

```bash
cat > deployment/auth-helpers/fetch-bedrock-token <<'EOF'
#!/bin/sh
exec aws bedrock get-bearer-token \
  --region "${AWS_REGION:-us-east-1}" \
  --query 'token' --output text
EOF
chmod +x deployment/auth-helpers/fetch-bedrock-token
```

Update `deployment/configs/controlplane.yaml` to reference your helper. The default scaffold ships an `example` profile pointing at `fetch-token-example`; replace it with a `bedrock` profile (or whatever name fits) pointing at your script. See the [credential pipeline guide](/guides/authentication/credential-pipeline) for the full profile schema.

The compose file bind-mounts `./deployment/auth-helpers/` into every fracta service container at `/opt/fracta/auth-helpers/`, so resolver `command:` references find your helpers on PATH inside the container.

<hr />

## 3. Start the stack

```bash
docker compose -f deployment/docker-compose.yml up -d
```

Verify all services are healthy:

```bash
docker compose -f deployment/docker-compose.yml ps
```

You should see five services running: falkordb, postgres, controlplane, gateway, strategy-runner.

### Secret injection

Compose interpolates `${VAR}` from your environment, so any secret manager that sets env vars works. For 1Password:

```bash
op run --env-file .op-env -- docker compose -f deployment/docker-compose.yml up -d
```

For Doppler:

```bash
doppler run -- docker compose -f deployment/docker-compose.yml up -d
```

Use this for any host-side secrets the compose stack needs (database passwords, API keys for MCP backends you add, etc.).

<hr />

## 4. Wire fracta into your AI CLI

The scaffolded `fracta.yaml` points at `http://localhost:19090` — the host-mapped port for the compose controlplane container. Your AI CLI runs `fracta serve` from your project root, which reads `./fracta.yaml`.

**Claude Code** (`.mcp.json` at the project root):

```json
{
  "mcpServers": {
    "fracta": {
      "command": "fracta",
      "args": ["serve"]
    }
  }
}
```

**Codex** (`.codex/config.toml`):

```toml
[mcp_servers.fracta]
command = "fracta"
args = ["serve"]
```

If you need to inject secrets into the host-side `fracta serve`, wrap it the same way you wrap `docker compose up`.

<hr />

## 5. Connect and verify

Restart Claude Code or run `/mcp` to reconnect MCP servers. The thin client connects to `localhost:19090`.

You should see fracta tools: `fracta_spawn`, `fracta_list`, `graph_query`, etc. If you've added MCP backend services to `deployment/docker-compose.yml`, you'll also see their tools.

<hr />

## 6. Spawn your first agent

**From the CLI:**

```bash
fracta spawn \
  --task hello-compose \
  --contract "Say hello and list what MCP tools you can see"
```

**From within Claude Code (via MCP):**

```
fracta_spawn(task="hello-compose", contract="Say hello and list what MCP tools you can see")
```

**Check status and output:**

```bash
fracta list
fracta peek --name hello-compose
```

Or via MCP: `fracta_list()` and `fracta_peek(name="hello-compose")`.

Note: Docker Compose uses `DirectoryWorkspace`, not git worktrees. Agents work in directories under `/workspace/agents/<task>`. Git merge semantics are not available — for git-based workflows, use [local process mode](/guides/deployment/local-process).

<hr />

## 7. View logs

```bash
docker compose -f deployment/docker-compose.yml logs                   # tail all
docker compose -f deployment/docker-compose.yml logs controlplane      # specific service
docker compose -f deployment/docker-compose.yml logs gateway
```

<hr />

## 8. Stop and clean up

```bash
docker compose -f deployment/docker-compose.yml down
```

To also remove persistent volumes (Postgres data, FalkorDB data):

```bash
docker compose -f deployment/docker-compose.yml down -v
```

<hr />

## Adding MCP backend services

The scaffolded `deployment/docker-compose.yml` ships only the fracta core. Add MCP backend services by editing the compose file. For example, to add Elasticsearch MCP:

```yaml
services:
  # ...existing services...

  elastic-mcp:
    image: docker.elastic.co/mcp/elasticsearch:latest
    command: ["http", "--address", "0.0.0.0:8000", "--sse"]
    environment:
      ES_URL: "${ELASTIC_URL}"
      ES_API_KEY: "${ELASTIC_API_KEY}"
```

Then reference it from `deployment/configs/gateway.yaml`:

```yaml
mcp_servers:
  servers:
    elastic:
      remote:
        url: http://elastic-mcp:8000/mcp
        transport: streamable-http
```

Restart the gateway container to pick up the change:

```bash
docker compose -f deployment/docker-compose.yml restart gateway
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
| MCP backends | Host subprocesses | Container HTTP |
| LLM auth | Host command (resolved on host PATH) | Container command (resolved at `/opt/fracta/auth-helpers/`) |
| Secret injection | Wraps `fracta serve` | Wraps `docker compose up` |

The client attachment is identical: both use `RemoteControlPlaneClient` over HTTP.

<hr />

## Next steps

- **Full architecture reference**: [deployment overview](/guides/deployment/overview) (Section 2)
- **Multi-runtime setup**: [runtime configuration](/guides/authentication/runtime-configuration)
- **Auth pipeline deep dive**: [credential pipeline](/guides/authentication/credential-pipeline)
- **Ready for Kubernetes?** Try [Kubernetes Quickstart](/guides/deployment/kubernetes)
