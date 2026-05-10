---
title: What is Fracta?
description: "A swarm intelligence engine: parallel agents explore, a graph captures what they find, deterministic strategies run on top."
---

# What is Fracta?

Fracta is a **swarm intelligence engine** that builds a model of your world in a graph database, then runs **deterministic strategies** against that model. Parallel AI agents explore the world — logs, alerts, code, infrastructure — and capture what they find as nodes and edges in FalkorDB. Once the world is mapped, strategies — versioned Python pipelines — execute analytics, correlations, and detections directly against the graph and staged data. No LLM in the loop, no sampling drift, no token burn for work that's just SQL and joins.

The swarm uses Claude Code, Codex, or OpenCode as the agent runtime — same CLI you already use. Fracta is the layer that spawns them in parallel git worktrees, routes their MCP tool calls through a shared gateway, persists what they discover, and lets you run reproducible workflows on top of it all.

## The problem

Working with a single AI agent is linear. You ask, you wait, you review, you ask again. When you have ten related tasks, you do them ten times in sequence — even though most of them don't depend on each other.

The bottleneck isn't the model. It's the orchestration around it.

Four concrete frictions:

- **Context-switching tax.** Every time you swap tasks, the agent loses its working memory. You re-explain the codebase, the conventions, the constraints. With ten tasks, that's ten warm-ups.
- **No visibility into long-running work.** When an agent is mid-investigation or running a long refactor, you either babysit the terminal or come back later and read scrollback. There's no live status, no "what is it doing right now," no way to peek without disrupting.
- **No shared knowledge between runs.** Agent A discovers something useful — a flaky test, a hidden dependency, an undocumented API. Agent B starts cold and has to discover it again.
- **Burning tokens on deterministic work.** Counting rows, joining tables, deduplicating, computing percentiles, correlating events across sources — none of this needs a language model. But when you ask an agent to "find anomalies in this dataset," the LLM ends up doing arithmetic in-context, slowly and expensively, with the answer changing run to run because of sampling.

## The three-stage arc

Fracta is built around a deliberate sequence:

1. **Explore (swarm).** Parallel agents run investigations against your data sources — logs, alerts, code, cloud APIs. Each agent works in its own worktree with its own context, coordinating through a mailbox and a shared MCP gateway. This is where the LLM earns its keep: open-ended reasoning, deciding what to look at next, integrating signals across sources.

2. **Capture (graph).** Everything the agents discover lands in FalkorDB as typed nodes and edges — systems, identities, IPs, events, findings, hunts, and the data sources behind them. The graph is the persistent memory. Two agents on the same investigation see the same world; an agent run tomorrow inherits everything yesterday's run learned. The graph-update protocol makes this non-optional, not a side effect.

3. **Execute (strategies).** Once the world is captured, deterministic Python pipelines run against the graph and staged Parquet tables. A strategy that joins two tables and correlates events runs in milliseconds, returns the same answer byte-for-byte every time, and burns zero LLM tokens. Strategies are versioned, composable, and portable across environments — you publish `contract.yaml` + `strategy.py`, your environment supplies the `binding.yaml`.

The sections below map each friction onto the stage that solves it.

## How fracta solves it

Fracta runs multiple agents at once. Each agent gets its own git worktree, its own state, and its own access to a shared MCP gateway that routes tool calls to backend data sources. Agents can message each other through a mailbox; the control plane orchestrates spawning, lifecycle, and concurrency.

The same CLI you already use — `claude`, `codex`, `opencode` — runs as the agent process. Fracta is the layer around it.

### Core capabilities

| Command | What it does |
|---|---|
| [`fracta spawn`](/reference/cli/spawn) | Launch a new agent in its own worktree |
| [`fracta list`](/reference/cli/list) | See every agent: status, intent, last update |
| [`fracta peek`](/reference/cli/peek) | Read an agent's recent semantic output without disrupting it |
| [`fracta watch`](/reference/cli/watch) | Stream live events from an agent over SSE |
| [`fracta say`](/reference/cli/say) | Send a follow-up message; the agent resumes its session |
| [`fracta merge`](/reference/cli/merge) | Merge an agent's branch into the current branch (agent stays alive) |
| [`fracta kill`](/reference/cli/kill) | Remove an agent's worktree and state when you're done |

### Per-agent workspaces

Every agent runs in its own workspace, isolated from every other agent. The shape of that workspace depends on the deployment mode:

- **Local-process**: a real git worktree under `.fracta/worktrees/<task>`, on its own feature branch, sharing the repo's `.git` object store. `fracta merge <task>` brings the agent's commits into the current branch.
- **Docker Compose / Kubernetes**: a per-agent directory under `/workspace/agents/<task>`. No git branches, no merge — agents commit and push (or hand off via files) to integrate.

In every mode you can run ten agents on the same project without them stepping on each other. Git-aware merge semantics only light up in local-process mode. See [Architecture](/introduction/architecture#agent-workspaces) for the full table.

### Strategies: deterministic analytics without burning tokens

Strategies are reusable Python pipelines that run in a sidecar process — not in the agent's context. An agent invokes a strategy by name, the sidecar pulls the required data into Parquet tables, runs DuckDB queries through a DAG of steps, and returns structured results. The agent gets the answer; the LLM never has to compute it.

This matters because:

- **Cost.** A strategy that joins two tables and counts groups runs in milliseconds for free. The same operation done by an LLM "thinking through" the data in-context takes seconds and burns thousands of tokens — every time.
- **Determinism.** Strategy output is reproducible. Re-run the same strategy with the same inputs and you get the same answer, byte-for-byte. LLM analytics drift run to run.
- **Reuse.** A strategy is a versioned artifact in `strategies/<category>/<name>/` with a contract (input schema), a `strategy.py`, and an optional binding (where to fetch the data). Once written, it's available to every agent in every deployment.
- **Composability.** Strategies can call each other. A correlation strategy can pull enrichment outputs from a hunt strategy without going through the LLM at all.

Typical use: an agent investigates a security event, calls `strategy_run(name="event_correlation", params={...})`, gets back a structured table of related events from the past 24 hours, and reasons about *that* — instead of asking the LLM to manually correlate raw logs.

The strategy framework supports hunt, detection, enrichment, correlation, and traversal categories out of the box. See the [Strategies section](/strategies/overview) for authoring details.

## Who is fracta for

- **Developers** running many related changes in parallel (refactors across packages, dependency upgrades, doc updates).
- **Security analysts** running parallel investigations across multiple data sources.
- **Teams** orchestrating long-running agent workflows that span hours or days.

## What's next

<Card title="Architecture" href="/introduction/architecture">
  Read the high-level model: control plane, MCP gateway, agents, worktrees.
</Card>

<Card title="Installation" href="/getting-started/installation">
  Install fracta and run your first agent in five minutes.
</Card>
