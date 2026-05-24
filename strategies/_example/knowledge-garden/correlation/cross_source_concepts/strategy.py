"""Score concepts from graph signal; update Concept.confidence / mention_count.

Spec-41 §4.2. Three-step DAG, graph-only (no MCP, no DuckDB reads). Pure
graph→graph computation:

  1. load_concepts — read each Concept's mention_count, last_seen_at, and
     per-MENTIONS extraction_score plus the count of distinct DomainSources.
  2. score          — confidence = sigmoid(weighted sum of frequency,
     recency, source-diversity, and mean extraction_score).
  3. update_graph   — write Concept.confidence, Concept.mention_count,
     Concept.last_updated; derive MENTIONS.weight = extraction_score ×
     recency_factor on each edge.

Ownership-seam invariant: this strategy NEVER writes
`Concept.extraction_score` (that field is owned by highlight_distill).
Reviewers will check this. Cypher in update_graph is explicitly SET on
confidence / mention_count / last_updated only.
"""

from __future__ import annotations

import datetime as _dt
import math


# Default weights (spec §4.2). Sum to 1.0.
W_FREQ = 0.30
W_RECENCY = 0.15
W_DIVERSITY = 0.40
W_EXTRACT = 0.15

# Recency: half-life in days for exp decay. ~90 days = a season.
RECENCY_HALF_LIFE_DAYS = 90.0


from fracta_strategies import Strategy, step


def _sigmoid(x: float) -> float:
    if x >= 0:
        z = math.exp(-x)
        return 1.0 / (1.0 + z)
    z = math.exp(x)
    return z / (1.0 + z)


def _parse_iso(ts: str | None) -> _dt.datetime | None:
    if not ts:
        return None
    try:
        if ts.endswith("Z"):
            ts = ts[:-1] + "+00:00"
        return _dt.datetime.fromisoformat(ts)
    except (TypeError, ValueError):
        return None


def _recency_decay(last_seen: _dt.datetime | None, now: _dt.datetime) -> float:
    if last_seen is None:
        return 0.0
    delta_days = max(0.0, (now - last_seen).total_seconds() / 86400.0)
    return 0.5 ** (delta_days / RECENCY_HALF_LIFE_DAYS)


class CrossSourceConcepts(Strategy):

    @step("Load per-Concept aggregates from the graph")
    def load_concepts(self, ctx) -> list[dict]:
        if ctx.graph is None:
            return []
        cypher = (
            "MATCH (c:Concept) "
            "OPTIONAL MATCH (c)<-[m:MENTIONS]-(src) "
            "OPTIONAL MATCH (src)-[:CAPTURED_FROM]->(ds:DomainSource) "
            "RETURN c.name AS name, c.last_seen_at AS last_seen_at, "
            "count(m) AS mention_count, "
            "avg(m.extraction_score) AS mean_extraction, "
            "count(DISTINCT ds) AS domain_source_count"
        )
        rows = ctx.graph.execute(cypher)
        out = []
        # FalkorDB returns rows as list of lists; normalise defensively.
        result_set = getattr(rows, "result_set", rows)
        for row in result_set or []:
            if isinstance(row, dict):
                out.append({
                    "name": row.get("name"),
                    "last_seen_at": row.get("last_seen_at"),
                    "mention_count": int(row.get("mention_count") or 0),
                    "mean_extraction": float(row.get("mean_extraction") or 0.0),
                    "domain_source_count": int(row.get("domain_source_count") or 0),
                })
            else:
                name, last_seen, mc, mean_ex, dsc = row
                out.append({
                    "name": name,
                    "last_seen_at": last_seen,
                    "mention_count": int(mc or 0),
                    "mean_extraction": float(mean_ex or 0.0),
                    "domain_source_count": int(dsc or 0),
                })
        return out

    @step("Score concepts with graph-aware confidence formula")
    def score(self, ctx, load_concepts: list[dict]) -> list[dict]:
        now = _dt.datetime.now(_dt.timezone.utc)
        scored = []
        for c in load_concepts:
            recency_factor = _recency_decay(_parse_iso(c["last_seen_at"]), now)
            x = (
                W_FREQ * math.log1p(c["mention_count"])
                + W_RECENCY * recency_factor
                + W_DIVERSITY * c["domain_source_count"]
                + W_EXTRACT * c["mean_extraction"]
            )
            scored.append({
                **c,
                "confidence": _sigmoid(x),
                "recency_factor": recency_factor,
            })
        return scored

    @step("Write Concept.confidence and MENTIONS.weight back to the graph")
    def update_graph(self, ctx, score: list[dict]) -> dict:
        if ctx.graph is None:
            return {"updated": 0, "error": "no graph configured"}
        now_iso = _dt.datetime.now(_dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        updated = 0
        for row in score:
            # Update Concept fields. NOTE: deliberately does NOT touch
            # c.extraction_score — that field belongs to highlight_distill.
            ctx.graph.execute(
                "MATCH (c:Concept {name: $name}) "
                "SET c.confidence = $conf, "
                "    c.mention_count = $mc, "
                "    c.last_updated = $now",
                {"name": row["name"], "conf": row["confidence"],
                 "mc": row["mention_count"], "now": now_iso},
            )
            # Derive MENTIONS.weight = extraction_score × recency_factor.
            ctx.graph.execute(
                "MATCH (c:Concept {name: $name})<-[m:MENTIONS]-() "
                "WHERE m.extraction_score IS NOT NULL "
                "SET m.weight = m.extraction_score * $rec",
                {"name": row["name"], "rec": row["recency_factor"]},
            )
            updated += 1
        return {
            "updated": updated,
            "weights": {
                "freq": W_FREQ, "recency": W_RECENCY,
                "diversity": W_DIVERSITY, "extract": W_EXTRACT,
            },
            "ownership_invariant": "Concept.extraction_score not written here",
        }
