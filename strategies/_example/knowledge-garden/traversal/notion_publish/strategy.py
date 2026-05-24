"""Publish Concept(s) to a Notion database idempotently.

Spec-41 §4.3. All four Notion tool calls go through ctx.mcp.call_tool —
the `actions:` framework extension is dropped from spec-41 scope.

Pipeline (per Concept):
  1. local lookup Publication by (sink='notion', concept_name)
  2. derive epistemic_status from confidence and write it to the graph
  3. render block children + properties; compute SHA-256 content_hash
  4. branch:
       - hash match              → skip (no API calls)
       - hash differs            → notion-update-page + append-block-children
       - not found locally       → notion-query-data-sources (adopt-or-create)
                                   → notion-create-pages if not adoptable
  5. update Publication.content_hash, last_updated_at; wire Concept-PUBLISHED_AS->Publication
"""

from __future__ import annotations

import datetime as _dt
import os
import sys
from typing import Any

from fracta_strategies import Strategy, step

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from render import (  # noqa: E402
    content_hash,
    epistemic_status_for,
    page_properties,
    render_concept_blocks,
)


def _iso_now() -> str:
    return _dt.datetime.now(_dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _row_to_dict(row) -> dict:
    """Defensive row → dict (FalkorDB returns dicts or lists across versions)."""
    if isinstance(row, dict):
        return row
    return {}


class NotionPublish(Strategy):

    @step("Load target Concept(s) from graph")
    def load_target_concepts(self, ctx) -> list[dict]:
        if ctx.graph is None:
            return []
        params = ctx.params or {}
        single = params.get("concept_name")
        batch_size = int(params.get("batch_size", 10))
        if single:
            cypher = (
                "MATCH (c:Concept {name: $name}) "
                "RETURN c.name AS name, c.display_name AS display_name, "
                "c.description AS description, c.confidence AS confidence, "
                "c.extraction_score AS extraction_score, "
                "c.mention_count AS mention_count, "
                "c.epistemic_status AS epistemic_status"
            )
            rows = ctx.graph.execute(cypher, {"name": single})
        else:
            cypher = (
                "MATCH (c:Concept) "
                "WHERE c.confidence IS NOT NULL "
                "RETURN c.name AS name, c.display_name AS display_name, "
                "c.description AS description, c.confidence AS confidence, "
                "c.extraction_score AS extraction_score, "
                "c.mention_count AS mention_count, "
                "c.epistemic_status AS epistemic_status "
                "ORDER BY c.confidence DESC LIMIT $n"
            )
            rows = ctx.graph.execute(cypher, {"n": batch_size})

        result_set = getattr(rows, "result_set", rows) or []
        out: list[dict] = []
        for row in result_set:
            if isinstance(row, dict):
                out.append(row)
            else:
                (name, display_name, description, confidence,
                 extraction_score, mention_count, epistemic_status) = row
                out.append({
                    "name": name, "display_name": display_name,
                    "description": description,
                    "confidence": confidence,
                    "extraction_score": extraction_score,
                    "mention_count": mention_count,
                    "epistemic_status": epistemic_status,
                })
        return out

    @step("Load supporting Highlights for each Concept")
    def load_supporting_highlights(self, ctx, load_target_concepts: list[dict]) -> dict[str, list[dict]]:
        if ctx.graph is None:
            return {}
        out: dict[str, list[dict]] = {}
        for concept in load_target_concepts:
            cypher = (
                "MATCH (c:Concept {name: $name})<-[:MENTIONS]-(h:Highlight) "
                "OPTIONAL MATCH (h)-[:PART_OF]->(d:Document) "
                "RETURN h.text AS text, h.note AS note, "
                "d.title AS book_title, d.author AS author "
                "LIMIT 20"
            )
            rows = ctx.graph.execute(cypher, {"name": concept["name"]})
            result_set = getattr(rows, "result_set", rows) or []
            highlights: list[dict] = []
            for row in result_set:
                if isinstance(row, dict):
                    highlights.append(row)
                else:
                    text, note, title, author = row
                    highlights.append({
                        "text": text, "note": note,
                        "book_title": title, "author": author,
                    })
            out[concept["name"]] = highlights
        return out

    @step("Write epistemic_status to graph and render Notion payloads")
    def render(self, ctx, load_target_concepts: list[dict],
               load_supporting_highlights: dict[str, list[dict]]) -> list[dict]:
        rendered: list[dict] = []
        for concept in load_target_concepts:
            status = epistemic_status_for(concept.get("confidence"))
            # Persist epistemic_status on the Concept BEFORE render so the
            # `concept_confidence_status_mismatch` checkpoint detects drift
            # symmetrically. notion_publish is the writer-of-record for
            # this property (spec §3.2).
            if ctx.graph is not None:
                ctx.graph.execute(
                    "MATCH (c:Concept {name: $name}) "
                    "SET c.epistemic_status = $status",
                    {"name": concept["name"], "status": status},
                )
            concept_with_status = {**concept, "epistemic_status": status}
            highlights = load_supporting_highlights.get(concept["name"], [])
            blocks = render_concept_blocks(concept_with_status, highlights)
            props = page_properties(concept_with_status, status)
            chash = content_hash(blocks, props)
            rendered.append({
                "concept": concept_with_status,
                "blocks": blocks,
                "properties": props,
                "content_hash": chash,
            })
        return rendered

    @step("Publish each Concept to Notion (create / update / skip)")
    def publish(self, ctx, render: list[dict]) -> dict:
        if ctx.mcp is None:
            return {"error": "no MCP gateway", "published": 0,
                    "skipped": 0, "errors": 0}
        params = ctx.params or {}
        database_id = params.get("notion_database_id")
        if not database_id:
            return {"error": "notion_database_id required", "published": 0,
                    "skipped": 0, "errors": 0}

        published = skipped = errors = adopted = 0
        details: list[dict] = []

        for item in render:
            concept = item["concept"]
            chash = item["content_hash"]
            name = concept["name"]
            now = _iso_now()
            publication_id = f"{name}:notion"

            try:
                existing = _lookup_local_publication(ctx, publication_id)
                if existing and existing.get("content_hash") == chash:
                    skipped += 1
                    details.append({"name": name, "action": "skip"})
                    continue

                if existing and existing.get("external_id"):
                    _notion_update(ctx, existing["external_id"], item)
                    _upsert_publication(ctx, publication_id,
                                        existing["external_id"], chash, now,
                                        existing.get("published_at") or now)
                    _wire_published_as(ctx, name, publication_id, now)
                    published += 1
                    details.append({"name": name, "action": "update",
                                    "external_id": existing["external_id"]})
                    continue

                # No local Publication — check Notion for an adoptable page.
                adoptable_id = _notion_lookup_by_concept_name(ctx, database_id, name)
                if adoptable_id:
                    _notion_update(ctx, adoptable_id, item)
                    _upsert_publication(ctx, publication_id, adoptable_id,
                                        chash, now, now)
                    _wire_published_as(ctx, name, publication_id, now)
                    adopted += 1
                    published += 1
                    details.append({"name": name, "action": "adopt",
                                    "external_id": adoptable_id})
                    continue

                # Truly new — create.
                created = _notion_create(ctx, database_id, item)
                external_id = (created or {}).get("id") or (created or {}).get("page_id")
                _upsert_publication(ctx, publication_id, external_id or "",
                                    chash, now, now)
                _wire_published_as(ctx, name, publication_id, now)
                published += 1
                details.append({"name": name, "action": "create",
                                "external_id": external_id})

            except Exception as exc:
                errors += 1
                details.append({"name": name, "action": "error",
                                "error": str(exc)})

        return {
            "published": published, "skipped": skipped,
            "adopted": adopted, "errors": errors,
            "details": details,
        }


# --- helpers -----------------------------------------------------------------


def _lookup_local_publication(ctx, publication_id: str) -> dict | None:
    if ctx.graph is None:
        return None
    rows = ctx.graph.execute(
        "MATCH (p:Publication {id: $pid, sink: 'notion'}) "
        "RETURN p.external_id AS external_id, p.content_hash AS content_hash, "
        "p.published_at AS published_at",
        {"pid": publication_id},
    )
    result_set = getattr(rows, "result_set", rows) or []
    for row in result_set:
        if isinstance(row, dict):
            return row
        external_id, chash, published_at = row
        return {"external_id": external_id, "content_hash": chash,
                "published_at": published_at}
    return None


def _upsert_publication(ctx, publication_id: str, external_id: str,
                        chash: str, now: str, published_at: str) -> None:
    if ctx.graph is None:
        return
    ctx.graph.execute(
        "MERGE (p:Publication {id: $pid, sink: 'notion'}) "
        "ON CREATE SET p.external_id=$eid, p.content_hash=$h, "
        "p.published_at=$pub, p.last_updated_at=$now "
        "ON MATCH SET p.external_id=$eid, p.content_hash=$h, "
        "p.last_updated_at=$now",
        {"pid": publication_id, "eid": external_id, "h": chash,
         "pub": published_at, "now": now},
    )


def _wire_published_as(ctx, concept_name: str, publication_id: str,
                       now: str) -> None:
    if ctx.graph is None:
        return
    ctx.graph.execute(
        "MATCH (c:Concept {name: $name}), (p:Publication {id: $pid}) "
        "MERGE (c)-[r:PUBLISHED_AS]->(p) "
        "ON CREATE SET r.first_published_at=$now, r.last_published_at=$now "
        "ON MATCH SET r.last_published_at=$now",
        {"name": concept_name, "pid": publication_id, "now": now},
    )


def _notion_lookup_by_concept_name(ctx, database_id: str, concept_name: str) -> str | None:
    resp = ctx.mcp.call_tool("notion.notion-query-data-sources", {
        "data_source_id": database_id,
        "filter": {
            "property": "concept_name",
            "rich_text": {"equals": concept_name},
        },
        "page_size": 1,
    })
    results = []
    if isinstance(resp, dict):
        results = resp.get("results") or resp.get("pages") or []
    elif isinstance(resp, list):
        results = resp
    if not results:
        return None
    first = results[0]
    if isinstance(first, dict):
        return first.get("id") or first.get("page_id")
    return None


def _notion_create(ctx, database_id: str, item: dict) -> dict:
    return ctx.mcp.call_tool("notion.notion-create-pages", {
        "parent": {"data_source_id": database_id},
        "properties": item["properties"],
        "children": item["blocks"],
    }) or {}


def _notion_update(ctx, page_id: str, item: dict) -> None:
    ctx.mcp.call_tool("notion.notion-update-page", {
        "page_id": page_id,
        "properties": item["properties"],
    })
    ctx.mcp.call_tool("notion.append-block-children", {
        "block_id": page_id,
        "children": item["blocks"],
    })
