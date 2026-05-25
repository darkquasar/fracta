## What this strategy does

Walks a Splunk deployment's index metadata and per-sourcetype field
extractions to build up a picture of the schema landscape — useful as a
first-pass enrichment before knowledge-graph ingestion or before authoring
more targeted hunts.

## When to use it

- You've just onboarded a new Splunk index (or set of indexes) and want to
  know what sourcetypes and fields it carries before writing SPL detections
  against it.
- You're auditing field-naming consistency across many sourcetypes (CIM
  conformance, custom-field drift, parsing-rule rot).
- You want to populate the knowledge graph with index-level entities so
  downstream strategies can `MATCH (i:Index)` against them.

## How it works

The strategy reads two pre-staged tables — `splunk_indexes` (one row per
index, with event count, on-disk size, and time bounds) and
`splunk_field_summary` (one row per extracted field per index/sourcetype,
sourced from `| fieldsummary`) — and groups them in DuckDB to produce a
per-index, per-sourcetype field inventory.

It does *not* call Splunk directly; the binding is responsible for fetching
the data through the appropriate MCP tool. That keeps the strategy itself
portable across any source that exposes an "indexes" + "field-summary"
shape.

## What you need to adapt in your binding

- `mcp_server` — your registered Splunk MCP server name (the example uses
  the placeholder `splunk`).
- `mcp_args.search` — the `| fieldsummary` query. Tune `earliest`/`head` to
  match your event volume; `| fieldsummary` is linear in event count and
  will not finish on a wide-open search of a busy index.
- `mcp_args.pattern` (for `list_indexes`) — restrict the survey to specific
  index patterns if your deployment has hundreds.
- `field_map` — only if your MCP backend exposes index metadata or
  fieldsummary output under different field names than the REST API's
  defaults (`name`, `totalEventCount`, `currentDBSizeMB`, etc.).

## Caveats

- The strategy caps at 50 indexes in the field-summary pass to keep the
  result size reasonable for in-context return to an agent. For larger
  surveys, parametrize `index_filter` to slice the work.
- `| fieldsummary` scales linearly with event count. The default
  `sample_window` is `-7d`; reduce it on noisy deploys (`-24h` is a safer
  starting point on a busy index). Operators with summary indexes or
  acceleration enabled can widen the window.
- Time bounds (`earliest`, `latest`) on `splunk_indexes` are reported as
  epoch seconds by `list_indexes` — leave them VARCHAR in the contract
  (Splunk's `| tstats` quirks turn them into mixed-type returns easily).
