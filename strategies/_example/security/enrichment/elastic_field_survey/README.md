## What this strategy does

Walks an Elasticsearch cluster's index metadata and field mappings to build
up a picture of the schema landscape — useful as a first-pass enrichment
before knowledge-graph ingestion or before authoring more targeted hunts.

## When to use it

- You've just connected a new Elasticsearch cluster and want to know what's
  in it before writing detection logic against specific indices.
- You're auditing field-naming consistency across many index patterns.
- You want to populate the knowledge graph with index-level entities so
  downstream strategies can `MATCH (i:Index)` against them.

## How it works

The strategy reads two pre-staged tables — `es_indices` (one row per index,
with doc count and on-disk size) and `es_mappings` (one row per field per
index) — and joins them in DuckDB to produce a per-index summary including
field counts and field-type distributions.

It does *not* call Elasticsearch directly; the binding is responsible for
fetching the data through the appropriate MCP tool. That keeps the strategy
itself portable across any source that exposes an "indices" + "mappings"
shape.

## What you need to adapt in your binding

- `mcp_server` — your registered Elasticsearch MCP server name
- `mcp_args.pattern` — restrict the survey to specific index patterns
- `field_map` — only if your MCP backend exposes the count/size/field
  metadata under different field names than the defaults assumed here

## Caveats

The strategy caps at 50 indices in the field-mapping pass to keep the
result size reasonable for in-context return to an agent. For larger
surveys, parametrize `index_pattern` to slice the work.
