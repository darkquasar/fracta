---
title: Response Adapters
description: Parsing non-standard tool responses
---

# Response Adapters

## Overview

Response adapters parse non-standard MCP tool output into structured rows for Parquet staging. They are used with `fetch_mode: mcp_client` when a tool returns output that isn't plain JSON, CSV, or NDJSON.

Adapters are selected via the `response_adapter` field in `binding.yaml`:

```yaml
source_bindings:
  alerts:
    fetch_mode: mcp_client
    mcp_tool: tabular_text
    mcp_server: vendor
    response_adapter: tabular_text
```

## Built-in Formats vs Adapters

There are two tiers of response handling:

**Tier 1: Built-in formats** (`response_format`) — generic, well-defined grammars:

| Format | Description |
|--------|-------------|
| `json` | Default. JSON array or object navigated via `items_path` |
| `csv` | RFC 4180 CSV. First row = headers |
| `ndjson` | Newline-delimited JSON. One object per line |

**Tier 2: Tool-specific adapters** (`response_adapter`) — registered parsers for non-standard output:

| Adapter | Tool | Description |
|---------|------|-------------|
| `tabular_text` | vendor Query | Prose headers + tabular row data (two sub-formats) |

`response_format` and `response_adapter` are mutually exclusive.

## The TabularText Adapter

The `tabular_text` adapter handles vendor Query output, which returns human-readable text rather than structured data. It auto-detects between two output formats:

### Format A: Pipe-delimited

```
Query executed successfully.
Columns: id | hostname | severity
<hr />
abc123 | host-1 | High
def456 | host-2 | Low
```

- `Columns:` line defines pipe-separated header names
- `---` separator marks the start of data rows
- Each data row uses `|` as the delimiter

### Format B: Python-list literals

```
Column Names: id, hostname, severity
Row 0: ['abc123', 'host-1', 'High']
Row 1: ['def456', 'host-2', 'Low']
```

- `Column Names:` line defines comma-separated header names
- `Row N:` lines contain Python-style list literals
- Quoted values handle commas (`'Doe, Jane'`) and escaped quotes (`'O\'Brien'`)

**Format detection:** If any line starts with `Column Names:`, format B is used. Otherwise, format A.

### Binding example

```yaml
source_bindings:
  outbound_connections:
    backend: vendor
    fetch_mode: mcp_client
    mcp_tool: tabular_text
    mcp_server: vendor
    response_adapter: tabular_text
    mcp_args:
      query: |
        | filter(event.type == "IP Connect")
        | columns endpoint.name, dst.ip.address, dst.port.number
        | limit 5000
      start_datetime: "{{start_time}}"
      end_datetime: "{{end_time}}"
    field_map:
      endpoint_name: endpoint.name
      dst_ip: dst.ip.address
      dst_port: dst.port.number
```

## Writing a Custom Adapter

### Function signature

```go
type ResponseAdapter func(text string, fields []FieldMapping) ([]map[string]any, error)
```

- `text` — the raw response body from the MCP tool
- `fields` — column metadata from the contract (source name, column name, type)
- Returns a slice of row maps (column name → value) or an error

### Registration

Register your adapter in `internal/loaders/response_adapters.go`:

```go
func init() {
    RegisterResponseAdapter("my_tool", parseMyToolResponse)
}

func parseMyToolResponse(text string, fields []FieldMapping) ([]map[string]any, error) {
    // Parse `text` into rows.
    // Each row is map[string]any where keys are source field names
    // (before field_map remapping — use the raw field names from the tool).
    var items []map[string]any

    // ... parsing logic ...

    return items, nil
}
```

### Registration API

```go
// RegisterResponseAdapter adds a named adapter to the global registry.
func RegisterResponseAdapter(name string, adapter ResponseAdapter)

// GetResponseAdapter returns the adapter for the given name, if registered.
func GetResponseAdapter(name string) (ResponseAdapter, bool)
```

### Guidelines

1. **Return source field names** — the staging layer handles `field_map` remapping. Your adapter should return keys matching the raw tool output, not the contract column names.
2. **All values as strings are fine** — the Parquet writer coerces types based on the contract's column type definitions.
3. **Return clear errors** — include enough context to diagnose the problem (e.g., "tabular_text: could not find column headers").
4. **Test both happy path and malformed input** — adapters are the boundary between unpredictable tool output and the structured staging layer.

## Interaction with Pagination

Response adapters work with **offset-mode** pagination. Each page is parsed independently through the adapter.

**Cursor-mode pagination is incompatible** with adapters — cursor extraction requires a JSON envelope, which adapter-parsed responses don't provide. Configuring `pagination.mode: cursor` with a `response_adapter` produces an error at runtime.

## Interaction with Fetch Modes

| Fetch mode | response_format | response_adapter | Behavior |
|------------|----------------|------------------|----------|
| `mcp_client` | `json` (default) | — | JSON parse + `items_path` navigation |
| `mcp_client` | `csv` | — | CSV parse, first row = headers |
| `mcp_client` | `ndjson` | — | Line-by-line JSON parse |
| `mcp_client` | — | `tabular_text` | TabularText-specific parser |
| `mcp` | ignored | ignored | Agent handles parsing manually |
| `native` | ignored | ignored | Strategy handles its own data |

Only `mcp_client` uses `response_format` and `response_adapter`. Other fetch modes ignore these fields.
