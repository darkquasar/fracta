# Example strategies

Reference strategies, complete with `contract.yaml`, `strategy.py`, **and** a
`binding.yaml`. These are not picked up by the runner — directory names
beginning with `_` are skipped during discovery (see
`strategies/runner.py:77`) — so they exist purely as copy-and-modify
starting points.

Examples here mirror the production layout: `_example/<domain>/<category>/<slug>/`,
e.g. `_example/security/enrichment/splunk_field_survey/`. Copy into the
matching path under `strategies/` (without the leading `_example/`) to make
a strategy live.

## What lives here vs. what is live

| Strategy | Status | Where it lives |
|---|---|---|
| `knowledge-garden/correlation/cross_source_concepts` | **Promoted** (v0.5.2) | `strategies/knowledge-garden/correlation/cross_source_concepts/` |
| `knowledge-garden/enrichment/highlight_distill` | **Promoted** (v0.5.2) | `strategies/knowledge-garden/enrichment/highlight_distill/` |
| `knowledge-garden/traversal/notion_publish` | **Promoted** (v0.5.2) | `strategies/knowledge-garden/traversal/notion_publish/` |
| `security/enrichment/splunk_field_survey` | Template only | `strategies/_example/security/enrichment/splunk_field_survey/` |

The three knowledge-garden strategies were promoted out of `_example/` so
fresh deploys discover them automatically. The security example stays here
because it requires per-environment binding work (Elastic index pattern,
field-mapping conventions) before it's useful.

## Why this directory still exists

A strategy ships as two publishable files: `contract.yaml` (what data it
needs) and `strategy.py` (the DAG of steps). Those are environment-agnostic
and can be shared, version-controlled, and reused across teams.

The third file, `binding.yaml`, is **always custom**. It binds the contract
to *your* environment — your Elasticsearch instance, your VendorSecurity
tenant, your Snowflake warehouse. There is no canonical binding that ships
with a published strategy; whoever runs it writes their own.

That presents a teaching problem. Without a binding alongside the strategy,
new readers can't see what a complete three-file strategy looks like. This
directory solves that: every example here is a runnable strategy with a
reference binding, intended to be copied into your own
`strategies/<domain>/<category>/` tree and adapted.

## How to promote a template

You have two options:

### Option A — copy (operator-local override)

```bash
# Copy the whole directory into your real strategies tree, preserving the
# domain/category path
mkdir -p strategies/security/enrichment
cp -r strategies/_example/security/enrichment/splunk_field_survey \
      strategies/security/enrichment/

# Edit the binding to match your environment
$EDITOR strategies/security/enrichment/splunk_field_survey/binding.yaml
```

Use this when you only want the strategy in your deploy, not in source.

### Option B — `git mv` (contribute upstream)

```bash
git mv strategies/_example/security/enrichment/splunk_field_survey \
       strategies/security/enrichment/splunk_field_survey
```

Use this when you're promoting a template to ship with fracta — the way
the three knowledge-garden strategies were promoted in v0.5.2.

Once a strategy is outside `_example/`, the runner picks it up on the next
`strategy_list` or `strategy_run` call. No restart needed.

## What changes between environments

In `binding.yaml`, the fields most likely to change:

- `config_key` — your credentials/connection lookup key
- `mcp_server` — the registered name of your backend's MCP server
- `mcp_args` / `query_template` — how queries are shaped for your data
- `field_map` — source field names → contract column names
- `index` (or table/dataset selector) — which slice of data to read

The `contract.yaml` and `strategy.py` should not need editing.

## See also

- [Strategies overview](../../docs/strategies/overview.md)
- [Quick Start](../../docs/strategies/quickstart.md)
- [Portability](../../docs/strategies/portability.md) — why this split exists
- [Bindings](../../docs/strategies/bindings.md) — full `binding.yaml` schema
