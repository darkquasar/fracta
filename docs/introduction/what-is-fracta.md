---
title: What is Fracta?
description: "Swarm intelligence orchestration and strategy building for AI agents — the system materialization of the explore/exploit pattern."
---

# What is Fracta?

Fracta is a **swarm intelligence engine** for AI agents — the system materialization of the explore/exploit pattern. **Agents handle exploration**: parallel reasoning, open-ended investigation, deciding what to look at next. **Strategies handle exploitation**: deterministic Python pipelines that encode reproducible analytics on top of what the swarm has discovered. The graph in the middle is more than a shared world model — it carries **ontologies**: nodes and edges that declare which entity types exist, which relationships are valid, and which dynamics are allowed. Ontologies are seeded upfront and extended at runtime as agents discover new shapes; strategies can rely on those guarantees when they query.

That split is the headline. Reasoning loops are non-deterministic by design — sampling, context, model version all drift. Strategies are how you encode the *deterministic* parts of an investigation (counts, joins, correlations, sliding windows, map-reduce, automation logic, detection rules, composable analytics, etc.) so they run reproducibly, in milliseconds. The mechanism that makes this possible is the **MCP gateway**: a native MCP server that exposes the same tool catalog to *two client modes* — agents driving it through an LLM, and strategies driving it through deterministic Python. Same tools, same trust boundary, no LLM in the loop on the strategy side.

Four pillars: the **agent swarm** (explore), the **MCP gateway** (the dual-client tool layer that enrols new MCP servers and accepts parallel concurrent connections, unattended), the **knowledge graph** (capture, with ontologies governing what shapes are valid), and the **strategy framework** (exploit). The agent runtime is whatever AI CLI you already use — Claude Code, Codex, OpenCode today; the architecture has nothing coding-specific about it. Use it for security investigations, ops triage, data exploration, code refactors, anything where you want many agents reasoning in parallel and deterministic logic running on top of what they find.

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

Each agent gets its own git worktree, its own state, and its own session against a shared MCP gateway (next section). Agents see each other's intent via `fracta_list`, can read each other's recent output via `fracta_peek`, and hand off work through mailbox messages (`fracta_send` / `fracta_inbox`). The control plane orchestrates spawning, admission, and lifecycle.

This is where the LLM earns its keep: open-ended reasoning, deciding what to look at next, integrating signals across sources.

### Gate — the MCP gateway

The gateway is a **native MCP server** that sits between the swarm and every backend tool. It does four things that earn it its own beat in the architecture:

- **Enrols MCP servers**, on-demand or from a catalog. Each registered backend's tools become callable as `<server>.<tool>` (`elastic.search`, `vendor.list_alerts`, `notion.get_page`, …) — namespaced, allow-listable, and discoverable through the graph's tool-discovery ontology.
- **Exposes two client modes** against the same tool catalog. Agents drive the gateway through an LLM, choosing tools and arguments via reasoning. Strategies drive the same gateway through deterministic Python: they declare a binding, the gateway pulls the rows directly into Parquet, the LLM never sees them. Same trust boundary, same auditing, same allow-lists — different driver.
- **Accepts parallel concurrent connections** and runs unattended. Many agents and many strategy runs can hit the gateway simultaneously without coordinating with each other; the gateway pools backend MCP clients and fans out work.
- **Acts as a trust boundary.** Credentials, secret material, allow-lists, and connection config live at the gateway, not at every caller. An agent or strategy that doesn't need a credential never holds it; revoking access is a gateway-level operation, not a fleet-wide rotation.

The agent-bypass property — strategies fetching data without burning tokens — is a *consequence* of the dual-client design, not a separate feature. The gateway doesn't care who's calling; the only difference between an agent's tool call and a strategy's is whether an LLM picked the arguments.

### Capture — the knowledge graph

Everything the agents discover lands in FalkorDB as typed nodes and edges. The graph is the persistent memory: two agents working the same problem see the same world, and an agent run tomorrow inherits everything yesterday's run learned. The graph-update protocol makes this non-optional, not a side effect.

The graph is also where **ontologies live**. An ontology is the schema-level layer of the graph — node labels, edge types, and the rules about which connections are valid. The power of the model is that **many ontologies can co-exist** in the same graph: a research-and-investigation ontology might track sources, claims, and counter-claims; a knowledge-garden ontology might track concepts, references, and the trails between them; a threat-intelligence ontology might track actors, campaigns, and indicators; a customer-success ontology might track accounts, conversations, and signals. Whatever the domain, the shape of valid knowledge is itself data in the graph.

Fracta ships with one default ontology — a **tool-discovery ontology** that lets agents trace any field they retrieve back to the MCP tool that produced it, the server it came from, the data store behind that, and ultimately the domain source the data represents. That alone gives agents a coherent model of *what they can ask and where the answers come from*. Beyond that default, ontologies are seeded by you and **extended at runtime by agents themselves** — as they map the world, they add new nodes and edges that capture shapes the seed didn't know about. Because the ontology is data, not code, agents and strategies share the same view of "what shapes are valid here." Strategies can `MATCH` against typed labels with confidence; agents can write new findings without colliding on schema.

### Exploit — the strategy framework

Deterministic Python pipelines run against the graph and staged Parquet tables. The staging itself happens through the **gateway in its strategy client mode**: when a strategy runs, the gateway pulls rows directly from each declared MCP backend into Parquet, and DuckDB picks up from there. A strategy that joins two tables and correlates events runs in milliseconds, returns the same answer byte-for-byte every time, and burns zero LLM tokens. Strategies are versioned, composable, and portable across environments — you publish `contract.yaml` + `strategy.py`, and your environment supplies the `binding.yaml` that maps the contract's abstract tables to concrete data sources.

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
