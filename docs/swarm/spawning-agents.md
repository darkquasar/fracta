---
title: Spawning Agents
description: How agents are launched, what shapes their behavior, and the lifecycle states they pass through.
---

> First time spawning an agent? Start with the [first agent quickstart](/getting-started/first-agent) — it walks you through one end-to-end. The page below is the reference.

## The basic call

```bash
fracta spawn <task-name> --runtime claude --contract "Hunt for SSO anomalies in the last 24h."
```

This creates an agent named `<task-name>`, picks the `claude` runtime, hands it the contract as its initial prompt, and supervises the resulting subprocess (or pod, depending on deployment mode) until it exits.

## Inputs that shape the agent

| Input | What it does |
|---|---|
| **Task name** | Identifies the agent in `fracta list`, names its workspace (worktree branch or directory), and is how you address it via `fracta say`, `fracta peek`, `fracta merge`, `fracta kill`. |
| **Runtime** | Which AI CLI runs as the agent process — `claude`, `codex`, `opencode`. The runtime adapter handles invocation, session continuation, and output capture. |
| **Contract** | The agent's initial instruction — the *what* and *why* of its work. Stored alongside the agent's state so resumes and `fracta say` follow-ups have the original framing. |
| **Credentials** | The control plane materializes a credential profile (LLM keys, MCP backend secrets) into the agent's environment before launch. See the [credential pipeline guide](/guides/authentication/credential-pipeline). |

## What the control plane does on spawn

1. **Admission check.** Concurrency limits, queue eligibility. If full, the agent enters the queue.
2. **Workspace creation.** Either a git worktree (local-process) or a per-agent directory (Docker Compose / Kubernetes). See [Workspaces](/swarm/workspaces).
3. **Credential materialization.** Profile-driven secrets are written into the agent's environment.
4. **Process launch.** The runtime CLI is started as a subprocess (local-process / Docker Compose) or as a Kubernetes Job (k8s mode).
5. **Wiring.** The agent's MCP client points at the gateway. Its mailbox is initialized. Its intent is set to a default.
6. **State write.** The agent appears in `fracta list` with status `running`.

## Lifecycle states

The orchestrator tracks each agent through a small state machine:

| State | Meaning |
|---|---|
| `queued` | Admission control held it; will start when capacity frees up |
| `running` | Process is alive and the agent is doing work |
| `completed` | Process exited cleanly; final output captured |
| `failed` | Process exited non-zero or crashed |
| `stopped` | Killed via `fracta kill` (or reaped after timeout) |

State transitions are persisted in the state store (SQLite or PostgreSQL). They survive restarts of the control plane.

## Talking to an agent after it's running

| Command | What it does |
|---|---|
| [`fracta list`](/reference/cli/list) | See every agent — status, intent, last update, unread message count |
| [`fracta peek`](/reference/cli/peek) | Read an agent's recent semantic output without disturbing it |
| [`fracta watch`](/reference/cli/watch) | Stream an agent's events live over SSE |
| [`fracta say`](/reference/cli/say) | Send a follow-up; the agent resumes its session with the new message |
| [`fracta merge`](/reference/cli/merge) | (Local-process only) Merge the agent's feature branch into the current branch — agent stays alive |
| [`fracta kill`](/reference/cli/kill) | Tear it down: workspace removed, branch deleted, state cleaned up |

`fracta merge` is non-destructive — the agent keeps running and can iterate further. `fracta kill` is the terminal operation.

## Spawning many at once

The swarm pattern is many agents in parallel. `fracta spawn` is happy to be invoked repeatedly:

```bash
fracta spawn refactor-auth     --runtime claude --contract "Refactor src/auth.go ..."
fracta spawn refactor-tokens   --runtime claude --contract "Refactor src/tokens.go ..."
fracta spawn upgrade-deps      --runtime codex  --contract "Bump deps in go.mod ..."
```

Each gets its own workspace; admission control enforces the configured concurrency limit. Use `fracta list` to watch all of them at once.

## Source

- Spawn flow: [`internal/orchestrator/spawn.go`](https://github.com/darkquasar/fracta/blob/main/internal/orchestrator/spawn.go)
- Lifecycle writer: [`internal/agentlifecycle/`](https://github.com/darkquasar/fracta/blob/main/internal/agentlifecycle)
- Admission and reaping: [`internal/admission/`](https://github.com/darkquasar/fracta/blob/main/internal/admission)
