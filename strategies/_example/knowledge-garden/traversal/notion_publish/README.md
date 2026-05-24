## What this strategy does

Renders a `Concept` (or a batch of top-N concepts by confidence) into a
Notion page and creates-or-updates it idempotently. Uses a
`Publication` node — keyed by `(sink='notion', concept_name)` — to track
the Notion `page_id` and a SHA-256 content hash so that re-running with
unchanged inputs is a true no-op (no Notion API calls).

This is the third stage of the Reading Garden pipeline: once
`cross_source_concepts` has scored Concepts, `notion_publish` materialises
the high-confidence ones into Notion.

## When to use it

- After `cross_source_concepts` has computed up-to-date confidence scores.
- On a single `concept_name` for manual / spot publishing.
- In batch mode (omit `concept_name`) for a periodic publish cycle —
  picks the top-N by confidence.

## How it works

Four steps:

1. **load_target_concepts** — single concept by name, or top-N by
   confidence from the graph.
2. **load_supporting_highlights** — for each target concept, pull up to
   20 supporting `Highlight` nodes (with parent `Document`).
3. **render** — derive `epistemic_status` from `confidence` (`<0.4`
   seedling, `0.4-0.8` budding, `>0.8` evergreen), persist it on the
   `Concept` (this strategy is the writer-of-record for the property),
   build the Notion block-children payload, compute SHA-256
   `content_hash` of (blocks + writable properties).
4. **publish** — per concept:
   - Lookup `Publication` locally. If hash matches → **skip** (no API).
   - If a Publication exists but hash differs → `notion-update-page` +
     `append-block-children`.
   - If no local Publication → `notion-query-data-sources` by
     `concept_name` to find an adoptable Notion page →
     `notion-update-page` + `append-block-children` if found, else
     `notion-create-pages`.
   - Update `Publication.content_hash`, `Publication.last_updated_at`,
     and wire `Concept -[:PUBLISHED_AS]-> Publication`.

## Ownership seam (read me before editing)

This strategy writes **`Concept.epistemic_status`**, the entire
`Publication` node lifecycle, and the `PUBLISHED_AS` edge.

It does **NOT** write `Concept.confidence`,
`Concept.extraction_score`, or `Concept.mention_count`. Those belong
to `cross_source_concepts` and `highlight_distill` respectively. If
`confidence` and `epistemic_status` drift apart, the
`concept_confidence_status_mismatch` checkpoint rule (spec §3.5) flags
it — that is the intended signalling channel, not a tactical override.

## What you need to adapt in your binding

`notion_database_id` is passed at run time (not in the binding) because
it is per-publish-target, not per-environment. Change `notion` to your
registered Notion MCP server config_key if it is named differently.

## Caveats

- **Block update is wipe-and-replace by default.** `append-block-children`
  is called with the freshly rendered children; the prior body history
  is not preserved. A future flag (`preserve_history`) can opt into an
  append-only mode.
- **Notion rate limit: 3 req/s.** Each create costs 1 call; each update
  costs 2 (`notion-update-page` + `append-block-children`). Batch of
  100 concepts ≈ 2.5 minutes wall-clock.
- **OAuth via mcp-remote.** Static Notion integration tokens do not
  work with the hosted Notion MCP; mcp-remote with browser OAuth is the
  v1 path. The token cache at `~/.mcp-auth/` is plaintext on shared
  machines — set `MCP_REMOTE_CONFIG_DIR` to override.
- **Database must be shared with the connection.** A 404 on the first
  `notion-create-pages` call almost always means the Notion DB has not
  been shared with the integration; see setup.mdx.
