---
title: Lifecycle
description: Discovery, hot reload, governance status, and the dual-container split
---

# Strategy Lifecycle

How strategies get found, deployed, updated, and graduated from exploratory work to validated production assets.

## Discovery

The strategy runner walks the `strategies/` directory tree recursively on every call (`strategy_list`, `strategy_describe`, `strategy_run`). There is no caching — drop a new strategy on disk and the next call sees it.

Discovery rules:

- The runner looks for directories containing both `contract.yaml` AND `strategy.py`.
- Directories starting with `.` or `_` are skipped (`.venv`, `__pycache__`, etc.).
- `contract.yaml` must contain a `name` field; otherwise the strategy is rejected.
- Subdirectories are arbitrary — the runner uses `os.walk`, so paths can nest as deep as you want. The convention is **domain first, then category**: `strategies/<domain>/<category>/<slug>/`, e.g. `strategies/security/enrichment/elastic_field_survey/`. Common categories: `enrichment`, `hunt`, `detection`, `correlation`, `traversal`. The directory layout is purely organizational — a strategy's identity comes from the `name` field in its `contract.yaml`, not its path.

`binding.yaml` is loaded if present alongside the contract. Its absence does not block discovery — the resolver falls back to semantic-tag auto-resolve. See [Portability](/strategies/portability) for when each path applies.

## Hot reload

Because discovery is fresh on every call, deploying a new or updated strategy in a running cluster is a matter of copying the right files into the right container. No restart, no rebuild.

In Kubernetes:

```bash
GWPOD=$(kubectl get pod -n fracta -l component=fracta-gateway -o jsonpath='{.items[0].metadata.name}')

# Runtime files go to the strategy-runner container
kubectl exec -n fracta $GWPOD -c strategy-runner -- mkdir -p /opt/fracta/strategies/<domain>/<category>/<slug>
kubectl cp contract.yaml fracta/$GWPOD:/opt/fracta/strategies/<domain>/<category>/<slug>/contract.yaml -c strategy-runner
kubectl cp strategy.py   fracta/$GWPOD:/opt/fracta/strategies/<domain>/<category>/<slug>/strategy.py   -c strategy-runner

# Binding overrides go to the fracta-gateway container
kubectl exec -n fracta $GWPOD -c fracta-gateway -- mkdir -p /opt/fracta/strategies/<domain>/<category>/<slug>
kubectl cp binding.yaml  fracta/$GWPOD:/opt/fracta/strategies/<domain>/<category>/<slug>/binding.yaml  -c fracta-gateway
```

The strategy is available on the next `strategy_list` or `strategy_run` call.

In Docker Compose or local-process mode, just write the files to the strategies directory the runner is configured to scan — no container plumbing needed.

For permanent deployments, bake the strategy into the Docker image instead:

```dockerfile
COPY strategies/ /opt/fracta/strategies/
```

## Dual-container split

In Kubernetes the gateway pod runs two containers, and which file goes where matters:

| Container | Reads | Why |
|---|---|---|
| **strategy-runner** (Python) | `contract.yaml` + `strategy.py` | Discovery and step execution |
| **fracta-gateway** (Go) | `binding.yaml` | Per-strategy binding overrides during resolution |

In practice, `strategy-runner` needs the contract and the Python; `fracta-gateway` needs the binding. Copying `contract.yaml` to the gateway container is harmless but not what makes resolution work — the load-bearing split is *runtime code + contract in the runner, binding overrides in the gateway*.

## Governance status

When the knowledge graph is connected, every strategy carries a governance status. The status reflects how much trust an operator has placed in the strategy, not whether it runs:

| Status | Meaning | Entry condition |
|---|---|---|
| `exploratory` | Newly authored or experimental. Runs fine, but not promoted as a production asset. | Default for newly created strategies |
| `validated` | Proven reliable through repeated use. | Automatic: run_count >= 5 AND reliability >= 0.8 |
| `promoted` | Validated *and* recommended for routine use. The highest tier. | Operator calls `strategy_promote` (or auto: run_count >= 20, reliability >= 0.95, composite_score >= 0.7) |
| `deprecated` | Scheduled for removal. | Operator marks deprecated |
| `retired` | No longer available for execution. | Automatic: 30 days after deprecation with no runs |

**Auto-transitions:**
- `exploratory -> validated`: When a strategy accumulates >= 5 runs with >= 0.8 reliability score.
- `promoted -> validated` (demotion): If reliability drops below 0.7, the strategy is demoted back to validated.
- `deprecated -> retired`: After 30 days with no executions, deprecated strategies are automatically retired.

`strategy_list` does not filter by status by default — it returns every discovered strategy and, when the graph is connected, enriches each with its governance status. To filter explicitly, pass the `status` parameter:

- `status="validated,promoted"` — only trusted strategies
- `status="exploratory"` — only experimental strategies
- `status="all"` — same as the default

Strategies authored through `strategy_create` start as `exploratory`. An operator advances them with `strategy_promote` once they've been reviewed.

## Auto-generated catalogue

Every strategy under `strategies/<domain>/<category>/<slug>/` shows up automatically as a page in the [Strategies → Catalogue](/strategies/catalogue) sidebar group. The catalogue page is built from:

- `contract.yaml` — name, version, description, tags, parameters, required tables.
- `strategy.py` — parsed via Python AST (never imported or executed) to extract the `@step` DAG, which renders as a Mermaid flowchart.
- `README.md` (optional) — your hand-written prose, inlined verbatim. This is where you describe what the strategy does, when to use it, what to adapt in the binding, and any caveats. Markdown including Mermaid blocks is supported.

To preview the catalogue locally before pushing:

```bash
make docs-gen
```

This runs `scripts/gen-strategy-catalogue.py`, which writes `.mdx` files into `docs/strategies/catalogue/` and updates the `Catalogue` subgroup in `docs.json`. On push to `main`, the [`strategy-catalogue` GitHub workflow](https://github.com/darkquasar/fracta/blob/main/.github/workflows/strategy-catalogue.yml) regenerates and commits the catalogue automatically. CI also runs `make docs-gen-check` on every PR — a PR that changes a strategy without committing the regenerated catalogue will fail.

The example strategy at [`strategies/_example/security/enrichment/elastic_field_survey/`](https://github.com/darkquasar/fracta/tree/main/strategies/_example/security/enrichment/elastic_field_survey) shows the file shape an author should aim for: contract + strategy + binding + README. Copy it as your starting point.

## Next

- [Overview](/strategies/overview) — the full framework reference, including the MCP tools listed above
- [Quick Start](/strategies/quickstart) — build a minimal strategy end-to-end
- [Bindings](/strategies/bindings) — what `fracta-gateway` is reading from `binding.yaml` 
