---
title: What is Fracta?
description: Swarm intelligence orchestration for AI coding agents
---

# What is Fracta?

Fracta lets you spawn parallel AI coding agents from a single CLI — Claude, Codex, OpenCode — each working on its own task, in its own git worktree, coordinating through a shared control plane.

## The problem

Working with a single AI agent is linear. You ask, you wait, you review, you ask again. When you have ten related tasks, you do them ten times in sequence — even though most of them don't depend on each other.

The bottleneck isn't the model. It's the orchestration around it.

Four concrete frictions:

- **Context-switching tax.** Every time you swap tasks, the agent loses its working memory. You re-explain the codebase, the conventions, the constraints. With ten tasks, that's ten warm-ups.
- **No visibility into long-running work.** When an agent is mid-investigation or running a long refactor, you either babysit the terminal or come back later and read scrollback. There's no live status, no "what is it doing right now," no way to peek without disrupting.
- **No shared knowledge between runs.** Agent A discovers something useful — a flaky test, a hidden dependency, an undocumented API. Agent B starts cold and has to discover it again.
- **Burning tokens on deterministic work.** Counting rows, joining tables, deduplicating, computing percentiles, correlating events across sources — none of this needs a language model. But when you ask an agent to "find anomalies in this dataset," the LLM ends up doing arithmetic in-context, slowly and expensively, with the answer changing run to run because of sampling.

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

The strategy framework supports hunt, detection, enrichment, correlation, and traversal categories out of the box. See the [Strategies guide](/guides/strategies/overview) for authoring details.

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
