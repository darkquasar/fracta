---
title: Quick Start
description: Build a minimal strategy end-to-end — contract.yaml, strategy.py, and binding.yaml.
---

A strategy is three files. Two of them — `contract.yaml` and `strategy.py` — are the **publishable, environment-agnostic artifact**. The third — `binding.yaml` — is what binds the strategy to *your* environment. Two teams can share a strategy by sharing the first two files; each team writes its own binding to point at its own MCP backends.

This walkthrough builds a strategy that counts documents in an Elasticsearch index.

## The directory

```
strategies/
  security/
    enrichment/
      my_strategy/
        contract.yaml      # what data the strategy needs
        strategy.py        # the DAG of @step methods
        binding.yaml       # how to fetch it in *this* environment
```

The convention is `strategies/<domain>/<category>/<slug>/` — domain first (e.g. `security`, `infra`), then category (e.g. `enrichment`, `hunt`, `detection`). The runner discovers strategies regardless of nesting depth.

## contract.yaml

Declares the strategy's identity, parameters, and the abstract data tables it needs:

```yaml
name: "my-strategy"
version: "1.0.0"
description: "Count documents in a staged table."
tags: [enrichment, example]
params:
  limit:
    type: int
    required: false
    default: 100
    description: "Max rows to scan"
requires:
  graph: false
  tables:
    my_data:
      description: "Input data from Elasticsearch"
      optional: false
      columns:
        _id:
          type: VARCHAR
        message:
          type: VARCHAR
```

Note that the contract talks about a table called `my_data` with two columns. It says nothing about *where* that table comes from — that's the binding's job.

## strategy.py

The DAG of pipeline steps. Each `@step` reads from `ctx.duckdb` (where staged tables are pre-loaded) and returns a value that downstream steps can depend on by parameter name:

```python
from fracta_strategies import Strategy, step

class MyStrategy(Strategy):

    @step("Load data")
    def load_data(self, ctx):
        rows = ctx.duckdb.execute(
            "SELECT _id, message FROM my_data LIMIT ?",
            [ctx.params.get("limit", 100)]
        ).fetchall()
        return [{"id": r[0], "message": r[1]} for r in rows]

    @step("Summarize")
    def summarize(self, ctx, load_data):
        return {
            "count": len(load_data),
            "sample": load_data[:3],
        }
```

`contract.yaml` and `strategy.py` together are the part of the strategy you can publish, share, and version-control without leaking anything about your specific environment.

## binding.yaml

The binding maps the contract's abstract `my_data` table to a concrete MCP data source — in this case, an Elasticsearch index reachable through the gateway:

```yaml
source_bindings:
  my_data:
    backend: elasticsearch
    config_key: elastic
    fetch_mode: fracta_mcp_gateway
    mcp_tool: search
    mcp_server: elastic
    index: "logs-*"
    field_map:
      _id: _id
      message: message
```

A different environment would write a different binding — different backend, different index, different field names — and the same `contract.yaml` + `strategy.py` would run unchanged. See [Portability](/strategies/portability) for the full story on why this split exists, and [Bindings](/strategies/bindings) for the complete schema and fetch modes.

## Run it

An agent calls `strategy_run(name="my-strategy")`. The gateway reads the binding to know *where* to fetch each table declared in the contract, stages the result as Parquet, loads it into a per-run DuckDB, and the sidecar executes the steps in DAG order. The agent gets back the structured `summarize` result.

## A complete worked example in the repo

For a runnable three-file strategy you can copy and adapt, see [`strategies/_example/security/enrichment/elastic_field_survey/`](https://github.com/darkquasar/fracta/tree/main/strategies/_example/security/enrichment/elastic_field_survey) in the repo. It surveys Elasticsearch indices and field mappings, and ships with a reference `binding.yaml` you can edit to point at your own ES instance. The `_example/` directory is skipped by the runner (directories starting with `_` are ignored on discovery) — copy a strategy out into a real `<domain>/<category>/` path under `strategies/` to make it live.

## Next

- [Strategies overview](/strategies/overview) — the full framework reference
- [Contracts](/strategies/contracts) — every field in `contract.yaml`
- [Bindings](/strategies/bindings) — every field in `binding.yaml`, plus fetch modes and pagination
- [Portability](/strategies/portability) — why the contract/binding split exists
- [Lifecycle](/strategies/lifecycle) — discovery, hot reload, and governance status
