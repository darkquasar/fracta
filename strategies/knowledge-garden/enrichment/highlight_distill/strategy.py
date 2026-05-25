"""Distill Readwise highlights into Highlight/Document/Concept/Entity nodes.

Spec-41 §4.1. Six-step DAG:
  1. load_highlights  — DuckDB (pre-staged via Readwise binding)
  2. load_documents   — DuckDB
  3. extract_concepts — fan-out 3 MCP extractors per highlight (parallel)
  4. merge_concepts   — pure merge function (merge.py)
  5. write_graph      — branch on Candidate.route; MERGE nodes + edges
  6. update_watermark — write max(updated_at) to DuckDB

Extractor MCP calls go through ctx.mcp.call_tool(...) directly. No actions:
framework extension — dropped from spec-41 scope.

Ownership seam: this strategy writes Concept.extraction_score (rolling max),
Entity.extraction_score, MENTIONS.{weight,extracted_by,extraction_score,agreement_n}.
It does NOT write Concept.confidence — that belongs to cross_source_concepts.
"""

from __future__ import annotations

import concurrent.futures
import datetime as _dt
import json
import os
import re
import sys
from typing import Any

from fracta_strategies import Strategy, step

# strategy.py is loaded as a top-level module by runner.py
# (importlib.util.spec_from_file_location with no package), so relative
# imports do not work. Insert the strategy directory and import merge as
# a sibling module.
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from merge import (  # noqa: E402
    Candidate,
    ExtractorHit,
    MergeInput,
    merge_extractor_outputs,
)


# v1 gliner taxonomy (spec §4.1.1 + scoring-findings §5).
GLINER_LABELS = [
    "Person", "Organisation", "Place", "Work", "Product",
    "Theory", "Method", "Concept", "Tool",
]
GLINER_THRESHOLD = 0.30
KEYBERT_TOP_N = 15
KEYBERT_DIVERSITY = 0.7
MAX_INPUT_TOKENS = 256
CHUNK_WARN_TOKENS = 240
EXTRACTOR_TIMEOUT_S = 30
FANOUT_MAX_WORKERS = 8


def _approx_tokens(text: str) -> int:
    return len(text.split())


def _iso_now() -> str:
    return _dt.datetime.now(_dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


_RELATIVE_WATERMARK = re.compile(r"^-(\d+)([dh])$")


def _resolve_watermark(ctx, *, default_relative: str = "-7d") -> str:
    raw = ctx.params.get("watermark_iso") if hasattr(ctx, "params") else None
    if not raw:
        raw = _watermark_from_table(ctx) or default_relative
    m = _RELATIVE_WATERMARK.match(raw.strip())
    if not m:
        return raw
    n = int(m.group(1))
    unit = m.group(2)
    delta = _dt.timedelta(days=n) if unit == "d" else _dt.timedelta(hours=n)
    return (_dt.datetime.now(_dt.timezone.utc) - delta).strftime("%Y-%m-%dT%H:%M:%SZ")


def _watermark_from_table(ctx) -> str | None:
    try:
        row = ctx.duckdb.execute(
            "SELECT watermark FROM _watermark WHERE strategy = 'highlight-distill'"
        ).fetchone()
    except Exception:
        return None
    return row[0] if row else None


class HighlightDistill(Strategy):

    # --- step 1 ---
    @step("Load recent highlights from staged DuckDB table")
    def load_highlights(self, ctx) -> list[dict]:
        watermark = _resolve_watermark(ctx)
        rows = ctx.duckdb.execute(
            "SELECT highlight_id, book_id, book_title, author, "
            "book_category, book_source_kind, book_source_url, book_cover_url, "
            "book_document_note, text, note, tags, highlighted_at, updated_at "
            "FROM recent_highlights WHERE updated_at > ?",
            [watermark],
        ).fetchall()
        cols = ["highlight_id", "book_id", "book_title", "author",
                "book_category", "book_source_kind", "book_source_url",
                "book_cover_url", "book_document_note",
                "text", "note", "tags", "highlighted_at", "updated_at"]
        return [dict(zip(cols, r)) for r in rows]

    # --- step 2 ---
    @step("Load recent documents from staged DuckDB table")
    def load_documents(self, ctx) -> list[dict]:
        watermark = _resolve_watermark(ctx)
        rows = ctx.duckdb.execute(
            "SELECT document_id, title, author, url, location, updated_at "
            "FROM recent_documents WHERE updated_at > ?",
            [watermark],
        ).fetchall()
        cols = ["document_id", "title", "author", "url", "location", "updated_at"]
        return [dict(zip(cols, r)) for r in rows]

    # --- step 3 ---
    @step("Fan out three MCP extractors per highlight in parallel")
    def extract_concepts(self, ctx, load_highlights: list[dict]) -> list[dict]:
        if not load_highlights:
            return []
        if ctx.mcp is None:
            return [{"highlight_id": h["highlight_id"],
                     "error": "no MCP gateway available"} for h in load_highlights]

        def _call_one(highlight: dict) -> dict:
            text = (highlight.get("text") or "") + "\n" + (highlight.get("note") or "")
            if _approx_tokens(text) > CHUNK_WARN_TOKENS:
                # No chunking in v1; first-window extraction is still useful.
                pass

            keybert_resp: Any = None
            gliner_resp: Any = None
            spacy_resp: Any = None
            errors: dict[str, str] = {}

            try:
                keybert_resp = ctx.mcp.call_tool("concept-keybert.keybert_extract_tool", {
                    "text": text,
                    "top_n": KEYBERT_TOP_N,
                    "use_mmr": True,
                    "diversity": KEYBERT_DIVERSITY,
                })
            except Exception as exc:
                errors["keybert"] = str(exc)
            try:
                gliner_resp = ctx.mcp.call_tool("concept-gliner.gliner_extract_tool", {
                    "text": text,
                    "labels": GLINER_LABELS,
                    "threshold": GLINER_THRESHOLD,
                })
            except Exception as exc:
                errors["gliner"] = str(exc)
            try:
                spacy_resp = ctx.mcp.call_tool("concept-spacy.spacy_extract_tool", {
                    "text": text,
                })
            except Exception as exc:
                errors["spacy"] = str(exc)

            return {
                "highlight_id": highlight["highlight_id"],
                "text": text,
                "keybert": keybert_resp,
                "gliner": gliner_resp,
                "spacy": spacy_resp,
                "errors": errors,
            }

        total_budget_s = 180  # wall-clock cap for the whole fan-out
        with concurrent.futures.ThreadPoolExecutor(max_workers=FANOUT_MAX_WORKERS) as pool:
            futures = {pool.submit(_call_one, h): h for h in load_highlights}
            results = []
            try:
                for fut in concurrent.futures.as_completed(futures, timeout=total_budget_s):
                    try:
                        results.append(fut.result(timeout=EXTRACTOR_TIMEOUT_S))
                    except Exception as exc:
                        h = futures[fut]
                        results.append({
                            "highlight_id": h["highlight_id"],
                            "errors": {"fanout": str(exc)},
                        })
            except concurrent.futures.TimeoutError:
                # Wall-clock budget elapsed; record remaining highlights as timed
                # out and move on. Running threads can't be killed in Python.
                for fut, h in futures.items():
                    if not fut.done():
                        results.append({
                            "highlight_id": h["highlight_id"],
                            "errors": {"fanout": "wall-clock timeout"},
                        })
                        fut.cancel()
        return results

    # --- step 4 ---
    @step("Merge extractor outputs per highlight into Candidate list")
    def merge_concepts(self, ctx, extract_concepts: list[dict]) -> list[dict]:
        out = []
        for bundle in extract_concepts:
            hits = _hits_from_responses(bundle)
            merge_input = MergeInput(
                highlight_id=bundle["highlight_id"],
                highlight_text=bundle.get("text", ""),
                hits=hits,
            )
            candidates = merge_extractor_outputs(merge_input)
            out.append({
                "highlight_id": bundle["highlight_id"],
                "candidates": [_candidate_to_dict(c) for c in candidates],
            })
        return out

    # --- step 5 ---
    @step("Write Highlight/Document/Concept/Entity nodes and MENTIONS edges")
    def write_graph(self, ctx, load_highlights: list[dict],
                    load_documents: list[dict],
                    merge_concepts: list[dict]) -> dict:
        if ctx.graph is None:
            return {"error": "no graph configured"}

        # MERGE the DomainSource / DataStore / QUERYABLE_VIA chain (CLAUDE.md §3.6).
        ctx.graph.query(
            "MERGE (d:DomainSource {name: 'Readwise Highlights'}) "
            "ON CREATE SET d._source = 'strategy:highlight_distill' "
            "MERGE (ds:DataStore {uri: 'fracta-mcp-gateway://readwise/'}) "
            "ON CREATE SET ds._source = 'strategy:highlight_distill' "
            "MERGE (d)-[:STORED_IN]->(ds) "
            "WITH ds "
            "MATCH (ms:MCPServer {config_key: 'readwise'}) "
            "MERGE (ds)-[:QUERYABLE_VIA]->(ms)"
        )

        highlights_by_id = {h["highlight_id"]: h for h in load_highlights}
        now = _iso_now()
        counts = {"documents": 0, "highlights": 0, "concepts": 0,
                  "entities": 0, "mentions": 0, "staged": 0, "discarded": 0}

        # v3: Derive Sources/Documents from the highlights themselves.
        # Each distinct book_id becomes one Document node, populated with
        # the denormalised book_* fields available on every highlight.
        # This resolves Bug 13 (the Reader-documents namespace mismatch).
        sources_by_id: dict = {}
        for h in load_highlights:
            bid = h.get("book_id")
            if not bid:
                continue
            if bid in sources_by_id:
                continue
            sources_by_id[bid] = {
                "book_id": bid,
                "title": h.get("book_title") or "",
                "author": h.get("author") or "",
                "category": h.get("book_category") or "",
                "source_kind": h.get("book_source_kind") or "",
                "source_url": h.get("book_source_url") or "",
                "cover_url": h.get("book_cover_url") or "",
                "document_note": h.get("book_document_note") or "",
            }

        # MERGE Document nodes per Readwise book.
        for bid, s in sources_by_id.items():
            doc_id = f"readwise:book:{bid}"
            ctx.graph.query(
                "MERGE (doc:Document {id: $id}) "
                "ON CREATE SET doc.title=$title, doc.author=$author, "
                "doc.url=$url, doc.cover_url=$cover, doc.category=$cat, "
                "doc.source_kind=$sk, doc.document_note=$note, "
                "doc.captured_at=$now "
                "ON MATCH SET doc.title=$title, doc.author=$author, "
                "doc.url=$url, doc.cover_url=$cover, doc.category=$cat, "
                "doc.source_kind=$sk, doc.document_note=$note "
                "WITH doc "
                "MATCH (src:DomainSource {name: 'Readwise Highlights'}) "
                "MERGE (doc)-[r:CAPTURED_FROM]->(src) "
                "ON CREATE SET r.captured_at = $now",
                {"id": doc_id, "title": s["title"], "author": s["author"],
                 "url": s["source_url"], "cover": s["cover_url"],
                 "cat": s["category"], "sk": s["source_kind"],
                 "note": s["document_note"], "now": now},
            )
            counts["documents"] += 1

        for bundle in merge_concepts:
            hl = highlights_by_id.get(bundle["highlight_id"])
            if hl is None:
                continue
            hl_id = f"readwise:{hl['highlight_id']}"
            # MERGE Highlight + CAPTURED_FROM + PART_OF Document.
            ctx.graph.query(
                "MERGE (h:Highlight {id: $id}) "
                "ON CREATE SET h.text=$text, h.note=$note, h.location=$loc, "
                "h.highlighted_at=$ha "
                "WITH h "
                "MATCH (src:DomainSource {name: 'Readwise Highlights'}) "
                "MERGE (h)-[r:CAPTURED_FROM]->(src) "
                "ON CREATE SET r.captured_at = $now",
                {"id": hl_id, "text": hl.get("text") or "",
                 "note": hl.get("note") or "",
                 "loc": "", "ha": hl.get("highlighted_at") or "", "now": now},
            )
            if hl.get("book_id"):
                doc_id = f"readwise:book:{hl['book_id']}"
                ctx.graph.query(
                    "MATCH (h:Highlight {id: $hl}), (d:Document {id: $doc}) "
                    "MERGE (h)-[:PART_OF]->(d)",
                    {"hl": hl_id, "doc": doc_id},
                )
            counts["highlights"] += 1

            for cand in bundle["candidates"]:
                route = cand["route"]
                if route == "discard":
                    counts["discarded"] += 1
                    continue
                if route == "stage":
                    ctx.duckdb.execute(
                        "CREATE TABLE IF NOT EXISTS pending_extractions ("
                        "highlight_id VARCHAR, candidate_name VARCHAR, "
                        "extraction_score DOUBLE, agreement_n INTEGER, "
                        "extractor_evidence VARCHAR, first_seen_at VARCHAR)"
                    )
                    ctx.duckdb.execute(
                        "INSERT INTO pending_extractions VALUES (?, ?, ?, ?, ?, ?)",
                        [hl["highlight_id"], cand["canon"], cand["extraction_score"],
                         cand["agreement_n"], json.dumps(cand), now],
                    )
                    counts["staged"] += 1
                    continue

                extracted_by = "|".join(cand["extractors"])
                if route == "entity":
                    ctx.graph.query(
                        "MERGE (e:Entity {name: $name}) "
                        "ON CREATE SET e.kind=$kind, e.extraction_score=$score "
                        "ON MATCH SET e.extraction_score = "
                        "CASE WHEN e.extraction_score IS NULL OR $score > e.extraction_score "
                        "THEN $score ELSE e.extraction_score END "
                        "WITH e "
                        "MATCH (h:Highlight {id: $hl}) "
                        "MERGE (h)-[m:MENTIONS]->(e) "
                        "ON CREATE SET m.extracted_by=$by, m.extraction_score=$score, "
                        "m.agreement_n=$n",
                        {"name": cand["canon"], "kind": cand["kind"] or "",
                         "score": cand["extraction_score"], "hl": hl_id,
                         "by": extracted_by, "n": cand["agreement_n"]},
                    )
                    counts["entities"] += 1
                else:  # concept
                    ctx.graph.query(
                        "MERGE (c:Concept {name: $name}) "
                        "ON CREATE SET c.display_name=$disp, "
                        "c.extraction_score=$score, c.first_seen_at=$now, "
                        "c.last_seen_at=$now "
                        "ON MATCH SET c.last_seen_at=$now, "
                        "c.extraction_score = "
                        "CASE WHEN c.extraction_score IS NULL OR $score > c.extraction_score "
                        "THEN $score ELSE c.extraction_score END "
                        "WITH c "
                        "MATCH (h:Highlight {id: $hl}) "
                        "MERGE (h)-[m:MENTIONS]->(c) "
                        "ON CREATE SET m.extracted_by=$by, m.extraction_score=$score, "
                        "m.agreement_n=$n",
                        {"name": cand["canon"], "disp": cand["display"],
                         "score": cand["extraction_score"], "now": now,
                         "hl": hl_id, "by": extracted_by, "n": cand["agreement_n"]},
                    )
                    counts["concepts"] += 1
                counts["mentions"] += 1

        return counts

    # --- step 6 ---
    @step("Update watermark to max(updated_at) for next-run delta")
    def update_watermark(self, ctx, load_highlights: list[dict],
                         write_graph: dict) -> dict:
        if not load_highlights:
            return {"watermark": ctx.params.get("watermark_iso"), "counts": write_graph}
        max_ts = max((h.get("updated_at") or "") for h in load_highlights)
        ctx.duckdb.execute(
            "CREATE TABLE IF NOT EXISTS _watermark "
            "(strategy VARCHAR, watermark VARCHAR)"
        )
        ctx.duckdb.execute(
            "DELETE FROM _watermark WHERE strategy = 'highlight-distill'"
        )
        ctx.duckdb.execute(
            "INSERT INTO _watermark VALUES (?, ?)",
            ["highlight-distill", max_ts],
        )
        return {"watermark": max_ts, "counts": write_graph}


# --- Helpers translating MCP responses into ExtractorHit lists ---


def _hits_from_responses(bundle: dict) -> list[ExtractorHit]:
    hits: list[ExtractorHit] = []
    hits.extend(_keybert_hits(bundle.get("keybert")))
    hits.extend(_gliner_hits(bundle.get("gliner")))
    hits.extend(_spacy_hits(bundle.get("spacy")))
    return hits


def _keybert_hits(resp) -> list[ExtractorHit]:
    if not resp:
        return []
    items = resp.get("keyphrases") if isinstance(resp, dict) else resp
    if not isinstance(items, list):
        return []
    out = []
    for idx, item in enumerate(items, start=1):
        if isinstance(item, dict):
            span = item.get("phrase") or item.get("keyphrase") or item.get("text")
            score = item.get("score")
        elif isinstance(item, (list, tuple)) and len(item) >= 2:
            span, score = item[0], item[1]
        else:
            continue
        if not span:
            continue
        out.append(ExtractorHit(
            extractor_id="keybert", source="keyphrase",
            span=str(span), score=float(score) if score is not None else None,
            rank=idx,
        ))
    return out


def _gliner_hits(resp) -> list[ExtractorHit]:
    if not resp:
        return []
    items = resp.get("entities") if isinstance(resp, dict) else resp
    if not isinstance(items, list):
        return []
    out = []
    for item in items:
        if not isinstance(item, dict):
            continue
        span = item.get("text") or item.get("span")
        if not span:
            continue
        out.append(ExtractorHit(
            extractor_id="gliner", source="ner",
            span=str(span),
            label=item.get("label"),
            score=float(item["score"]) if item.get("score") is not None else None,
            start=item.get("start"),
            end=item.get("end"),
        ))
    return out


def _spacy_hits(resp) -> list[ExtractorHit]:
    if not resp or not isinstance(resp, dict):
        return []
    out = []
    for ent in resp.get("entities", []) or []:
        if not isinstance(ent, dict):
            continue
        span = ent.get("text") or ent.get("span")
        if not span:
            continue
        out.append(ExtractorHit(
            extractor_id="spacy", source="ner",
            span=str(span),
            label=ent.get("label"),
            start=ent.get("start"),
            end=ent.get("end"),
        ))
    for chunk in resp.get("noun_chunks", []) or []:
        if not isinstance(chunk, dict):
            continue
        span = chunk.get("text") or chunk.get("span")
        if not span:
            continue
        out.append(ExtractorHit(
            extractor_id="spacy", source="noun_chunk",
            span=str(span),
            start=chunk.get("start"),
            end=chunk.get("end"),
            root_pos=chunk.get("root_pos"),
        ))
    return out


def _candidate_to_dict(c: Candidate) -> dict:
    return {
        "canon": c.canon,
        "display": c.display,
        "route": c.route,
        "kind": c.kind,
        "extraction_score": c.extraction_score,
        "base_source": c.base_source,
        "base_value": c.base_value,
        "agreement_n": c.agreement_n,
        "extractors": c.extractors,
        "typed_labels": [list(t) for t in c.typed_labels],
        "weak_join": c.weak_join,
        "mmr_discounted": c.mmr_discounted,
        "offsets": [list(o) for o in c.offsets],
    }
