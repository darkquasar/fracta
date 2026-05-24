"""Pure Notion-block-JSON renderer + content-hash helper.

No graph I/O, no MCP calls — testable from a fixture. The strategy module
(`strategy.py`) feeds this with a Concept row and supporting Highlights
pulled from the graph; this module returns the block-children payload
plus the SHA-256 content_hash used for idempotent re-publish detection.
"""

from __future__ import annotations

import hashlib
import json
from typing import Any


def epistemic_status_for(confidence: float | None) -> str:
    """Map Concept.confidence to seedling / budding / evergreen (spec §3.2)."""
    if confidence is None:
        return "seedling"
    if confidence < 0.4:
        return "seedling"
    if confidence <= 0.8:
        return "budding"
    return "evergreen"


def _rich_text(content: str) -> list[dict]:
    return [{"type": "text", "text": {"content": content}}]


def _heading_1(text: str) -> dict:
    return {"object": "block", "type": "heading_1",
            "heading_1": {"rich_text": _rich_text(text)}}


def _heading_2(text: str) -> dict:
    return {"object": "block", "type": "heading_2",
            "heading_2": {"rich_text": _rich_text(text)}}


def _paragraph(text: str) -> dict:
    return {"object": "block", "type": "paragraph",
            "paragraph": {"rich_text": _rich_text(text)}}


def _quote(text: str) -> dict:
    return {"object": "block", "type": "quote",
            "quote": {"rich_text": _rich_text(text)}}


def _callout(text: str, emoji: str = "🌱") -> dict:
    # Notion accepts emoji as plain string in `icon.emoji`.
    return {"object": "block", "type": "callout",
            "callout": {
                "rich_text": _rich_text(text),
                "icon": {"type": "emoji", "emoji": emoji},
            }}


_STATUS_EMOJI = {"seedling": "🌱", "budding": "🌿", "evergreen": "🌳"}


def render_concept_blocks(concept: dict, highlights: list[dict]) -> list[dict]:
    """Render a Concept page body as a list of Notion block children.

    `concept` keys consumed: name, display_name, description, confidence,
    extraction_score, mention_count, epistemic_status.

    `highlights` is a list of dicts: {text, note, book_title, author}.
    """
    status = concept.get("epistemic_status") or epistemic_status_for(
        concept.get("confidence"))
    emoji = _STATUS_EMOJI.get(status, "🌱")

    blocks: list[dict] = []
    blocks.append(_callout(
        f"Epistemic status: {status} (confidence "
        f"{(concept.get('confidence') or 0.0):.2f}, "
        f"extraction_score {(concept.get('extraction_score') or 0.0):.2f}, "
        f"{concept.get('mention_count') or 0} mention(s))",
        emoji=emoji,
    ))

    description = concept.get("description")
    if description:
        blocks.append(_heading_2("Definition"))
        blocks.append(_paragraph(str(description)))

    if highlights:
        blocks.append(_heading_2("Supporting highlights"))
        for h in highlights:
            blocks.append(_quote(str(h.get("text") or "")))
            attribution_parts = []
            if h.get("book_title"):
                attribution_parts.append(str(h["book_title"]))
            if h.get("author"):
                attribution_parts.append(f"— {h['author']}")
            if attribution_parts:
                blocks.append(_paragraph(" ".join(attribution_parts)))
            if h.get("note"):
                blocks.append(_paragraph(f"Note: {h['note']}"))

    return blocks


def content_hash(blocks: list[dict], properties: dict) -> str:
    """SHA-256 of the deterministically-serialised page payload.

    Includes both block children and the writable page properties so that
    a change in epistemic_status (driven by confidence drift) triggers an
    update even if the body blocks are identical.
    """
    payload = {"blocks": blocks, "properties": properties}
    serialised = json.dumps(payload, sort_keys=True, ensure_ascii=False)
    return hashlib.sha256(serialised.encode("utf-8")).hexdigest()


def page_properties(concept: dict, status: str) -> dict:
    """Notion page-properties dict (writable subset)."""
    name = concept.get("display_name") or concept.get("name") or ""
    return {
        "Name": {"title": _rich_text(name)},
        "concept_name": {"rich_text": _rich_text(str(concept.get("name") or ""))},
        "epistemic_status": {"select": {"name": status}},
    }
