---
title: Portability
description: Why strategies are split into a publishable artifact and an environment-specific binding
---

# Portability

A strategy is two things glued together at runtime:

- **The publishable artifact** — `contract.yaml` + `strategy.py`. These describe *what* the strategy needs and *what* it computes. They are environment-agnostic by design.
- **The environment binding** — `binding.yaml`. This describes *how* to fetch the data the contract asks for, in *your* particular environment.

That split is the whole point. Two security teams can share a hunt strategy by exchanging the first two files; each team writes its own `binding.yaml` to point at its own Elasticsearch cluster, its own VendorSecurity tenant, its own Snowflake warehouse — and the same Python code runs unchanged.

## What lives where

| File | Lives where | Travels with the strategy? |
|---|---|---|
| `contract.yaml` | The strategy directory | **Yes** — publishable |
| `strategy.py` | The strategy directory | **Yes** — publishable |
| `binding.yaml` | Same directory, but environment-specific | **No** — local to your install |

In practice this means: when you publish a strategy to a registry, share it on a gist, or check it into a public repository, you publish only the contract and the Python. When someone else picks it up, they author their own binding to plug it into their stack.

## Why a separate file

The contract declares an abstract requirement:

```yaml
requires:
  tables:
    alerts:
      columns:
        alert_id: { type: VARCHAR }
        severity: { type: INTEGER }
        detected_at: { type: TIMESTAMP }
```

That declaration is portable because it doesn't name a backend, a tool, an index, or a field path. The binding is what fills those in:

```yaml
source_bindings:
  alerts:
    backend: vendor
    mcp_tool: search_alerts
    field_map:
      alert_id: id
      severity: severity
      detected_at: detected_at
```

A different team in a different environment writes a *different* binding — say, pointing at Elasticsearch with a different index pattern and different field names — and the strategy's Python code never has to change.

## Do I always need a binding.yaml?

No. Bindings are optional. Without one, the resolver attempts **automatic resolution** via the knowledge graph using semantic column tags in the contract:

```yaml
columns:
  alert_id:
    type: VARCHAR
    semantic: alert_id   # ← lets auto-resolve match this column to a backend field
```

When every required column has a `semantic` tag and the knowledge graph knows which MCP tool returns that semantic, the resolver picks the source automatically. No binding.yaml needed.

You write an explicit binding when:

- Some columns lack semantic tags (or no tags at all).
- Multiple backends could satisfy the contract and you want to pin one explicitly.
- You need non-default fetch behavior — a custom query template, pagination, a specific MCP tool argument, a response adapter.
- You want guarantees: the binding makes the data path explicit and reviewable.

The two paths are not mutually exclusive — a partially-bound contract can use semantic auto-resolve for some tables and an explicit binding for others.

## How this plays at runtime

When an agent calls `strategy_run`:

1. The gateway loads `contract.yaml` to learn what tables the strategy wants.
2. It looks for `binding.yaml` next to it. If present, the binding tells it exactly how to fetch each table.
3. For any table not covered by the binding, the gateway falls back to auto-resolve via semantic tags + the knowledge graph.
4. The fetched rows are written to Parquet, loaded into a fresh DuckDB, and the strategy's `@step` methods run on top.

The strategy code in `strategy.py` only ever sees DuckDB tables matching the contract — it has no knowledge of where they came from. That's what makes the whole thing portable.

## Next

- [Bindings](/strategies/bindings) — the full `binding.yaml` schema, fetch modes, and field mapping
- [Contracts](/strategies/contracts) — the full `contract.yaml` schema, including semantic tags
- [Quick Start](/strategies/quickstart) — see all three files in a minimal working strategy
