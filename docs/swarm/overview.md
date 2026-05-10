---
title: Swarm Overview
description: Parallel AI agents, isolated workspaces, shared mailbox — the swarm pillar
---

# Swarm Overview

The swarm is the *exploration* stage of Fracta's three-stage arc. Parallel AI agents — each running an AI CLI like Claude Code, Codex, or OpenCode — work in their own isolated workspaces, coordinate through a shared MCP gateway and mailbox, and feed their discoveries into the knowledge graph that the [Strategies](/strategies/overview) layer runs against.

This section covers how agents are spawned, where they live, how they talk to each other, and how the control plane orchestrates them.

## What the swarm gives you

- **True parallelism.** Run ten agents on the same project at once. Each gets its own context, its own working memory, its own subset of the problem.
- **Workspace isolation.** Every agent runs in its own workspace — a real git worktree in local-process mode, a per-agent directory in containerized modes — so agents don't step on each other's files.
- **A shared world.** Agents see each other's intent and recent output through `fracta_list` and `fracta_peek`. They can hand off work via mailbox messages (`fracta_send` / `fracta_inbox`). What they discover lands in FalkorDB, where every other agent — and every strategy — can see it.
- **Lifecycle without babysitting.** The control plane handles spawning, admission control, queueing, and reaping. You issue `fracta spawn`, it picks the right runtime, materializes credentials, and supervises the subprocess (or pod) until it exits.

## The agent runtime is your existing CLI

Fracta does not ship its own LLM client. The agent process is `claude`, `codex`, or `opencode` — whichever CLI you already use. Fracta is the layer that:

1. Spawns it in an isolated workspace.
2. Hands it the credentials it needs.
3. Wires its MCP tool calls to the shared gateway.
4. Tracks its lifecycle and lets you talk to it through `fracta say`, `fracta peek`, and `fracta watch`.

That means: you don't switch tools. The agents you spawn behave like the CLI you already know — they just run in parallel, coordinate, and persist what they find.

## In this section

- [Spawning Agents](/swarm/spawning-agents) — `fracta spawn` in depth: tasks, contracts, runtime selection, lifecycle states
- [Workspaces](/swarm/workspaces) — git worktrees, directory workspaces, and which capabilities light up in which deployment mode
- [Coordination](/swarm/coordination) — the mailbox, intent, peek, and the inbox rhythm
- [Control Plane](/swarm/control-plane) — the orchestrator that supervises everything

## How the swarm feeds the rest of Fracta

Once agents have explored, what they found lives in two places: the knowledge graph (entities, relationships, discovered data sources) and the staged data layer (Parquet files materialized through MCP backends). [Strategies](/strategies/overview) then run deterministic Python pipelines against both — the cheap, reproducible analytics layer that turns exploration into reusable workflows.
