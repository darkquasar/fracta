## What this strategy does

Recomputes `Concept.confidence` and `Concept.mention_count` for every
`Concept` in the graph, and derives `MENTIONS.weight` from
`MENTIONS.extraction_score × recency_factor`. Reads only — fans out no
MCP calls and stages nothing in DuckDB.

This is the second stage of the Reading Garden pipeline. It folds the
per-mention `extraction_score` written by `highlight_distill` into a
graph-aware confidence score that `notion_publish` later maps to
`Concept.epistemic_status` (`seedling` / `budding` / `evergreen`).

## When to use it

- After every `highlight_distill` run (the new MENTIONS edges shift
  mention counts and mean extraction scores).
- On a periodic cron (daily / weekly) so the recency decay term keeps
  drifting older concepts toward lower confidence even when no new
  highlights arrive.

## How it works

```
confidence = sigmoid(
    w_freq      · log1p(mention_count)
  + w_recency   · 0.5 ** (days_since_last_seen / 90)
  + w_diversity · domain_source_count
  + w_extract   · mean(MENTIONS.extraction_score)
)
```

Default weights: `{freq: 0.30, recency: 0.15, diversity: 0.40, extract: 0.15}`.

The diversity term is the load-bearing cross-source corroboration: a
Concept seen in `N` highlights across `M` distinct `DomainSource`s gets
`M`, not `N`, credit. That is why `highlight_distill` MERGEs the
`CAPTURED_FROM → DomainSource` chain — without those edges this term is
always zero.

`MENTIONS.weight` is set per edge as `extraction_score × recency_factor`.
The recency factor decays at a 90-day half-life from each Concept's
`last_seen_at`; freshly-mentioned concepts keep their edge weight near
the raw extraction score, while older mentions naturally fade.

## Ownership seam (read me before editing)

This strategy writes **only** `Concept.confidence`,
`Concept.mention_count`, `Concept.last_updated`, and `MENTIONS.weight`.

It does **NOT** write `Concept.extraction_score` — that field belongs
to `highlight_distill`. The two-writer authority pattern (one strategy
per property) mirrors spec-32. The verification checklist (spec §8)
checks that `Concept.extraction_score` values are unchanged after this
strategy runs.

If a future contributor wants `cross_source_concepts` to update
`extraction_score`, that is a spec change, not a tactical edit.

## Caveats

- **No alias merging.** If `popper` and `karl popper` both exist as
  separate Concept nodes, this strategy scores them independently. The
  `concept_low_extraction_high_confidence` checkpoint rule (spec §3.5)
  is what flags such drift.
- **The sigmoid output is in `(0, 1)`, never exactly 0 or 1.** A Concept
  with zero mentions and zero diversity still gets `sigmoid(0) = 0.5`.
  Tune the weights or shift with a bias term if a strictly increasing
  zero-anchored output is preferred.
