---
title: Strategy Bindings
description: The binding.yaml schema and fetch modes
---

# Strategy Bindings

## Overview

A binding (`binding.yaml`) maps a contract's abstract tables to concrete data sources. It tells the resolver _how_ to fetch each table: which backend, which MCP tool, what arguments, and how to parse the response.

Bindings are optional. Without one, the resolver attempts automatic resolution via the knowledge graph using semantic column tags. With a binding, you get explicit control over data sourcing.

```
strategies/hunt/my_hunt/
  contract.yaml     # what data is needed
  binding.yaml      # how to fetch it
  strategy.py
```

## Binding Structure

```yaml
source_bindings:
  <table_name>:       # must match a table in contract.yaml requires.tables
    backend: ...
    fetch_mode: ...
    # ... source-specific fields

field_overrides:       # optional global semantic overrides
  <semantic_type>:
    <table_name>: <source_field>
```

## Source Binding Fields

Each entry under `source_bindings` maps one contract table to a data source:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `backend` | string | — | Data backend name (`"elasticsearch"`, `"vendor"`, `"mcp"`, etc.) |
| `config_key` | string | — | Credentials/config lookup key (e.g., `"vendor_mcp"`, `"elastic"`) |
| `fetch_mode` | string | `"mcp"` | How data is fetched (see [Fetch Modes](#fetch-modes)) |
| `mcp_tool` | string | — | MCP tool to call (e.g., `"search"`, `"tabular_text"`) |
| `mcp_server` | string | — | MCP server name (e.g., `"elastic"`, `"vendor"`) |
| `mcp_args` | map | — | Static tool arguments; supports `{{param}}` interpolation |
| `query_template` | string | — | Query string with `{{param}}` placeholders |
| `index` | string | — | Backend-specific index/table selector (e.g., ES index pattern) |
| `field_map` | map | — | Maps contract column names to source field names |
| `items_path` | string | — | Dot-path to items array in JSON response (e.g., `"data"`, `"results.items"`) |
| `single_item` | bool | `false` | Response is one object, not an array |
| `max_rows` | int | `10000` | Hard cap on rows to stage |
| `timeout` | string | `"30s"` | Per-call timeout (e.g., `"120s"`, `"5m"`) |
| `response_format` | string | `"json"` | Response format: `"json"`, `"csv"`, `"ndjson"` |
| `response_adapter` | string | — | Tool-specific parser name (e.g., `"tabular_text"`) |
| `pagination` | object | — | Pagination configuration (see [Pagination](#pagination)) |

### Validation rules

- `response_format` and `response_adapter` are **mutually exclusive** — set one or the other, not both.
- `response_format` must be one of: `"json"`, `"csv"`, `"ndjson"` (or omitted for JSON default). Invalid values like `"cvs"` are rejected at parse time.

## Fetch Modes

The `fetch_mode` field controls who fetches the data and how:

| Mode | Who fetches | Response handling | When to use |
|------|------------|-------------------|-------------|
| `mcp_client` / `fracta_mcp_gateway` | Go MCPFetcher | Parsed by Go (JSON/CSV/NDJSON/adapter) | MCP tools with structured, parseable responses |
| `mcp` | Agent | Agent calls tool, stages via `strategy_stage` | Complex responses needing agent logic |
| `native` / `strategy_native` | Strategy Python code | Strategy writes to DuckDB at runtime | Strategies that compute or fetch inline |

**Default:** `"mcp"` (agent-driven staging).

`api` and `direct` were legacy Go loader-plugin modes and are no longer supported. Use `fracta_mcp_gateway` with an MCP server binding instead.

### Choosing a fetch mode

Use `mcp_client` when:
- The MCP tool returns JSON, CSV, or NDJSON
- You want automatic Parquet staging without agent intervention
- You need pagination support

Use `mcp` when:
- The tool returns complex or unpredictable output
- The agent needs to inspect/filter results before staging
- You want maximum flexibility

Use `native` when:
- The strategy fetches its own data (e.g., direct API calls from Python)
- No external staging is needed

## Response Formats

When using `fetch_mode: mcp_client`, the response parsing is controlled by `response_format` and `response_adapter`.

### Built-in formats (`response_format`)

| Format | Parser | Description |
|--------|--------|-------------|
| `json` (default) | `json.Unmarshal` + `items_path` navigation | Standard JSON array or object with nested items |
| `csv` | RFC 4180 CSV reader | First row = headers, subsequent rows = data |
| `ndjson` | Line-by-line JSON | One JSON object per line (newline-delimited) |

### Tool-specific adapters (`response_adapter`)

For tools that return non-standard output, use a named adapter instead of a format. See [docs/response-adapters.md](/guides/strategies/response-adapters) for the full adapter reference.

```yaml
# Example: vendor Query returns prose+table output
source_bindings:
  alerts:
    fetch_mode: mcp_client
    mcp_tool: tabular_text
    mcp_server: vendor
    response_adapter: tabular_text    # uses the TabularText-specific parser
```

## Field Mapping

The `field_map` maps contract column names (left) to source field names (right):

```yaml
field_map:
  alert_id: id             # contract column "alert_id" ← source field "id"
  alert_name: name
  severity: severity
  dst_ip: dst.ip.address   # dot-paths supported for nested fields
```

Alternatively, use `field_overrides` at the top level for semantic-based mapping that works across multiple tables:

```yaml
field_overrides:
  ip_address:
    auth_events: source_ip
    network_logs: src_addr
```

`field_map` takes precedence over `field_overrides` when both match the same column.

## Pagination

For large datasets, configure pagination to fetch data page-by-page. All pages are written to a single Parquet file.

### Offset mode

```yaml
pagination:
  mode: offset
  page_size: 10000
  offset_param: "from"       # query arg name for offset
  limit_param: "size"        # query arg name for page size
  total_path: "meta.total"   # optional: dot-path to total count for logging
```

The fetcher increments the offset by `page_size` each iteration: `from=0`, `from=10000`, `from=20000`, ...

### Cursor mode

```yaml
pagination:
  mode: cursor
  page_size: 1000
  limit_param: "size"
  cursor_param: "cursor"           # query arg name for cursor token
  next_cursor_path: "meta.next"    # dot-path to next cursor in JSON response
```

The fetcher extracts the next cursor from each response and passes it to the next page request.

**Constraint:** Cursor mode requires JSON responses. It is incompatible with `response_format: csv`, `response_format: ndjson`, and any `response_adapter` — the cursor lives in a JSON envelope that non-JSON formats don't have. This is enforced at runtime.

### Pagination fields

| Field | Type | Modes | Description |
|-------|------|-------|-------------|
| `mode` | string | all | `"offset"` or `"cursor"` |
| `page_size` | int | all | Results per page (default: 100) |
| `offset_param` | string | offset | Query arg name for offset value |
| `limit_param` | string | both | Query arg name for page size |
| `cursor_param` | string | cursor | Query arg name for cursor token |
| `next_cursor_path` | string | cursor | Dot-path to next cursor in response |
| `total_path` | string | offset | Dot-path to total count (optional, for logging) |

### Pagination limits

- **Total budget:** 5 minutes for the entire paginated fetch
- **Per-page timeout:** Controlled by `timeout` (default: 30s)
- **Row cap:** Controlled by `max_rows` (default: 10,000)
- **Termination:** Empty page, partial page, maxRows reached, or null cursor

## Parameter Interpolation

Both `mcp_args` and `query_template` support `{{param}}` placeholders that are resolved from strategy parameters at runtime. Interpolation is recursive — a param value can itself contain `{{other_param}}`.

```yaml
mcp_args:
  index: "logs-{{log_source}}-*"
  query_body:
    query:
      bool:
        filter:
          - terms:
              severity: "{{severity_filter}}"
```

## Examples

### Elasticsearch with pagination

```yaml
source_bindings:
  suspicious_dns:
    backend: dnsprovider_gateway
    config_key: elastic
    fetch_mode: mcp_client
    mcp_tool: search
    mcp_server: elastic
    mcp_args:
      index: "logs-dnsprovider.generic-*"
      query_body:
        size: 10000
        query:
          bool:
            filter:
              - terms:
                  dnsprovider.QueryCategoryNames: "{{categories}}"
    field_map:
      domain: dnsprovider.QueryName
      user_email: user.email
      timestamp: "@timestamp"
    max_rows: 50000
    timeout: "120s"
    pagination:
      mode: offset
      page_size: 10000
      offset_param: "from"
      limit_param: "size"
```

### VendorSecurity with agent-driven staging

```yaml
source_bindings:
  outbound_connections:
    backend: vendor
    config_key: vendor_mcp
    fetch_mode: mcp
    mcp_tool: tabular_text
    mcp_server: vendor
```

### CSV response from an MCP tool

```yaml
source_bindings:
  inventory:
    backend: asset_db
    fetch_mode: mcp_client
    mcp_tool: export_csv
    mcp_server: assets
    response_format: csv
    field_map:
      host_id: id
      hostname: name
      os: operating_system
```

### Multiple tables with field overrides

```yaml
source_bindings:
  alerts:
    backend: vendor
    config_key: vendor_mcp
    fetch_mode: mcp_client
    mcp_tool: search_alerts
    mcp_server: vendor_mcp
    field_map:
      alert_id: id
      alert_name: name
      severity: severity

  host_vulns:
    backend: vendor
    config_key: vendor_mcp
    fetch_mode: mcp
    mcp_tool: search_vulnerabilities
    mcp_server: vendor_mcp

field_overrides:
  ip_address:
    alerts: src_ip
    host_vulns: host_ip
```
