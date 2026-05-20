---
title: Control Plane
description: The orchestrator behind the swarm — spawns, supervises, and reaps agents over HTTP and MCP.
---

The control plane is the orchestrator behind the swarm. It listens on port 9090 and exposes both an HTTP API and an MCP server. Every `fracta` CLI command, and every agent that calls a `fracta_*` MCP tool, ultimately goes through it.

## Responsibilities

- **Spawning agents.** Creates a workspace, materializes credential profiles, and launches the runtime CLI as a subprocess (or k8s Job, depending on deployment mode).
- **Admission control.** Enforces concurrency limits and queue eligibility — if you spawn more agents than the configured cap allows, the extras enter a queue.
- **Lifecycle.** Tracks state transitions (`queued` → `running` → `completed` / `failed` / `stopped`).
- **Reaping.** Cleans up dead worktrees and expired sessions on a timer.

## Agents talk to the gateway, not the control plane

A subtle but important split:

- The **`fracta` CLI** (`fracta spawn`, `list`, `say`, `peek`, `merge`, `kill`) talks to the **control plane**. The CLI is a control-plane client only.
- **Agents** (the AI CLIs running in workspaces) talk to the **MCP gateway**. They never connect to the control plane directly.

The first-party `fracta_*` MCP tools (`fracta_spawn`, `fracta_list`, `fracta_inbox`, `fracta_send`, etc.) are exposed by the gateway *to* agents — under the hood, the gateway proxies those calls back to the control plane. From the agent's perspective, it's all just MCP.

This keeps the agents' attack surface minimal (they only see MCP) and the control plane's API surface clean (HTTP + MCP, both designed for orchestration use).

## State and persistence

The control plane writes to a state store — SQLite in local-process mode, PostgreSQL in production deployments. State that survives restarts:

- Every agent's status, intent, output buffer, and resume tokens.
- Mailbox messages.
- Lifecycle history (for the `completed` / `failed` agents that have already finished).

## The full process picture

The control plane is one of four cooperating processes:

```
fracta CLI ──HTTP──▶ Control Plane ──spawns──▶ Agents
                                                  │
                                                  ▼ MCP
                                              MCP Gateway ◀── strategy_run ──▶ Strategy Runner
                                                  │
                                                  ▼
                                          MCP backends + Knowledge Graph
```

See the [Architecture](/introduction/architecture) page for the full diagram and the surrounding pieces (gateway, strategy runner, knowledge graph).

## Source

- CLI entry: [`cmd/controlplane.go`](https://github.com/darkquasar/fracta/blob/main/cmd/controlplane.go)
- Implementation: [`internal/controlplane/`](https://github.com/darkquasar/fracta/blob/main/internal/controlplane)
- HTTP API handlers: [`internal/cpapi/`](https://github.com/darkquasar/fracta/blob/main/internal/cpapi)
- Orchestrator core: [`internal/orchestrator/orchestrator.go`](https://github.com/darkquasar/fracta/blob/main/internal/orchestrator/orchestrator.go)
- Admission and reaping: [`internal/admission/`](https://github.com/darkquasar/fracta/blob/main/internal/admission), [`internal/agentlifecycle/`](https://github.com/darkquasar/fracta/blob/main/internal/agentlifecycle)
