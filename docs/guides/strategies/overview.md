---
title: Strategies
description: DAG-based investigation pipelines run by the python sidecar
---

# Strategy Developer Guide

Strategies are reusable Python DAG pipelines that run inside a sidecar process. They receive data as Parquet tables (staged from MCP backends), execute steps in dependency order using DuckDB, and return structured results.

## Quick Start

Create a strategy that counts documents in an Elasticsearch index:

```
strategies/
  enrichment/
    my_strategy/
      contract.yaml
      strategy.py
```

**contract.yaml**:
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

**strategy.py**:
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

Run it: an agent calls `strategy_run(name="my-strategy")`. The gateway auto-resolves data from the binding, stages it as Parquet, and the sidecar executes the steps.

<hr />

## Authoring Paths

There are two supported ways to create strategies:

1. **Manual filesystem authoring** — create `contract.yaml`, `strategy.py`, and optional `binding.yaml` under `strategies/<category>/<slug>/`.
2. **MCP-driven creation** — call `strategy_create` with Python code plus either:
   - `contract` (preferred, YAML string for `contract.yaml`)
   - `metadata` (legacy JSON path)

`strategy_create` writes the strategy files through the sidecar and can also register governance nodes in the graph when the graph is connected.

Use manual authoring when iterating locally in the repo. Use `strategy_create` when an agent or operator is compiling exploratory work into a reusable strategy through  fracta itself.

<hr />

## Directory Layout

```
strategies/
  <category>/
    <strategy_slug>/
      contract.yaml      # Required: metadata, params, data requirements
      strategy.py         # Required: Strategy subclass with @step methods
      binding.yaml        # Optional: maps tables to concrete MCP data sources
```

**Discovery rules**:
- The runner walks `strategies/` recursively looking for directories containing both `contract.yaml` AND `strategy.py`
- Directories starting with `.` or `_` are skipped (`.venv`, `__pycache__`, etc.)
- `contract.yaml` must contain a `name` field
- Discovery is fresh on every call (list, describe, run) — no caching

**Categories** are conventional directory names: `enrichment/`, `hunt/`, `detection/`, `correlation/`, `traversal/`.

<hr />

## Contract Reference

The `contract.yaml` defines what the strategy does, what parameters it accepts, and what data it needs.

### Required Fields

```yaml
name: "strategy-name"         # Unique identifier, hyphenated
version: "1.0.0"              # Semantic version
description: >                # What this strategy does
  Multi-line description.
tags: [hunt, dns, anomaly]    # At least one tag required
```

### Parameters

```yaml
params:
  time_start:
    type: str                 # str | int | float | bool
    required: true
    description: "ISO 8601 start time"
  severity_threshold:
    type: int
    required: false
    default: 50
    description: "Minimum severity score"
```

Types are validated and coerced at runtime: float64 → int, string → bool, etc.

### Data Requirements

```yaml
requires:
  graph: false                # Whether the strategy needs FalkorDB access
  tables:
    alerts:
      description: "Alert data from VendorSecurity"
      optional: false         # Required tables must be staged before execution
      columns:
        alert_id:
          type: VARCHAR
          semantic: alert_id  # Semantic tag enables auto-resolve
        severity:
          type: INTEGER
        detected_at:
          type: TIMESTAMP
```

**Column types**: Any valid DuckDB type — `VARCHAR`, `INTEGER`, `BIGINT`, `DOUBLE`, `TIMESTAMP`, `BOOLEAN`, etc.

**Semantic tags**: Optional hints that enable the auto-resolve pipeline to match columns to MCP backend fields. Without semantic tags, a `binding.yaml` is required for auto-staging.

### Backend Pinning and Discovery Hints

```yaml
pinned_backend: elasticsearch  # Strategy always uses this backend

discovery:
  description: >
    How the orchestrator should stage data for this strategy.
  mcp_hints:
    - tool: "elasticsearch.search"
      purpose: "Fetch sample documents"
      stage_as: "my_data"
```

`pinned_backend` tells the resolver which MCP backend to use. `mcp_hints` are advisory — they guide the orchestrator and auto-resolve pipeline.

<hr />

## Binding Reference

The optional `binding.yaml` maps contract table requirements to concrete MCP data sources. Required when columns lack semantic tags for auto-resolve.

```yaml
source_bindings:
  alerts:
    backend: vendor
    config_key: vendor_mcp
    fetch_mode: mcp              # See fetch modes below
    mcp_tool: search_alerts
    mcp_server: vendor
    query_template: |
      [{"fieldId": "severity", "values": ["{{severity_filter}}"]}]
    field_map:
      alert_id: id               # table column: source field
      severity: severity
      detected_at: detected_at
```

### Fetch Modes

| Mode | Who fetches | When | Use case |
|---|---|---|---|
| `fracta_mcp_gateway` | Go (MCP client pool) | Inline at strategy_run time | Default for gateway-connected backends. Auto-staged. |
| `mcp` | Agent (via `strategy_stage`) | Agent calls tool, stages result | When agent must orchestrate the fetch (e.g., multi-step queries) |
| `native` | Strategy Python code | At runtime via `ctx.graph` or `ctx.duckdb` | Graph-only strategies that populate tables themselves |

### Query Templates

Templates use `{{param_name}}` placeholders resolved from strategy params:

```yaml
query_template: |
  {"index": "logs-*", "query": {"range": {"@timestamp": {"gte": "{{time_start}}"}}}}
```

### Field Mapping

`field_map` renames source fields to match contract column names:

```yaml
field_map:
  alert_id: id           # contract column "alert_id" ← source field "id"
  asset_name: asset.name # nested source field with dot notation
```

### Pagination (for large tables)

```yaml
pagination:
  mode: offset           # offset | cursor
  page_size: 10000
  offset_param: "from"
  limit_param: "size"
```

Background staging is used for large paginated `fracta_mcp_gateway` fetches. The current heuristic is:
- `pagination` is configured
- and `max_rows > 50000`

When that path is selected, the agent receives `{"status": "staging"}` and can poll with the `session_id`.

<hr />

## Python Framework

### Strategy Base Class

Every strategy is a Python class that extends `Strategy`:

```python
from fracta_strategies import Strategy, step

class MyStrategy(Strategy):
    @step("Step name")
    def my_step(self, ctx):
        return {"key": "value"}
```

### @step Decorator

Marks a method as a pipeline step. Steps run in topologically sorted order.

```python
@step("Human-readable step name")
def my_step(self, ctx):
    ...
```

**Dependencies** are inferred from parameter names:

```python
@step("Load data")
def load_data(self, ctx):
    return ctx.duckdb.execute("SELECT * FROM my_table").fetchall()

@step("Process")
def process(self, ctx, load_data):  # ← depends on load_data
    return [transform(row) for row in load_data]

@step("Report")
def report(self, ctx, load_data, process):  # ← depends on both
    return {"raw": len(load_data), "processed": len(process)}
```

For explicit ordering, use `depends`:

```python
@step("Cleanup", depends=["process"])
def cleanup(self, ctx):
    ...
```

### StrategyContext

Every step receives `ctx` with:

| Field | Type | Description |
|---|---|---|
| `ctx.duckdb` | `duckdb.Connection` | Fresh per-run DuckDB (400MB memory limit, spill to disk). Staged tables are pre-loaded. |
| `ctx.graph` | `FalkorDB client` or `None` | Knowledge graph access. Only available when `requires.graph: true` and graph is configured. |
| `ctx.params` | `dict` | Validated and type-coerced parameters from the caller. |

### Querying Staged Data

Tables declared in `requires.tables` are loaded into DuckDB from Parquet before execution:

```python
@step("Analyze alerts")
def analyze(self, ctx):
    high = ctx.duckdb.execute(
        "SELECT count(*) FROM alerts WHERE severity > ?",
        [ctx.params.get("severity_threshold", 50)]
    ).fetchone()[0]
    return {"high_severity_count": high}
```

### Querying the Knowledge Graph

When `requires.graph: true`:

```python
@step("Find related systems")
def find_systems(self, ctx):
    result = ctx.graph.query(
        "MATCH (s:System)-[:DETECTED_ON]-(e:Event) "
        "WHERE e.semantic = 'credential_theft' RETURN s.name, count(e)"
    )
    return [{"system": r[0], "events": r[1]} for r in result.result_set]
```

<hr />

## Data Flow

```
Agent calls strategy_run(name="my-strategy", params={...})
       |
       v
Gateway reads contract.yaml + binding.yaml
       |
       v
Auto-resolve: matches tables to MCP backends
       |
       +--> fracta_mcp_gateway: Go fetches via MCP pool → Parquet
       +--> mcp: returns "pending", agent stages data
       +--> native: strategy populates at runtime
       |
       v
Runner loads Parquet into DuckDB tables
       |
       v
Strategy steps execute in DAG order
       |
       v
Result returned to agent
```

**Staging path**: `{staging_dir}/{run_id}/{table_name}.parquet`

Each run gets a unique 8-character hex ID. Parquet files are cleaned up after execution.

<hr />

## MCP Tools

Agents interact with strategies through these MCP tools:

| Tool | Purpose |
|---|---|
| `strategy_list` | List all discovered strategies. When the graph is connected, results include governance status. |
| `strategy_describe` | Get full details for a strategy including resolution plan |
| `strategy_run` | Execute a strategy. Auto-resolves data, runs steps, returns result. |
| `strategy_create` | Create a new strategy from Python code plus `contract` YAML (preferred) or legacy `metadata` JSON |
| `strategy_stage` | Manually stage MCP results as Parquet (for `fetch_mode: mcp`) |
| `strategy_stage_status` | Query persistent staging progress for a run (`session_id`) |
| `strategy_resolve` | Preview the resolution plan without executing |
| `strategy_match` | Find strategies matching an investigation intent (scored) |
| `strategy_promote` | Advance a strategy from validated to promoted status |

### strategy_list Filtering

`strategy_list` does **not** currently default to `validated,promoted`. By default it returns all discovered strategies and, when the graph is connected, enriches them with governance status.

Use the optional `status` parameter to filter explicitly:
- `status="validated,promoted"`
- `status="exploratory"`
- `status="all"`

### strategy_run Response States

| Status | Meaning | Next action |
|---|---|---|
| `complete` | Strategy executed successfully | Read `result` and `trace` |
| `error` | Strategy failed | Read `error` and `structured_error` |
| `pending` | Agent must stage data | Call `strategy_stage` for each pending table, then retry with `session_id` |
| `staging` | Background staging in progress | Retry with `session_id` to poll or get result |
| `executing` | Another caller is running this session | Wait and retry with `session_id` |

<hr />

## Error Handling

### Partial Results

When a step fails, all previously completed step outputs are preserved:

```json
{
  "status": "error",
  "error": "step 'analyze' failed: division by zero",
  "partial_results": {
    "load_data": [{"id": "1"}, {"id": "2"}]
  },
  "trace": {
    "steps": [
      {"name": "Load data", "status": "ok", "duration_ms": 12},
      {"name": "Analyze", "status": "error", "duration_ms": 1, "error": "division by zero"}
    ]
  }
}
```

Partial results are capped at 16 KB per step and 64 KB total.

### Structured Errors

Strategy errors include classification for client-side handling:

```json
{
  "structured_error": {
    "message": "auto-resolve failed: ...",
    "category": "transient",
    "retryable": true,
    "phase": "resolution"
  }
}
```

Categories: `transient` (retryable), `permanent` (fix required), `validation` (bad input).

<hr />

## Deployment

### Docker Image

Strategies are baked into the Docker image at `/opt/fracta/strategies/`. The `Dockerfile` copies the `strategies/` directory:

```dockerfile
COPY strategies/ /opt/fracta/strategies/
```

### Hot Reload

The runner re-discovers strategies on every call (list, describe, run). To deploy a new strategy without rebuilding:

```bash
# Copy runtime files to the strategy-runner container
GWPOD=$(kubectl get pod -n  fracta -l component=fracta-gateway -o jsonpath='{.items[0].metadata.name}')

kubectl exec -n  fracta $GWPOD -c strategy-runner -- mkdir -p /opt/fracta/strategies/<category>/<slug>
kubectl cp contract.yaml fracta/$GWPOD:/opt/fracta/strategies/<category>/<slug>/contract.yaml -c strategy-runner
kubectl cp strategy.py   fracta/$GWPOD:/opt/fracta/strategies/<category>/<slug>/strategy.py   -c strategy-runner

# Copy binding overrides to the gateway container
kubectl exec -n  fracta $GWPOD -c fracta-gateway -- mkdir -p /opt/fracta/strategies/<category>/<slug>
kubectl cp binding.yaml  fracta/$GWPOD:/opt/fracta/strategies/<category>/<slug>/binding.yaml  -c fracta-gateway
```

No restart needed — the strategy is available on the next `strategy_list` or `strategy_run` call.

### Dual-Container Requirement

In K8s mode, the gateway pod has two containers:
- **fracta-gateway** (Go): Reads `binding.yaml` for per-strategy binding overrides and uses `strategy_describe` metadata from the sidecar for resolution
- **strategy-runner** (Python): Reads `contract.yaml` and `strategy.py` for discovery and execution

In practice:
- `strategy-runner` needs `contract.yaml` and `strategy.py`
- `fracta-gateway` needs `binding.yaml`

Copying `contract.yaml` to the gateway container is harmless, but it is not the critical file for per-strategy resolution. The important split is runtime code + contract in the runner, binding overrides in the gateway.
