---
title: Local Process Quickstart
description: Run fracta on your machine in under ten minutes
---

# Quickstart: Local Process Mode

Everything runs on your machine: a control plane daemon, a gateway subprocess, agents as local processes, and SQLite for state. This is the simplest way to try fracta.

```
Your machine
┌──────────────────────────────────────────────────┐
│ Claude / Codex / OpenCode                        │
│   └─ fracta serve ──stdio──> :9090 (daemon)       │
│                              ├─ gateway :8080    │
│                              ├─ SQLite state     │
│                              └─ FalkorDB :6379   │
│                                                  │
│ fracta spawn ──HTTP──> :9090 ──> subprocess agent  │
│   agent connects to gateway for MCP tools        │
└──────────────────────────────────────────────────┘
```

<hr />

## Prerequisites

- **Go 1.25+** — `go version`
- **Docker** — for running FalkorDB (the knowledge graph)
- **A runtime CLI** — at least one of:
  - `claude` (Claude Code): `npm install -g @anthropic-ai/claude-code`
  - `codex` (OpenAI Codex): `npm install -g @openai/codex`
  - `opencode` (OpenCode): `npm install -g opencode-ai`
- **Optional**: `op` CLI (1Password) for secret injection

<hr />

## 1. Build fracta

```bash
make build
```

Verify:

```bash
bin/fracta --help
```

You should see the  fracta CLI help with subcommands like `serve`, `spawn`, `list`, etc.

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

## 3. Link your runtime config

Each deployment mode ships pre-built MCP configs. Symlink the local-process config to your repo root:

**Claude:**

```bash
ln -sf deployment/local-process/runtimes/claude/.mcp.json .mcp.json
```

**Codex:**

```bash
mkdir -p .codex
ln -sf ../deployment/local-process/runtimes/codex/config.toml .codex/config.toml
```

### A note about `op` (1Password)

The default local-process `.mcp.json` wraps the  fracta command with `op run --env-file .op-env --` to inject secrets from 1Password. If you don't use 1Password, create a simpler `.mcp.json` at the repo root:

```json
{
  "mcpServers": {
    "fracta": {
      "command": "bin/fracta",
      "args": [
        "serve",
        "--config", "deployment/local-process/fracta.yaml",
        "--graph-addr", "localhost:6379",
        "--strategy-dir", "strategies/"
      ]
    }
  }
}
```

Or set the required env vars directly and use the default symlinked config:

```bash
export ELASTIC_URL="https://..."
export ELASTIC_API_KEY="..."
export FALKORDB_URL="redis://localhost:6379"
```

<hr />

## 4. Credentials setup

### LLM runtime credentials

These authenticate your spawned agents to their LLM provider.

**Claude (Bedrock):** The local-process config uses `bedrock-auth-helper` as the auth resolver command. This is a corporate-internal tool. To use a different Bedrock auth mechanism, edit the `auth.credentials.profiles.bedrock` section in `deployment/local-process/fracta.yaml` and replace the `command` with your own token-fetching command.

**Codex (OpenAI):** Set `OPENAI_API_KEY` as an environment variable before starting fracta:

```bash
export OPENAI_API_KEY="sk-..."
```

The config interpolates `${OPENAI_API_KEY}` from the environment.

**OpenCode (Bedrock):** Uses `bedrock-auth-helper` via `command_output` auth origin. Same as Claude — replace the command if you're not on corporate network.

### MCP server API credentials

These authenticate backend tools (Elasticsearch, VendorSecurity) to their external APIs.

With 1Password (the repo default):

```bash
op run --env-file .op-env -- bin/fracta serve --config deployment/local-process/fracta.yaml
```

Without 1Password, export the variables directly:

```bash
export ELASTIC_URL="https://your-cluster.elastic.co"
export ELASTIC_API_KEY="your-api-key"
# Then start  fracta normally — the config interpolates ${VAR}
```

Without any MCP creds, agents still get graph tools, strategy tools, and  fracta lifecycle tools. They just can't query Elasticsearch or VendorSecurity.

<hr />

## 5. Connect from your AI CLI

Restart Claude Code (or press `/mcp` to reconnect MCP servers).  Fracta auto-starts the control plane daemon when `fracta serve` runs and no daemon is detected.

You should see  fracta tools available — `fracta_spawn`, `fracta_list`, `graph_query`, etc.

If using Codex, restart Codex. Same auto-start behavior applies.

<hr />

## 6. Spawn your first agent

**From the CLI:**

```bash
bin/fracta spawn \
  --config deployment/local-process/fracta.yaml \
  --task hello-world \
  --contract "List the files in the repo root and say hello"
```

**From within Claude Code (via MCP tool):**

```
fracta_spawn(task="hello-world", contract="List the files in the repo root and say hello")
```

**Check status:**

```bash
bin/fracta list --config deployment/local-process/fracta.yaml
```

**Read agent output:**

```bash
bin/fracta peek --config deployment/local-process/fracta.yaml --name hello-world
```

Or via MCP: `fracta_list()` and `fracta_peek(name="hello-world")`.

<hr />

## What just happened

1. The spawn request went to the control plane daemon via HTTP
2. The control plane created a git worktree at `.fracta/worktrees/hello-world` on branch `feature/hello-world`
3. It wrote runtime workspace files (`.mcp.json`, `.claude/settings.json`) into the worktree
4. It launched a Claude subprocess pointed at the worktree
5. The agent connected to the gateway at `:8080` for MCP tools
6. The agent executed the task and completed
7. The reaper will eventually clean up the worktree

<hr />

## Next steps

- **Full local-process reference**: [deployment-modes.md](/guides/deployment/overview) (Section 1)
- **Multi-runtime configuration**: [runtime-configuration.md](/guides/authentication/runtime-configuration)
- **Credential deep dive**: [credential-pipeline.md](/guides/authentication/credential-pipeline)
- **Ready for the full stack?** Try [Docker Compose Quickstart](/guides/deployment/docker-compose)
