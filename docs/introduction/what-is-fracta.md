---
title: What is Fracta?
description: "Swarm intelligence orchestration and strategy building for AI agents — the system materialization of the explore/exploit pattern."
---

# What is Fracta?

Fracta is a **swarm intelligence engine** for AI agents — the system materialization of the explore/exploit pattern. **Agents handle exploration**: parallel reasoning, open-ended investigation, deciding what to look at next. **Strategies handle exploitation**: deterministic Python pipelines that encode reproducible analytics on top of what the swarm has discovered. The graph in the middle is more than a shared world model — it carries **ontologies**: nodes and edges that declare which entity types exist, which relationships are valid, and which dynamics are allowed. Ontologies are seeded upfront and extended at runtime as agents discover new shapes; strategies can rely on those guarantees when they query.

That split is the headline. Reasoning loops are non-deterministic by design — sampling, context, model version all drift. Strategies are how you encode the *deterministic* parts of an investigation (counts, joins, correlations, sliding windows, map-reduce, automation logic, detection rules, composable analytics, etc.) so they run reproducibly, in milliseconds, without the LLM in the loop and without burning tokens on work that's just SQL.

Fracta runs the agent swarm in parallel isolated workspaces, captures what they discover as typed nodes and edges in FalkorDB, and exposes the strategy framework that operates on top. The agent runtime is whatever AI CLI you already use — Claude Code, Codex, OpenCode today; the architecture has nothing coding-specific about it. Use it for security investigations, ops triage, data exploration, code refactors, anything where you want many agents reasoning in parallel and deterministic logic running on top of what they find.

## The problem

Working with a single AI agent is linear. You ask, you wait, you review, you ask again. When you have ten related tasks, you do them ten times in sequence — even though most of them don't depend on each other.

The bottleneck isn't the model. It's the orchestration around it.

Four concrete frictions:

- **Context-switching tax.** Every time you swap tasks, the agent loses its working memory. You re-explain the codebase, the conventions, the constraints. With ten tasks, that's ten warm-ups.
- **No visibility into long-running work.** When an agent is mid-investigation or running a long refactor, you either babysit the terminal or come back later and read scrollback. There's no live status, no "what is it doing right now," no way to peek without disrupting.
- **No shared knowledge between runs.** Agent A discovers something useful — a flaky test, a hidden dependency, an undocumented API. Agent B starts cold and has to discover it again.
- **Burning tokens on deterministic work.** Counting rows, joining tables, deduplicating, computing percentiles, correlating events across sources — none of this needs a language model. But when you ask an agent to "find anomalies in this dataset," the LLM ends up doing arithmetic in-context, slowly and expensively, with the answer changing run to run because of sampling.

## How fracta solves it

The headline above is the framing — explore (agents) and exploit (strategies) joined by a shared world model in the graph. Here's how each piece is materialized.

### Explore — the agent swarm

Each agent gets its own git worktree, its own state, and its own access to a shared MCP gateway that routes tool calls to backend data sources. Agents see each other's intent via `fracta_list`, can read each other's recent output via `fracta_peek`, and hand off work through mailbox messages (`fracta_send` / `fracta_inbox`). The control plane orchestrates spawning, admission, and lifecycle.

This is where the LLM earns its keep: open-ended reasoning, deciding what to look at next, integrating signals across sources.

### Capture — the knowledge graph

Everything the agents discover lands in FalkorDB as typed nodes and edges — systems, identities, IPs, events, findings, hunts, and the data sources behind them. The graph is the persistent memory. Two agents on the same investigation see the same world; an agent run tomorrow inherits everything yesterday's run learned. The graph-update protocol makes this non-optional, not a side effect.

The graph is also where **ontologies live**. An ontology is the schema-level layer of the graph — node labels, edge types, and the rules about which connections are allowed. The `DomainSource → DataStore → MCPServer → MCPTool → MCPField` resolution chain that lets agents trace any field back to its origin is one ontology Fracta ships with. Domain-specific ontologies (e.g. ATT&CK tactics for security, service-dependency models for ops) can be seeded as part of your deployment and **extended at runtime** as agents discover new entity types or relationships. Because the ontology is data, not code, agents and strategies share the same view of "what shapes are valid here." Strategies can `MATCH` against typed labels with confidence; agents can write new findings without colliding on schema.

### Exploit — the strategy framework

Deterministic Python pipelines run against the graph and staged Parquet tables. A strategy that joins two tables and correlates events runs in milliseconds, returns the same answer byte-for-byte every time, and burns zero LLM tokens. Strategies are versioned, composable, and portable across environments — you publish `contract.yaml` + `strategy.py`, and your environment supplies the `binding.yaml` that maps the contract's abstract tables to concrete data sources.

## Core capabilities

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

## Why strategies matter

The exploit side of the platform deserves its own beat. Strategies are reusable Python pipelines that run in a sidecar process — not in the agent's context. An agent invokes a strategy by name, the sidecar pulls the required data into Parquet tables, runs DuckDB queries through a DAG of steps, and returns structured results. The agent gets the answer; the LLM never has to compute it.

This matters because:

- **Cost.** A strategy that joins two tables and counts groups runs in milliseconds for free. The same operation done by an LLM "thinking through" the data in-context takes seconds and burns thousands of tokens — every time.
- **Determinism.** Strategy output is reproducible. Re-run the same strategy with the same inputs and you get the same answer, byte-for-byte. LLM analytics drift run to run.
- **Reuse.** A strategy is a versioned artifact in `strategies/<domain>/<category>/<slug>/` with a contract (input schema), a `strategy.py`, and an optional binding (where to fetch the data). Once written, it's available to every agent in every deployment.
- **Composability.** Strategies can call each other. A correlation strategy can pull enrichment outputs from a hunt strategy without going through the LLM at all.

Typical use: an agent investigates a security event, calls `strategy_run(name="event_correlation", params={...})`, gets back a structured table of related events from the past 24 hours, and reasons about *that* — instead of asking the LLM to manually correlate raw logs.

The strategy framework supports hunt, detection, enrichment, correlation, and traversal categories out of the box. See the [Strategies section](/strategies/overview) for authoring details.

## Who is fracta for

- **Security and ops teams** running parallel investigations across logs, alerts, infrastructure, and identity systems — and codifying repeatable detections as strategies.
- **Data and analytics teams** mixing open-ended exploration (LLM agents) with reproducible analytics (strategies) over the same captured world model.
- **Developers** running many related changes in parallel — refactors across packages, dependency upgrades, doc updates — backed by deterministic checks as strategies.
- **Anyone** orchestrating long-running agent workflows that span hours or days, where you want the swarm to do the open-ended thinking and the strategies to do the reproducible work.

## What's next

<Card title="Architecture" href="/introduction/architecture">
  Read the high-level model: control plane, MCP gateway, agents, worktrees.
</Card>

<Card title="Installation" href="/getting-started/installation">
  Install fracta and run your first agent in five minutes.
</Card>
