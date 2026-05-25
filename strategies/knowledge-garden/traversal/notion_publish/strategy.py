"""Publish Sources, Highlights, and Concepts to Notion (3-database v3).

v3 architecture (vs v2's two-database highlight/concept):
  - Adds a third Notion database for Sources (one page per Readwise book).
  - Highlights gain a `source` RELATION pointing at the matching Source page,
    so each Highlight has a one-click breadcrumb back to its origin article.
  - Concepts unchanged structurally — still have a `highlights` RELATION; the
    Source breadcrumb is reachable via Concept → Highlight → Source.

Pipeline (single run):
  1. load_target_concepts
  2. load_supporting_highlights        (now includes book_id + book_title etc.)
  3. load_sources                      (graph: distinct Documents reachable from
                                         the Highlights we're about to publish)
  4. publish_sources                   (Notion + graph upsert; source_url_by_book_id)
  5. publish_highlights                (with source RELATION property)
  6. render_concepts
  7. publish_concepts                  (with highlights RELATION property)

Required params (v3 three-DB architecture):
  - notion_concepts_database_id      (Concepts DB, RELATION -> Highlights DB)
  - notion_highlights_database_id    (Highlights DB, RELATION -> Sources DB)
  - notion_sources_database_id       (Sources DB; one page per Readwise book)

Legacy alias: `notion_database_id` is accepted as a fallback for
`notion_concepts_database_id` to ease pre-v3 operator migration.
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
    highlight_page_properties,
    page_properties,
    render_concept_blocks,
    render_highlight_page,
    render_source_page,
    source_page_properties,
)


def _iso_now() -> str:
    return _dt.datetime.now(_dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _iso_date_today() -> str:
    return _dt.datetime.now(_dt.timezone.utc).strftime("%Y-%m-%d")


def _coerce_data_source_id(raw: str) -> str:
    raw = (raw or "").strip()
    if raw.startswith("collection://"):
        return raw[len("collection://"):]
    return raw


def _data_source_url(raw: str) -> str:
    return f"collection://{_coerce_data_source_id(raw)}"


def _strip_readwise_prefix(node_id: str) -> str:
    """Graph IDs look like 'readwise:1018941958' or 'readwise:book:60113420'.
    Float-coerced forms like 'readwise:1.018941958e+09' also occur (Bug 9)."""
    if not node_id:
        return ""
    parts = node_id.split(":")
    bare = parts[-1] if parts else node_id
    if "e+" in bare:
        try:
            return str(int(float(bare)))
        except (ValueError, OverflowError):
            return bare
    return bare


class NotionPublish(Strategy):

    # --------------------- step 1 ---------------------
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
            rows = ctx.graph.query(cypher, {"name": single})
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
            rows = ctx.graph.query(cypher, {"n": batch_size})

        rs = getattr(rows, "result_set", rows) or []
        out: list[dict] = []
        for row in rs:
            if isinstance(row, dict):
                out.append(row)
            else:
                (n, dn, desc, conf, es, mc, ep) = row
                out.append({"name": n, "display_name": dn, "description": desc,
                            "confidence": conf, "extraction_score": es,
                            "mention_count": mc, "epistemic_status": ep})
        return out

    # --------------------- step 2 ---------------------
    @step("Load supporting Highlights for each Concept (with book_id)")
    def load_supporting_highlights(self, ctx, load_target_concepts: list[dict]) -> dict[str, list[dict]]:
        if ctx.graph is None:
            return {}
        out: dict[str, list[dict]] = {}
        for concept in load_target_concepts:
            cypher = (
                "MATCH (c:Concept {name: $name})<-[:MENTIONS]-(h:Highlight) "
                "OPTIONAL MATCH (h)-[:PART_OF]->(d:Document) "
                "RETURN h.id AS graph_id, h.text AS text, h.note AS note, "
                "h.highlighted_at AS highlighted_at, "
                "d.id AS document_graph_id, d.title AS book_title, "
                "d.author AS author, d.url AS source_url "
                "LIMIT 20"
            )
            rows = ctx.graph.query(cypher, {"name": concept["name"]})
            rs = getattr(rows, "result_set", rows) or []
            highlights: list[dict] = []
            for row in rs:
                if isinstance(row, dict):
                    hl = row
                else:
                    (gid, text, note, ha, doc_gid, title, author, src) = row
                    hl = {"graph_id": gid, "text": text, "note": note,
                          "highlighted_at": ha, "document_graph_id": doc_gid,
                          "book_title": title, "author": author,
                          "source_url": src}
                hl["readwise_highlight_id"] = _strip_readwise_prefix(hl.get("graph_id") or "")
                hl["readwise_book_id"] = _strip_readwise_prefix(hl.get("document_graph_id") or "")
                highlights.append(hl)
            out[concept["name"]] = highlights
        return out

    # --------------------- step 3 ---------------------
    @step("Load Source records (distinct Documents) reachable from these Concepts")
    def load_sources(self, ctx, load_supporting_highlights: dict[str, list[dict]]) -> list[dict]:
        if ctx.graph is None:
            return []
        # Reuse the document_graph_id captured in load_supporting_highlights
        # directly — avoids round-tripping through int(float()) which loses
        # precision (Bug 9) and stops the rebuilt id from matching the
        # actually-stored Document.id (the float-coerced form).
        seen_graph_ids: set = set()
        for hl_list in load_supporting_highlights.values():
            for h in hl_list:
                gid = h.get("document_graph_id")
                if gid:
                    seen_graph_ids.add(gid)
        if not seen_graph_ids:
            return []
        graph_ids = list(seen_graph_ids)
        rows = ctx.graph.query(
            "MATCH (d:Document) WHERE d.id IN $ids "
            "RETURN d.id AS graph_id, d.title AS title, d.author AS author, "
            "d.category AS category, d.source_kind AS source_kind, "
            "d.url AS source_url, d.cover_url AS cover_url, "
            "d.document_note AS document_note",
            {"ids": graph_ids},
        )
        rs = getattr(rows, "result_set", rows) or []
        out: list[dict] = []
        for row in rs:
            if isinstance(row, dict):
                s = row
            else:
                (gid, title, author, cat, sk, src, cov, dn) = row
                s = {"graph_id": gid, "title": title, "author": author,
                     "category": cat, "source_kind": sk, "source_url": src,
                     "cover_url": cov, "document_note": dn}
            s["readwise_book_id"] = _strip_readwise_prefix(s.get("graph_id") or "")
            out.append(s)
        return out

    # --------------------- step 4 ---------------------
    @step("Publish Source pages to Notion (idempotent per readwise_book_id)")
    def publish_sources(self, ctx, load_sources: list[dict]) -> dict[str, Any]:
        if ctx.mcp is None or not load_sources:
            return {"id_to_url": {}, "count": 0, "details": []}
        params = ctx.params or {}
        s_db_raw = params.get("notion_sources_database_id")
        if not s_db_raw:
            return {"id_to_url": {}, "count": 0, "details": [],
                    "warning": "notion_sources_database_id not provided"}
        s_db_id = _coerce_data_source_id(s_db_raw)

        id_to_url: dict[str, str] = {}
        details: list[dict] = []
        now = _iso_now()

        for s in load_sources:
            rid = s.get("readwise_book_id")
            if not rid:
                continue
            content_md = render_source_page(s)
            props = source_page_properties(s)
            chash = content_hash(content_md, props)
            pub_id = f"source:{rid}:notion"
            try:
                existing = _lookup_publication(ctx, pub_id, "notion:source")
                if existing and existing.get("content_hash") == chash:
                    id_to_url[rid] = _url_from_id(existing.get("external_id"))
                    details.append({"book_id": rid, "action": "skip"})
                    continue
                if existing and existing.get("external_id"):
                    _notion_update_body(ctx, existing["external_id"], content_md)
                    _notion_update_props(ctx, existing["external_id"], props)
                    id_to_url[rid] = _url_from_id(existing["external_id"])
                    _upsert_publication(ctx, pub_id, "notion:source",
                                        existing["external_id"], chash, now,
                                        existing.get("published_at") or now)
                    details.append({"book_id": rid, "action": "update"})
                    continue
                created = _notion_create(ctx, s_db_id, props, content_md)
                ext_id = _first_page_id(created)
                if ext_id:
                    id_to_url[rid] = _url_from_id(ext_id)
                _upsert_publication(ctx, pub_id, "notion:source",
                                    ext_id or "", chash, now, now)
                details.append({"book_id": rid, "action": "create",
                                "external_id": ext_id})
            except Exception as exc:
                details.append({"book_id": rid, "action": "error",
                                "error": str(exc)})

        return {"id_to_url": id_to_url, "count": len(id_to_url),
                "details": details}

    # --------------------- step 5 ---------------------
    @step("Publish Highlight pages to Notion (with source RELATION)")
    def publish_highlights(
        self, ctx,
        load_supporting_highlights: dict[str, list[dict]],
        publish_sources: dict[str, Any],
    ) -> dict[str, Any]:
        if ctx.mcp is None:
            return {"id_to_url": {}, "count": 0, "details": []}
        params = ctx.params or {}
        h_db_raw = params.get("notion_highlights_database_id")
        if not h_db_raw:
            return {"id_to_url": {}, "count": 0, "details": [],
                    "warning": "notion_highlights_database_id not provided"}
        h_db_id = _coerce_data_source_id(h_db_raw)
        source_id_to_url = (publish_sources or {}).get("id_to_url", {})

        # De-dup across concepts
        seen: dict[str, dict] = {}
        for hl_list in load_supporting_highlights.values():
            for h in hl_list:
                rid = h.get("readwise_highlight_id")
                if not rid:
                    continue
                seen.setdefault(rid, h)

        id_to_url: dict[str, str] = {}
        details: list[dict] = []
        now = _iso_now()

        for rid, hl in seen.items():
            source_url = source_id_to_url.get(hl.get("readwise_book_id"))
            content_md = render_highlight_page(hl)
            props = highlight_page_properties(hl, source_url=source_url)
            chash = content_hash(content_md, props)
            pub_id = f"highlight:{rid}:notion"
            try:
                existing = _lookup_publication(ctx, pub_id, "notion:highlight")
                if existing and existing.get("content_hash") == chash:
                    id_to_url[rid] = _url_from_id(existing.get("external_id"))
                    details.append({"highlight_id": rid, "action": "skip"})
                    continue
                if existing and existing.get("external_id"):
                    _notion_update_body(ctx, existing["external_id"], content_md)
                    _notion_update_props(ctx, existing["external_id"], props)
                    id_to_url[rid] = _url_from_id(existing["external_id"])
                    _upsert_publication(ctx, pub_id, "notion:highlight",
                                        existing["external_id"], chash, now,
                                        existing.get("published_at") or now)
                    details.append({"highlight_id": rid, "action": "update"})
                    continue
                created = _notion_create(ctx, h_db_id, props, content_md)
                ext_id = _first_page_id(created)
                if ext_id:
                    id_to_url[rid] = _url_from_id(ext_id)
                _upsert_publication(ctx, pub_id, "notion:highlight",
                                    ext_id or "", chash, now, now)
                details.append({"highlight_id": rid, "action": "create",
                                "external_id": ext_id})
            except Exception as exc:
                details.append({"highlight_id": rid, "action": "error",
                                "error": str(exc)})

        return {"id_to_url": id_to_url, "count": len(id_to_url),
                "details": details}

    # --------------------- step 6 ---------------------
    @step("Write epistemic_status to graph and render Concept payloads")
    def render_concepts(
        self, ctx,
        load_target_concepts: list[dict],
        load_supporting_highlights: dict[str, list[dict]],
        publish_highlights: dict[str, Any],
    ) -> list[dict]:
        id_to_url = (publish_highlights or {}).get("id_to_url", {})
        rendered: list[dict] = []
        today = _iso_date_today()
        for concept in load_target_concepts:
            status = epistemic_status_for(concept.get("confidence"))
            if ctx.graph is not None:
                ctx.graph.query(
                    "MATCH (c:Concept {name: $name}) "
                    "SET c.epistemic_status = $status",
                    {"name": concept["name"], "status": status},
                )
            cws = {**concept, "epistemic_status": status}
            hls = load_supporting_highlights.get(concept["name"], [])
            highlight_refs = []
            highlight_urls = []
            for h in hls:
                rid = h.get("readwise_highlight_id")
                url = id_to_url.get(rid) if rid else None
                if url:
                    highlight_urls.append(url)
                highlight_refs.append({
                    "readwise_highlight_id": rid,
                    "title": (h.get("text") or "")[:120],
                    "book_title": h.get("book_title"),
                    "author": h.get("author"),
                })
            content_md = render_concept_blocks(cws, highlight_refs)
            props = page_properties(cws, status, last_updated_iso=today,
                                    highlight_urls=highlight_urls)
            chash = content_hash(content_md, props)
            rendered.append({"concept": cws, "content": content_md,
                             "properties": props, "content_hash": chash})
        return rendered

    # --------------------- step 7 ---------------------
    @step("Publish Concept pages to Notion (create / update / skip / adopt)")
    def publish_concepts(self, ctx, render_concepts: list[dict]) -> dict:
        if ctx.mcp is None:
            return {"error": "no MCP gateway", "published": 0,
                    "skipped": 0, "errors": 0, "adopted": 0}
        params = ctx.params or {}
        # v3 param name is notion_concepts_database_id; accept the legacy
        # notion_database_id for back-compat with pre-v3 operator scripts.
        c_db_raw = params.get("notion_concepts_database_id") or params.get("notion_database_id")
        if not c_db_raw:
            return {"error": "notion_concepts_database_id required", "published": 0,
                    "skipped": 0, "errors": 0, "adopted": 0}
        c_db_id = _coerce_data_source_id(c_db_raw)
        c_ds_url = _data_source_url(c_db_raw)

        published = skipped = errors = adopted = 0
        details: list[dict] = []

        for item in render_concepts:
            concept = item["concept"]
            chash = item["content_hash"]
            name = concept["name"]
            now = _iso_now()
            pub_id = f"{name}:notion"
            try:
                existing = _lookup_publication(ctx, pub_id, "notion:concept")
                if existing and existing.get("content_hash") == chash:
                    skipped += 1
                    details.append({"name": name, "action": "skip"})
                    continue
                if existing and existing.get("external_id"):
                    _notion_update_body(ctx, existing["external_id"], item["content"])
                    _notion_update_props(ctx, existing["external_id"], item["properties"])
                    _upsert_publication(ctx, pub_id, "notion:concept",
                                        existing["external_id"], chash, now,
                                        existing.get("published_at") or now)
                    _wire_published_as(ctx, name, pub_id, now)
                    published += 1
                    details.append({"name": name, "action": "update"})
                    continue
                adoptable = _strict_lookup(ctx, c_ds_url, name)
                if adoptable:
                    _notion_update_body(ctx, adoptable, item["content"])
                    _notion_update_props(ctx, adoptable, item["properties"])
                    _upsert_publication(ctx, pub_id, "notion:concept",
                                        adoptable, chash, now, now)
                    _wire_published_as(ctx, name, pub_id, now)
                    adopted += 1
                    published += 1
                    details.append({"name": name, "action": "adopt",
                                    "external_id": adoptable})
                    continue
                created = _notion_create(ctx, c_db_id, item["properties"], item["content"])
                ext_id = _first_page_id(created)
                _upsert_publication(ctx, pub_id, "notion:concept",
                                    ext_id or "", chash, now, now)
                _wire_published_as(ctx, name, pub_id, now)
                published += 1
                details.append({"name": name, "action": "create",
                                "external_id": ext_id})
            except Exception as exc:
                errors += 1
                details.append({"name": name, "action": "error",
                                "error": str(exc)})

        return {"published": published, "skipped": skipped,
                "adopted": adopted, "errors": errors,
                "details": details}


# ---------------- graph helpers ----------------


def _lookup_publication(ctx, pub_id: str, sink: str) -> dict | None:
    if ctx.graph is None:
        return None
    rows = ctx.graph.query(
        "MATCH (p:Publication {id: $pid, sink: $sink}) "
        "RETURN p.external_id AS external_id, p.content_hash AS content_hash, "
        "p.published_at AS published_at",
        {"pid": pub_id, "sink": sink},
    )
    rs = getattr(rows, "result_set", rows) or []
    for row in rs:
        if isinstance(row, dict):
            return row
        eid, ch, pa = row
        return {"external_id": eid, "content_hash": ch, "published_at": pa}
    return None


def _upsert_publication(ctx, pub_id: str, sink: str, external_id: str,
                        chash: str, now: str, published_at: str) -> None:
    if ctx.graph is None:
        return
    ctx.graph.query(
        "MERGE (p:Publication {id: $pid, sink: $sink}) "
        "ON CREATE SET p.external_id=$eid, p.content_hash=$h, "
        "p.published_at=$pub, p.last_updated_at=$now "
        "ON MATCH SET p.external_id=$eid, p.content_hash=$h, "
        "p.last_updated_at=$now",
        {"pid": pub_id, "sink": sink, "eid": external_id, "h": chash,
         "pub": published_at, "now": now},
    )


def _wire_published_as(ctx, concept_name: str, pub_id: str, now: str) -> None:
    if ctx.graph is None:
        return
    ctx.graph.query(
        "MATCH (c:Concept {name: $name}), (p:Publication {id: $pid}) "
        "MERGE (c)-[r:PUBLISHED_AS]->(p) "
        "ON CREATE SET r.first_published_at=$now, r.last_published_at=$now "
        "ON MATCH SET r.last_published_at=$now",
        {"name": concept_name, "pid": pub_id, "now": now},
    )


# ---------------- notion helpers ----------------


def _first_page_id(resp: Any) -> str | None:
    if not isinstance(resp, dict):
        return None
    pages = resp.get("pages") or []
    if pages and isinstance(pages[0], dict):
        return pages[0].get("id") or pages[0].get("page_id")
    return resp.get("id") or resp.get("page_id")


def _url_from_id(page_id: str | None) -> str | None:
    if not page_id:
        return None
    bare = page_id.replace("-", "")
    return f"https://www.notion.so/{bare}"


def _strict_lookup(ctx, data_source_url: str, name: str) -> str | None:
    resp = ctx.mcp.call_tool("notion.notion-search", {
        "query": name, "query_type": "internal",
        "data_source_url": data_source_url, "page_size": 5,
    })
    results: list = []
    if isinstance(resp, dict):
        results = resp.get("results") or resp.get("pages") or []
    elif isinstance(resp, list):
        results = resp
    for hit in results:
        if not isinstance(hit, dict):
            continue
        candidate = hit.get("id") or hit.get("page_id") or hit.get("url")
        if not candidate:
            continue
        try:
            fetched = ctx.mcp.call_tool("notion.notion-fetch", {"id": candidate})
        except Exception:
            continue
        props = _extract_props(fetched)
        np = str(props.get("Name") or props.get("title") or "").strip()
        cn = str(props.get("concept_name") or "").strip()
        if np == name or cn == name:
            return candidate
    return None


def _extract_props(fetched: object) -> dict:
    import json as _json
    import re as _re
    if isinstance(fetched, dict):
        if isinstance(fetched.get("properties"), dict):
            return fetched["properties"]
        text = fetched.get("text") or fetched.get("content") or ""
        if isinstance(text, str):
            m = _re.search(r"<properties>\s*(\{.*?\})\s*</properties>", text, _re.S)
            if m:
                try:
                    return _json.loads(m.group(1))
                except Exception:
                    return {}
    return {}


def _notion_create(ctx, data_source_id: str, properties: dict, content: str) -> dict:
    return ctx.mcp.call_tool("notion.notion-create-pages", {
        "parent": {"data_source_id": data_source_id},
        "pages": [{"properties": properties, "content": content}],
    }) or {}


def _notion_update_body(ctx, page_id: str, content: str) -> None:
    ctx.mcp.call_tool("notion.notion-update-page", {
        "page_id": page_id, "command": "replace_content", "new_str": content,
    })


def _notion_update_props(ctx, page_id: str, properties: dict) -> None:
    ctx.mcp.call_tool("notion.notion-update-page", {
        "page_id": page_id, "command": "update_properties", "properties": properties,
    })
