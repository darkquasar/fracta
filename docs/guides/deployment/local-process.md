---
title: Quickstart, Local Process Mode
description: Everything runs on your machine — control plane daemon, gateway, agents as local processes, SQLite for state.
---

Everything runs on your machine: a control plane daemon, a gateway subprocess, agents as local processes, and SQLite for state. This is the simplest way to try fracta.

```
Your machine
┌──────────────────────────────────────────────────┐
│ Claude / Codex / OpenCode                        │
│   └─ fracta serve ──stdio──> :9090 (daemon)      │
│                              ├─ gateway :8080    │
│                              ├─ SQLite state     │
│                              └─ FalkorDB :6379   │
│                                                  │
│ fracta spawn ──HTTP──> :9090 ──> subprocess agent│
│   agent connects to gateway for MCP tools        │
└──────────────────────────────────────────────────┘
```

<hr />

## Prerequisites

- **fracta CLI** installed and on PATH (`fracta --help` works). See [installation](/getting-started/installation).
- **Docker** — for running FalkorDB (the knowledge graph).
- **A runtime CLI** — at least one of:
  - `claude` (Claude Code): `npm install -g @anthropic-ai/claude-code`
  - `codex` (OpenAI Codex): `npm install -g @openai/codex`
  - `opencode` (OpenCode): `npm install -g opencode-ai`
- **A git repository** to scaffold into. `fracta init` runs in your own project root, not in the fracta repo.

<hr />

## 1. Initialize fracta in your project

From the root of any git repository:

```bash
fracta init --scaffold local
```

You'll see:

```
Fracta initialized successfully.
  scaffold: local
  source:   embedded (fracta vX.Y.Z)
  files:    2 written, 0 skipped
```

This drops a minimal scaffold:

```
your-project/
├── fracta.yaml           # runtime.backend: local, agent runtimes, allowed_tools
├── .fracta/              # gitignored runtime state
│   ├── state.db          # SQLite
│   └── logs/
└── deployment/
    └── auth-helpers/
        └── README.md     # PATH conventions for agent auth helpers
```

`fracta.yaml` is yours to edit. The defaults work for getting started; later you'll tune `agents.agent_runtimes`, `auth.credentials.profiles`, and `project.allowed_tools`.

<hr />

## 2. Start FalkorDB

The knowledge graph powers graph tools and strategy execution. Start it with Docker:

```bash
docker run -d --name falkordb -p 6379:6379 falkordb/falkordb:v4.16.9
```

Verify:

```bash
redis-cli -p 6379 ping
# PONG
```

Without FalkorDB, the gateway starts in degraded mode after a 60-second timeout. Agents will work, but graph and strategy tools will be unavailable.

<hr />

## 3. Wire fracta into your AI CLI

Each runtime CLI has its own MCP-server config format. The simplest approach is to add a fracta entry that runs `fracta serve` from your project root.

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

`fracta serve` reads `./fracta.yaml` by default. To pass a different config or extra flags, add them to `args`.

**Codex** (`.codex/config.toml`):

```toml
[mcp_servers.fracta]
command = "fracta"
args = ["serve"]
```

**OpenCode** — see the [OpenCode runtime guide](/guides/authentication/runtime-configuration) for `opencode.json` setup.

### Secret injection (optional)

If you use a secret manager, wrap the command. For 1Password:

```json
{
  "mcpServers": {
    "fracta": {
      "command": "op",
      "args": ["run", "--env-file", ".op-env", "--", "fracta", "serve"]
    }
  }
}
```

For Doppler: `["run", "--", "fracta", "serve"]`. For plain env vars, set them in your shell before starting the AI CLI — fracta inherits the environment.

<hr />

## 4. Auth credentials

Agents need credentials to talk to their LLM provider. The default scaffolded `fracta.yaml` ships an `example` auth profile that points at `deployment/auth-helpers/fetch-token-example` — a deliberately non-functional template that fails loudly until you edit it.

Add a real helper script for your provider. For example, for Anthropic API:

```bash
cat > deployment/auth-helpers/fetch-anthropic-key <<'EOF'
#!/bin/sh
exec cat "${ANTHROPIC_API_KEY_FILE:-$HOME/.anthropic/api-key}"
EOF
chmod +x deployment/auth-helpers/fetch-anthropic-key
```

Then update `fracta.yaml` to reference it:

```yaml
auth:
  credentials:
    profiles:
      anthropic:
        runtime_auth_resolvers:
          anthropic_helper:
            type: command
            command: fetch-anthropic-key
            ttl_ms: 60000
        default_binding:
          type: claude_api_key_helper
          runtime_auth_resolver: anthropic_helper

agents:
  agent_runtimes:
    claude:
      auth_profile: anthropic
```

For Bedrock STS, Vertex via gcloud, or custom HTTP token proxies, see the example snippets in `deployment/auth-helpers/fetch-token-example`. The full pipeline is documented in the [credential pipeline guide](/guides/authentication/credential-pipeline).

<hr />

## 5. Connect from your AI CLI

Restart Claude Code (or press `/mcp` to reconnect MCP servers). Fracta auto-starts the control plane daemon when `fracta serve` runs and no daemon is detected.

You should see fracta tools available — `fracta_spawn`, `fracta_list`, `graph_query`, etc.

If using Codex, restart Codex. Same auto-start behavior applies.

<hr />

## 6. Spawn your first agent

**From the CLI:**

```bash
fracta spawn \
  --task hello-world \
  --contract "List the files in the repo root and say hello"
```

**From within Claude Code (via MCP tool):**

```
fracta_spawn(task="hello-world", contract="List the files in the repo root and say hello")
```

**Check status:**

```bash
fracta list
```

**Read agent output:**

```bash
fracta peek --name hello-world
```

Or via MCP: `fracta_list()` and `fracta_peek(name="hello-world")`.

<hr />

## What just happened

1. The spawn request went to the control plane daemon via HTTP.
2. The control plane created a git worktree at `.fracta/worktrees/hello-world` on branch `feature/hello-world`.
3. It wrote runtime workspace files (`.mcp.json`, `.claude/settings.json`) into the worktree.
4. It launched a Claude subprocess pointed at the worktree.
5. The agent connected to the gateway at `:8080` for MCP tools.
6. The agent executed the task and completed.
7. The reaper will eventually clean up the worktree.

<hr />

## Strategy runner gateway plumbing

Local mode runs the gateway and the strategy runner in one process, so the runner can in principle reach the gateway directly. The framework still relies on the per-request `gateway_url` and `agent_task` to be set, which only happens when `strategy.gateway_access: true` is in `fracta.yaml`. Since v0.5.2 the local scaffold ships with that flag enabled by default:

```yaml
strategy:
  gateway_access: true   # required for ctx.mcp.call_tool() from strategies
```

Strategies declaring `requires.mcp: true` (e.g. `highlight-distill`, `notion-publish`) will refuse to start without it.

<hr />

## Next steps

- **Full local-process reference**: [deployment overview](/guides/deployment/overview) (Section 1)
- **Multi-runtime configuration**: [runtime configuration](/guides/authentication/runtime-configuration)
- **Credential deep dive**: [credential pipeline](/guides/authentication/credential-pipeline)
- **Ready for the full stack?** Try [Docker Compose Quickstart](/guides/deployment/docker-compose)
