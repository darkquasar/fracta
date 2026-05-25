"""Notion-MCP-compatible renderer, v3: three-database knowledge garden.

v3 architecture:
  - Sources DB    — one page per Readwise book (= source article / book / podcast).
  - Highlights DB — one page per Readwise highlight, with a `source` RELATION → Sources.
  - Concepts DB   — one page per extracted Concept, with a `highlights` RELATION → Highlights.

Concept body keeps the v2 "linked highlights" list (no inlined text).
Highlight body shows the full quote + attribution; the `source` RELATION
provides the clickable cross-link to the Source page.
Source page shows the source's metadata, optional cover URL, and the
user's document-level note if Readwise captured one.

Exported functions:
  - epistemic_status_for(confidence)
  - render_concept_blocks(concept, highlight_refs)         -> markdown
  - render_highlight_page(highlight)                       -> markdown
  - render_source_page(source)                             -> markdown        # NEW
  - page_properties(concept, status, last_updated_iso, highlight_urls)
  - highlight_page_properties(highlight, source_url=None)  -- source_url is the
                                                            Notion page URL of
                                                            the linked source.
  - source_page_properties(source)                         -> dict             # NEW
  - content_hash(content, properties)
"""

from __future__ import annotations

import hashlib
import json


def epistemic_status_for(confidence: float | None) -> str:
    if confidence is None:
        return "seedling"
    if confidence < 0.4:
        return "seedling"
    if confidence <= 0.8:
        return "budding"
    return "evergreen"


_STATUS_EMOJI = {"seedling": "🌱", "budding": "🌿", "evergreen": "🌳"}


def _truncate_words(text: str, max_words: int = 14) -> str:
    words = (text or "").split()
    if len(words) <= max_words:
        return text or ""
    return " ".join(words[:max_words]) + "…"


# ---------------- Concept ----------------


def render_concept_blocks(concept: dict, highlight_refs: list[dict]) -> str:
    name = concept.get("display_name") or concept.get("name") or "Untitled Concept"
    status = concept.get("epistemic_status") or epistemic_status_for(
        concept.get("confidence")
    )
    emoji = _STATUS_EMOJI.get(status, "🌱")

    parts: list[str] = []
    parts.append(f"# {name}")
    parts.append("")
    parts.append(
        f"> {emoji} **{status.capitalize()}** · "
        f"confidence {(concept.get('confidence') or 0.0):.2f} · "
        f"extraction_score {(concept.get('extraction_score') or 0.0):.2f} · "
        f"{concept.get('mention_count') or 0} mention(s)"
    )
    parts.append("")

    description = concept.get("description")
    if description:
        parts.append("## Definition")
        parts.append("")
        parts.append(str(description))
        parts.append("")

    if highlight_refs:
        parts.append("## Linked highlights")
        parts.append("")
        parts.append(
            "_See the **highlights** relation in the page properties for "
            "the clickable cross-links. Each highlight is stored once "
            "in the Highlights database (which itself links to the Source) "
            "and may underpin multiple concepts._"
        )
        parts.append("")
        for ref in highlight_refs:
            title = ref.get("title") or ref.get("readwise_highlight_id") or "(untitled)"
            book = ref.get("book_title")
            author = ref.get("author")
            line = f"- {title}"
            if book or author:
                attr_parts = [f"_{book}_" if book else None,
                              f"— {author}" if author else None]
                line = f"{line}  " + " ".join(p for p in attr_parts if p)
            parts.append(line)
        parts.append("")

    return "\n".join(parts).rstrip() + "\n"


def page_properties(
    concept: dict,
    status: str,
    last_updated_iso: str | None = None,
    highlight_urls: list[str] | None = None,
) -> dict:
    name = concept.get("display_name") or concept.get("name") or ""
    props: dict = {
        "Name": str(name),
        "concept_name": str(concept.get("name") or ""),
        "epistemic_status": status,
    }
    if concept.get("confidence") is not None:
        props["confidence"] = float(concept["confidence"])
    if concept.get("mention_count") is not None:
        props["mention_count"] = int(concept["mention_count"] or 0)
    if concept.get("extraction_score") is not None:
        props["extraction_score"] = float(concept["extraction_score"])
    if last_updated_iso:
        props["date:last_updated:start"] = last_updated_iso
    if highlight_urls:
        # RELATION values are JSON-stringified arrays of page URLs (Bug 23).
        props["highlights"] = json.dumps(highlight_urls)
    return props


# ---------------- Highlight ----------------


def render_highlight_page(highlight: dict) -> str:
    text = (highlight.get("text") or "").strip()
    note = (highlight.get("note") or "").strip()
    book = highlight.get("book_title") or ""
    author = highlight.get("author") or ""
    source_url = highlight.get("source_url") or ""
    highlighted_at = highlight.get("highlighted_at") or ""

    parts: list[str] = []
    parts.append("# Highlight")
    parts.append("")
    if text:
        for line in text.splitlines() or [text]:
            parts.append(f"> {line}")
        parts.append("")
    attr_parts: list[str] = []
    if book:
        attr_parts.append(f"[{book}]({source_url})" if source_url else f"_{book}_")
    if author:
        attr_parts.append(f"— {author}")
    if attr_parts:
        parts.append("**Original source:** " + " ".join(attr_parts))
        parts.append("")
    parts.append(
        "_See the **source** relation in the page properties for the "
        "canonical Source page in this workspace._"
    )
    parts.append("")
    if highlighted_at:
        parts.append(f"_Highlighted {highlighted_at[:10]}_")
        parts.append("")
    if note:
        parts.append("## Note")
        parts.append("")
        parts.append(note)
        parts.append("")
    return "\n".join(parts).rstrip() + "\n"


def highlight_page_properties(highlight: dict, source_url: str | None = None) -> dict:
    text = (highlight.get("text") or "").strip()
    title = _truncate_words(text, max_words=14) or (
        f"Highlight {highlight.get('readwise_highlight_id')}"
        if highlight.get("readwise_highlight_id") else "Untitled highlight"
    )
    props: dict = {
        "Name": title,
        "readwise_highlight_id": str(highlight.get("readwise_highlight_id") or ""),
    }
    if highlight.get("book_title"):
        props["book_title"] = str(highlight["book_title"])
    if highlight.get("author"):
        props["book_author"] = str(highlight["author"])
    if highlight.get("highlighted_at"):
        props["date:highlighted_at:start"] = str(highlight["highlighted_at"])[:10]
    if highlight.get("source_url"):
        # On the Highlights DB we have a plain `source_url` URL column for the
        # original article URL — distinct from the `source` RELATION which
        # points at the Source page inside this workspace.
        props["source_url"] = str(highlight["source_url"])
    if source_url:
        # RELATION column on Highlights → Sources (single page).
        props["source"] = json.dumps([source_url])
    return props


# ---------------- Source ----------------


def render_source_page(source: dict) -> str:
    """Render the body of a Source page. `source` keys:
        readwise_book_id, title, author, category, source_kind,
        source_url, cover_url, document_note.
    """
    title = source.get("title") or "Untitled source"
    author = source.get("author") or ""
    cat = source.get("category") or ""
    kind = source.get("source_kind") or ""
    cover_url = source.get("cover_url") or ""
    src_url = source.get("source_url") or ""
    note = (source.get("document_note") or "").strip()

    parts: list[str] = []
    parts.append(f"# {title}")
    parts.append("")
    if author:
        parts.append(f"**By {author}**")
        parts.append("")
    badge_parts: list[str] = []
    if cat:
        badge_parts.append(f"_{cat}_")
    if kind:
        badge_parts.append(f"via _{kind}_")
    if badge_parts:
        parts.append("> " + " · ".join(badge_parts))
        parts.append("")
    if cover_url:
        parts.append(f"![cover]({cover_url})")
        parts.append("")
    if src_url:
        parts.append(f"**Original URL:** [{src_url}]({src_url})")
        parts.append("")
    parts.append(
        "_See the linked highlights in the **Highlights** database "
        "(filter on `source = this page`) and the concepts that mention "
        "those highlights, by traversing the relations._"
    )
    parts.append("")
    if note:
        parts.append("## Document note")
        parts.append("")
        parts.append(note)
        parts.append("")
    return "\n".join(parts).rstrip() + "\n"


def source_page_properties(source: dict) -> dict:
    """Flat properties for one row in the Sources DB."""
    title = source.get("title") or "Untitled source"
    props: dict = {
        "Name": title,
        "readwise_book_id": str(source.get("readwise_book_id") or ""),
    }
    if source.get("author"):
        props["author"] = str(source["author"])
    if source.get("category"):
        props["category"] = str(source["category"])
    if source.get("source_kind"):
        props["source_kind"] = str(source["source_kind"])
    if source.get("source_url"):
        props["source_url"] = str(source["source_url"])
    if source.get("cover_url"):
        props["cover_url"] = str(source["cover_url"])
    if source.get("document_note"):
        # Notion rich_text fields have a soft length limit per block; keep
        # a reasonable cap to avoid 400s on enormous notes.
        props["document_note"] = str(source["document_note"])[:1900]
    return props


# ---------------- shared ----------------


def content_hash(content: str, properties: dict) -> str:
    payload = {"content": content, "properties": properties}
    serialised = json.dumps(payload, sort_keys=True, ensure_ascii=False)
    return hashlib.sha256(serialised.encode("utf-8")).hexdigest()
