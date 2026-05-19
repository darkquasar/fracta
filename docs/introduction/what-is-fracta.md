---
title: What is Fracta?
description: Swarm intelligence orchestration for AI agents — turning open-ended exploration into reproducible, high-performance automation.
---

Swarm intelligence orchestration and strategy engine for AI agents. Fracta turns open-ended agent exploration into high-performance, deterministic automation, turning the unstructured discoveries of your AI workforce into repeatable, high-performance logic.

## How does Fracta achieve this

**Agents handle exploration**: parallel reasoning, open-ended investigation, deciding what to look at next. **Strategies handle automation**: deterministic Python pipelines that encode reproducible analytics on top of what the swarm has discovered. The graph in the middle is more than a shared world model, it carries **ontologies**: nodes and edges that declare which entity types exist, which relationships are valid, and which dynamics are allowed. Ontologies are seeded upfront and extended at runtime as agents discover new shapes; strategies can rely on those guarantees when they query.

That split is the headline. Reasoning loops are non-deterministic by design: sampling, context, model version all drift. Strategies are how you encode the *deterministic* parts of an investigation (counts, joins, correlations, sliding windows, map-reduce, automation logic, detection rules, composable analytics, etc.) so they run reproducibly, in milliseconds. The mechanism that makes this possible is the **MCP gateway**: a native MCP server that exposes the same tool catalog to *two client modes*. Agents drive it through an LLM, and strategies drive it through deterministic Python. Same tools, same trust boundary, no LLM in the loop on the strategy side.

Four pillars: the **agent swarm** (explore), the **MCP gateway** (the dual-client tool layer that enrols new MCP servers and accepts parallel concurrent connections, unattended), the **knowledge graph** (capture, with ontologies governing what shapes are valid), and the **strategy framework** (automate). The agent runtime is whatever AI CLI you already use: Claude Code, Codex, OpenCode today. The architecture has nothing coding-specific about it. Use it for security investigations, ops triage, data exploration, code refactors, anything where you want many agents reasoning in parallel and deterministic logic running on top of what they find.

## The problem

Working with a single AI agent is linear. You ask, you wait, you review, you ask again. When you have ten related tasks, you do them ten times in sequence, even though most of them don't depend on each other.

The bottleneck isn't the model. It's the orchestration around it.

Four concrete frictions:

- **Context-switching tax.** Every time you swap tasks, the agent loses its working memory. You re-explain the codebase, the conventions, the constraints. With ten tasks, that's ten warm-ups.
- **No visibility into long-running work.** When an agent is mid-investigation or running a long refactor, you either babysit the terminal or come back later and read scrollback. There's no live status, no "what is it doing right now," no way to peek without disrupting.
- **No shared knowledge between runs.** Agent A discovers something useful (a flaky test, a hidden dependency, an undocumented API). Agent B starts cold and has to discover it again.
- **Burning tokens on deterministic work.** Counting rows, joining tables, deduplicating, computing percentiles, correlating events across sources: none of this needs a language model. But when you ask an agent to "find anomalies in this dataset," the LLM ends up doing arithmetic in-context, slowly and expensively, with the answer changing run to run because of sampling.

## How fracta solves it

The headline above is the framing: explore (agents) and automate (strategies) joined by a shared world model in the graph. Here's how each piece is materialized.

### Explore: the agent swarm

Each agent gets its own git worktree, its own state, and its own session against a shared MCP gateway (next section). Agents see each other's intent via `fracta_list`, can read each other's recent output via `fracta_peek`, and hand off work through mailbox messages (`fracta_send` / `fracta_inbox`). The control plane orchestrates spawning, admission, and lifecycle.

This is where the LLM earns its keep: open-ended reasoning, deciding what to look at next, integrating signals across sources.

### Gate: the MCP gateway

The gateway is a **native MCP server** that sits between the swarm and every backend tool. It does four things that earn it its own beat in the architecture:

- **Enrols MCP servers**, on-demand or from a catalog. Each registered backend's tools become callable as `<server>.<tool>` (`elastic.search`, `vendor.list_alerts`, `notion.get_page`, …): namespaced, allow-listable, and discoverable through the graph's tool-discovery ontology.
- **Exposes two client modes** against the same tool catalog. Agents drive the gateway through an LLM, choosing tools and arguments via reasoning. Strategies drive the same gateway through deterministic Python: they declare a binding, the gateway pulls the rows directly into Parquet, the LLM never sees them. Same trust boundary, same auditing, same allow-lists, different driver.
- **Accepts parallel concurrent connections** and runs unattended. Many agents and many strategy runs can hit the gateway simultaneously without coordinating with each other; the gateway pools backend MCP clients and fans out work.
- **Acts as a trust boundary.** Credentials, secret material, allow-lists, and connection config live at the gateway, not at every caller. An agent or strategy that doesn't need a credential never holds it; revoking access is a gateway-level operation, not a fleet-wide rotation.

The agent-bypass property (strategies fetching data without burning tokens) is a *consequence* of the dual-client design, not a separate feature. The gateway doesn't care who's calling; the only difference between an agent's tool call and a strategy's is whether an LLM picked the arguments.

### Capture: the knowledge graph

Everything the agents discover lands in FalkorDB as typed nodes and edges. The graph is the persistent memory: two agents working the same problem see the same world, and an agent run tomorrow inherits everything yesterday's run learned. The graph-update protocol makes this non-optional, not a side effect.

The graph is also where **ontologies live**. An ontology is the schema-level layer of the graph: node labels, edge types, and the rules about which connections are valid. The power of the model is that **many ontologies can co-exist** in the same graph: a research-and-investigation ontology might track sources, claims, and counter-claims; a knowledge-garden ontology might track concepts, references, and the trails between them; a threat-intelligence ontology might track actors, campaigns, and indicators; a customer-success ontology might track accounts, conversations, and signals. Whatever the domain, the shape of valid knowledge is itself data in the graph.

Fracta ships with one default ontology, a **tool-discovery ontology** that lets agents trace any field they retrieve back to the MCP tool that produced it, the server it came from, the data store behind that, and ultimately the domain source the data represents. That alone gives agents a coherent model of *what they can ask and where the answers come from*. Beyond that default, ontologies are seeded by you and **extended at runtime by agents themselves**: as they map the world, they add new nodes and edges that capture shapes the seed didn't know about. Because the ontology is data, not code, agents and strategies share the same view of "what shapes are valid here." Strategies can `MATCH` against typed labels with confidence; agents can write new findings without colliding on schema.

### Automate: the strategy framework

Deterministic Python pipelines run against the graph and staged Parquet tables. The staging itself happens through the **gateway in its strategy client mode**: when a strategy runs, the gateway pulls rows directly from each declared MCP backend into Parquet, and DuckDB picks up from there. A strategy that joins two tables and correlates events runs in milliseconds, returns the same answer byte-for-byte every time, and burns zero LLM tokens. Strategies are versioned, composable, and portable across environments: you publish `contract.yaml` + `strategy.py`, and your environment supplies the `binding.yaml` that maps the contract's abstract tables to concrete data sources.

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
- **Docker Compose / Kubernetes**: a per-agent directory under `/workspace/agents/<task>`. No git branches, no merge; agents commit and push (or hand off via files) to integrate.

In every mode you can run ten agents on the same project without them stepping on each other. Git-aware merge semantics only light up in local-process mode. See [Architecture](/introduction/architecture#agent-workspaces) for the full table.

## Why Fracta? The Problem We Are Solving

AI agents excel at open-ended exploration, but they struggle with efficiency and reproducibility once a task becomes routine. Fracta was built to solve the friction of transitioning from "agentic discovery" to "production-grade automation."

### The Limits of Current Approaches

- **Token Waste & Drift**: Once an agent discovers a "happy path" (a successful sequence of actions), repeating that path using traditional prompt engineering or standard harnesses (like agents.md, claude.md, or custom system instructions) is incredibly inefficient. It still relies on non-deterministic LLM interpretation, risking output drift and consuming copious amounts of tokens just to do the exact same job twice.
- **The Complexity Ceiling of Visual Automation**: On the other end of the spectrum, you can encode deterministic paths using visual workflow tools (like n8n). However, as these automations reach high levels of complexity, they become tangled, impossible to debug, and exceptionally difficult to version-control or share with other developers.

### The Fracta Solution

Fracta resolves this tension by allowing you to easily encode agentic reasoning into deterministic, reproducible paths that operate at scale.

- **🐍 Strategies as Pure Python DAGs**: We encode deterministic paths into Python strategies. These run reliably without LLM interpretation, saving tokens and guaranteeing reproducible outcomes.
- **🛠️ Unified Tooling**: These deterministic strategies leverage the exact same MCP tools that the AI agents use during their non-deterministic exploration. You do not need to build your tools twice.
- **⚡ High-Performance State Transfer**: To bridge the gap between agents and strategies, Fracta uses high-performance DuckDB and Parquet as staging databases. These act as the ultra-fast communication medium and data handoff point between open-ended exploration and rigid execution.
- **🤝 Agnostic & Shareable Contracts**: Strategies are built around a strict contract, making them portable and shareable. Because infrastructure varies, each user can simply attach their own `binding.yaml` to a strategy. This satisfies the contract requirements based on the specific nuances of their unique environment, without needing to rewrite the core logic.

### Agents as a Programmable Compute Layer

Fracta fundamentally shifts how we view AI agents. It treats them as a programmable compute layer rather than standalone tools.

Fracta does not care which agentic runtimes (Claude, OpenAI, local models) are doing the work in the backend. Instead, it provides the robust infrastructure to support them:

- A shared bus for standardized logging.
- A shared messaging pipeline (inbox, intention, broadcast).
- A shared graph database to continuously encode the state of the world.
- A shared strategy execution engine.

**The Result**: You can deploy hundreds or thousands of containerized agent pods at any given time. They can run concurrently, solve independent missions, communicate securely with each other, and update the global state of the world in real-time.

### In practice

An agent investigates a security event, calls `strategy_run(name="event_correlation", params={...})`, gets back a structured table of related events from the past 24 hours, and reasons about *that*, instead of asking the LLM to manually correlate raw logs. The strategy framework supports hunt, detection, enrichment, correlation, and traversal categories out of the box. See the [Strategies section](/strategies/overview) for authoring details.

## Who is fracta for

- **Security and ops teams** running parallel investigations across logs, alerts, infrastructure, and identity systems, and codifying repeatable detections as strategies.
- **Data and analytics teams** mixing open-ended exploration (LLM agents) with reproducible analytics (strategies) over the same captured world model.
- **Developers** running many related changes in parallel (refactors across packages, dependency upgrades, doc updates) backed by deterministic checks as strategies.
- **Anyone** orchestrating long-running agent workflows that span hours or days, where you want the swarm to do the open-ended thinking and the strategies to do the reproducible work.

## What's next

<Card title="Architecture" href="/introduction/architecture">
  Read the high-level model: control plane, MCP gateway, agents, worktrees.
</Card>

<Card title="Installation" href="/getting-started/installation">
  Install fracta and run your first agent in five minutes.
</Card>
