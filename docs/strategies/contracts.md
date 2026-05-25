---
title: Strategy Contracts
description: The contract.yaml schema and parameters
---

## Overview

A contract (`contract.yaml`) declares what a strategy needs to run: its parameters, required tables with column schemas, graph dependencies, and optional discovery hints. Contracts are the interface between strategy authors and the data-staging layer — they describe _what_ data is needed without specifying _how_ to fetch it.

Contracts live alongside strategy code:

```
strategies/security/hunt/my_hunt/
  contract.yaml
  binding.yaml      # optional — see docs/bindings.md
  strategy.py
```

## Contract Fields

### Top-level

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Strategy identifier (e.g., `"credential-stealer-hunt"`) |
| `version` | string | no | Semantic version (e.g., `"2.0.0"`) |
| `description` | string | yes | Human-readable purpose |
| `tags` | []string | yes | At least one tag for classification (e.g., `[hunt, macos]`) |
| `params` | map | no | Input parameters the strategy accepts |
| `requires` | object | no | Runtime dependencies (graph, tables, sources) |
| `pinned_backend` | string | no | Constrain resolution to a specific backend (e.g., `"elasticsearch"`) |
| `discovery` | object | no | Pre-staging guidance for orchestrators |

### params

Each key is a parameter name; the value is a `ParamSpec`:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | yes | `"str"`, `"int"`, `"float"`, `"bool"`, or `"list"` |
| `required` | bool | no | Whether the caller must provide this parameter |
| `default` | any | no | Default value if not provided |
| `description` | string | no | Parameter purpose (shown in `strategy_describe`) |

```yaml
params:
  days_back:
    type: int
    required: false
    default: 30
    description: "Number of days to look back"
  severity_filter:
    type: str
    required: false
    default: "HIGH,CRITICAL"
    description: "Comma-separated severity levels to include"
```

### requires

| Field | Type | Description |
|-------|------|-------------|
| `graph` | bool | Whether the strategy needs knowledge graph access |
| `duckdb` | bool | Whether the strategy needs DuckDB (implied by `tables`) |
| `tables` | map | Required staging tables with column schemas |
| `sources` | []string | Advisory list of expected LogSource names |

### requires.tables

Each key is a table name; the value is a `TableSpec`:

| Field | Type | Description |
|-------|------|-------------|
| `description` | string | What data this table holds |
| `optional` | bool | Strategy works without this table if `true` |
| `volume_hint` | string | Expected cardinality: `"low"`, `"medium"`, `"high"` |
| `columns` | map | Column definitions (see below) |

### Column definitions

Each key is a column name; the value is a `ColumnSpec`:

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | DuckDB type: `VARCHAR`, `INT64`, `FLOAT64`, `BOOLEAN`, `TIMESTAMP` |
| `semantic` | string | Semantic type linking to the knowledge graph (e.g., `"ip_address"`, `"identity_arn"`) |
| `description` | string | Column purpose |

Semantic tags enable automatic field resolution via the graph — the resolver matches columns to data sources by shared semantic types.

```yaml
requires:
  graph: true
  tables:
    auth_events:
      description: "Authentication events with geolocation"
      volume_hint: medium
      columns:
        identity_arn:
          type: VARCHAR
          semantic: identity_arn
        source_ip:
          type: VARCHAR
          semantic: ip_address
        timestamp:
          type: TIMESTAMP
          semantic: timestamp
```

### discovery

Optional hints for orchestrators that auto-stage data:

| Field | Type | Description |
|-------|------|-------------|
| `description` | string | Prose explanation of how to stage data |
| `mcp_hints` | []object | Suggested MCP tools |

Each `mcp_hint`:

| Field | Type | Description |
|-------|------|-------------|
| `tool` | string | MCP tool name (e.g., `"elasticsearch.search"`) |
| `purpose` | string | Why this tool is needed |
| `stage_as` | string | Table name to store results as |

```yaml
discovery:
  description: "Fetch auth events from Elasticsearch, stage as auth_events"
  mcp_hints:
    - tool: "elasticsearch.search"
      purpose: "Fetch authentication events with geo data"
      stage_as: "auth_events"
```

## Validation

The following are enforced at parse time:

- `name` must be non-empty
- `description` must be non-empty
- `tags` must contain at least one element

## Examples

### Minimal contract (graph-only, no tables)

```yaml
name: "correlate-ip-across-sources"
version: "1.0.0"
description: "Query the graph for all log sources with IP fields"
tags: [correlation, ip, multi-source]
params:
  ip:
    type: str
    required: true
    description: "IP address to investigate"
requires:
  graph: true
  sources: [CloudTrail, VPCFlowLogs, IdentitySystemLog]
```

### Full contract with tables and discovery

```yaml
name: "impossible-travel-detect"
version: "1.0.0"
description: "Detect impossible travel from authentication events"
tags: [detection, impossible-travel, geolocation]
params:
  identity_arn:
    type: str
    required: true
    description: "Identity ARN to analyze"
  max_speed_kmh:
    type: int
    required: false
    default: 900
    description: "Maximum plausible travel speed in km/h"
requires:
  graph: true
  tables:
    auth_events:
      description: "Authentication events with geolocation data"
      volume_hint: medium
      columns:
        identity_arn:
          type: VARCHAR
          semantic: identity_arn
        source_ip:
          type: VARCHAR
          semantic: ip_address
        latitude: { type: FLOAT64 }
        longitude: { type: FLOAT64 }
        timestamp:
          type: TIMESTAMP
          semantic: timestamp
  sources: [CloudTrail, IdentitySystemLog]
discovery:
  description: "Fetch auth events from Elasticsearch"
  mcp_hints:
    - tool: "elasticsearch.search"
      purpose: "Fetch authentication events with geo data"
      stage_as: "auth_events"
```

### Native-fetch contract (no tables)

```yaml
name: "credential-stealer-native"
version: "1.0.0"
description: "Self-contained credential stealer hunt (strategy fetches its own data)"
tags: [hunt, credential-theft, native]
params:
  days_back: { type: int, required: false, default: 30 }
  max_alerts: { type: int, required: false, default: 100 }
requires:
  graph: true
  duckdb: true
  # No tables — strategy fetches directly at runtime
```

### Pinned-backend contract

```yaml
name: "splunk-field-survey"
version: "1.0.0"
description: "Survey Splunk indexes and extracted fields per sourcetype"
tags: [enrichment, splunk, schema-discovery]
params:
  index_filter: { type: str, required: false, default: "*" }
pinned_backend: splunk
requires:
  graph: false
  tables:
    splunk_indexes:
      description: "Index metadata"
      columns:
        index: { type: VARCHAR }
        event_count: { type: VARCHAR }
        size_bytes: { type: VARCHAR }
```
