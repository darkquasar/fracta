"""Pure merge of three concept-extractor outputs into ranked Candidate set.

Implements the asymmetric-base scoring algorithm from spec-41 §4.1.1
(`scoring-and-merge-algorithm-findings.md` §§3-9).

No graph I/O, no MCP calls. Side-effect-free so it is unit-testable from
fixtures (`strategies/tests/test_merge.py`).
"""

from dataclasses import dataclass, field
from typing import Optional


# --- Defaults (spec §9; configurable via binding extraction_config:) ---

KEYBERT_RANK_HIGH = 0.70
KEYBERT_RANK_MEDIUM = 0.50
KEYBERT_RANK_LOW = 0.30
SPACY_NER_PRIOR = 0.55
SPACY_NP_PRIOR = 0.30
AGREEMENT_BONUS = {1: 0.0, 2: 0.15, 3: 0.30}
TYPE_CONSISTENCY_BONUS = 0.05
WEAK_JOIN_PENALTY = 0.10
NP_ONLY_DISCARD = 0.35
COMMIT_THRESHOLD = 0.40
STAGE_THRESHOLD = 0.30
MMR_TOP_K = 8
MMR_LAMBDA = 0.7


# --- gliner/spacy label → coarse Entity kind ---

GLINER_ENTITY_LABELS = {
    "Person": "person",
    "Organisation": "org",
    "Organization": "org",
    "Place": "place",
    "Location": "place",
    "Work": "work",
    "Book": "work",
    "Paper": "work",
    "Product": "product",
}
GLINER_CONCEPT_LABELS = {"Theory", "Method", "Concept", "Tool"}

SPACY_NER_ENTITY_LABELS = {
    "PERSON": "person",
    "ORG": "org",
    "GPE": "place",
    "LOC": "place",
    "WORK_OF_ART": "work",
    "PRODUCT": "product",
}


# --- Input / output dataclasses (stable contract per spec §11) ---


@dataclass
class ExtractorHit:
    extractor_id: str                   # "keybert" | "gliner" | "spacy"
    source: str                         # "keyphrase" | "ner" | "noun_chunk"
    span: str
    label: Optional[str] = None         # gliner / spacy NER only
    score: Optional[float] = None       # gliner sigmoid OR keybert cosine; None for spacy
    rank: Optional[int] = None          # keybert only (1-indexed)
    start: Optional[int] = None
    end: Optional[int] = None
    root_pos: Optional[str] = None      # spacy noun_chunk only


@dataclass
class MergeInput:
    highlight_id: str
    highlight_text: str
    hits: list[ExtractorHit]


@dataclass
class Candidate:
    canon: str
    display: str
    route: str                          # "concept" | "entity" | "stage" | "discard"
    kind: Optional[str]
    extraction_score: float
    base_source: str                    # "gliner" | "keybert_rank" | "spacy_ner_prior" | "spacy_np_prior"
    base_value: float
    agreement_n: int
    extractors: list[str]
    typed_labels: list[tuple[str, str]]
    weak_join: bool
    mmr_discounted: bool
    offsets: list[tuple[int, int]] = field(default_factory=list)


# --- Helpers ---


def _canon(s: str) -> str:
    """Canonical key: lowercase, strip simple punctuation, collapse whitespace.

    Lemmatisation is not done here — the spec proposes spacy lemma as the
    source of truth, but the merge function is pure and does not call MCP.
    Operators wanting lemma-tighter merging can pre-process hits before
    calling merge_extractor_outputs.
    """
    out = []
    prev_space = False
    for ch in s.lower():
        if ch.isalnum() or ch == " ":
            out.append(ch)
            prev_space = ch == " "
        elif ch in "-_":
            out.append(" ")
            prev_space = True
        elif not prev_space:
            out.append(" ")
            prev_space = True
    return " ".join("".join(out).split())


def _tokens(canon: str) -> set:
    return set(canon.split())


def _jaccard(a: str, b: str) -> float:
    ta, tb = _tokens(a), _tokens(b)
    if not ta or not tb:
        return 0.0
    return len(ta & tb) / len(ta | tb)


def _overlap_ratio(a_start: int, a_end: int, b_start: int, b_end: int) -> float:
    """Fraction of the shorter span covered by the intersection."""
    inter = max(0, min(a_end, b_end) - max(a_start, b_start))
    if inter == 0:
        return 0.0
    shorter = min(a_end - a_start, b_end - b_start)
    return inter / shorter if shorter > 0 else 0.0


def _is_substring_join(a: str, b: str) -> bool:
    """Whole-word substring check for keybert ↔ existing cluster join."""
    ta, tb = _tokens(a), _tokens(b)
    return ta.issubset(tb) or tb.issubset(ta)


# --- Cluster representation ---


@dataclass
class _Cluster:
    canon: str
    display: str
    hits: list[ExtractorHit] = field(default_factory=list)
    extractors: set = field(default_factory=set)
    weak_join: bool = False

    def add(self, hit: ExtractorHit) -> None:
        self.hits.append(hit)
        self.extractors.add(hit.extractor_id)
        # Prefer most-titlecase surface form for display.
        if sum(c.isupper() for c in hit.span) > sum(c.isupper() for c in self.display):
            self.display = hit.span

    def offsets(self) -> list[tuple[int, int]]:
        return [
            (h.start, h.end) for h in self.hits
            if h.start is not None and h.end is not None
        ]


# --- Clustering (spec §3.2) ---


def _filter_pre_merge(hits: list[ExtractorHit]) -> list[ExtractorHit]:
    """Drop spacy noun chunks with root_pos == 'PRON' (pronouns)."""
    out = []
    for h in hits:
        if h.extractor_id == "spacy" and h.source == "noun_chunk" and h.root_pos == "PRON":
            continue
        out.append(h)
    return out


def _cluster_hits(hits: list[ExtractorHit]) -> list[_Cluster]:
    """Group hits into clusters.

    Order of precedence (spec §3.2):
      1. Offset overlap (gliner ↔ spacy, both have offsets).
      2. Canonical-key match (keybert joins by canon).
      3. Substring fallback (keybert-only ↔ cluster); flags weak_join.
    """
    clusters: list[_Cluster] = []

    # Pass 1: gliner + spacy by offset overlap, then by canon.
    offset_hits = [h for h in hits if h.extractor_id in ("gliner", "spacy")
                   and h.start is not None and h.end is not None]
    for h in offset_hits:
        canon_h = _canon(h.span)
        # Try offset overlap with existing clusters.
        attached = None
        for c in clusters:
            for hh in c.hits:
                if hh.start is None or hh.end is None:
                    continue
                if _overlap_ratio(h.start, h.end, hh.start, hh.end) >= 0.8:
                    attached = c
                    break
            if attached:
                break
        if attached is None:
            # Try canon match too.
            for c in clusters:
                if c.canon == canon_h:
                    attached = c
                    break
        if attached is None:
            attached = _Cluster(canon=canon_h, display=h.span)
            clusters.append(attached)
        attached.add(h)

    # Pass 2: keybert join by canon-match, then substring fallback.
    keybert_hits = [h for h in hits if h.extractor_id == "keybert"]
    for h in keybert_hits:
        canon_h = _canon(h.span)
        attached = None
        # Strict canon match first.
        for c in clusters:
            if c.canon == canon_h:
                attached = c
                break
        # Substring fallback.
        if attached is None:
            for c in clusters:
                if _is_substring_join(canon_h, c.canon):
                    attached = c
                    attached.weak_join = True
                    break
        if attached is None:
            attached = _Cluster(canon=canon_h, display=h.span)
            clusters.append(attached)
        attached.add(h)

    return clusters


# --- Scoring (spec §4) ---


def _typed_labels_agree(cluster: _Cluster) -> bool:
    """True iff every typed contribution maps to the same coarse kind."""
    kinds = set()
    for h in cluster.hits:
        if h.extractor_id == "gliner" and h.label in GLINER_ENTITY_LABELS:
            kinds.add(GLINER_ENTITY_LABELS[h.label])
        elif h.extractor_id == "spacy" and h.source == "ner" and h.label in SPACY_NER_ENTITY_LABELS:
            kinds.add(SPACY_NER_ENTITY_LABELS[h.label])
    return len(kinds) == 1


def _base_score(cluster: _Cluster) -> tuple[float, str]:
    """Return (base_value, base_source) per the asymmetric-base rules."""
    if "gliner" in cluster.extractors:
        gliner_scores = [h.score for h in cluster.hits
                         if h.extractor_id == "gliner" and h.score is not None]
        if gliner_scores:
            return max(gliner_scores), "gliner"
    if "keybert" in cluster.extractors:
        ranks = [h.rank for h in cluster.hits
                 if h.extractor_id == "keybert" and h.rank is not None]
        rank = min(ranks) if ranks else None
        if rank is not None:
            if rank <= 3:
                return KEYBERT_RANK_HIGH, "keybert_rank"
            if rank <= 10:
                return KEYBERT_RANK_MEDIUM, "keybert_rank"
            return KEYBERT_RANK_LOW, "keybert_rank"
        # No rank info — treat as medium tier (defensive).
        return KEYBERT_RANK_MEDIUM, "keybert_rank"
    # spacy-only.
    has_ner = any(h.source == "ner" for h in cluster.hits if h.extractor_id == "spacy")
    if has_ner:
        return SPACY_NER_PRIOR, "spacy_ner_prior"
    return SPACY_NP_PRIOR, "spacy_np_prior"


def _extraction_score(cluster: _Cluster) -> tuple[float, float, str, int]:
    """Return (extraction_score, base_value, base_source, agreement_n)."""
    base, base_source = _base_score(cluster)
    n = len(cluster.extractors)
    bonus_agree = AGREEMENT_BONUS.get(n, 0.0)
    bonus_type = TYPE_CONSISTENCY_BONUS if _typed_labels_agree(cluster) else 0.0
    penalty_weak = WEAK_JOIN_PENALTY if cluster.weak_join else 0.0
    score = max(0.0, min(1.0, base + bonus_agree + bonus_type - penalty_weak))
    return score, base, base_source, n


# --- Routing (spec §5) ---


def _route(cluster: _Cluster, score: float) -> tuple[str, Optional[str]]:
    """Return (route, kind). route ∈ {concept, entity, stage, discard}."""
    # Gliner-first typing.
    if "gliner" in cluster.extractors:
        for h in cluster.hits:
            if h.extractor_id == "gliner" and h.label in GLINER_ENTITY_LABELS:
                return "entity", GLINER_ENTITY_LABELS[h.label]
            if h.extractor_id == "gliner" and h.label in GLINER_CONCEPT_LABELS:
                return "concept", None
    # Spacy NER fallback typing.
    if "spacy" in cluster.extractors:
        for h in cluster.hits:
            if (h.extractor_id == "spacy" and h.source == "ner"
                    and h.label in SPACY_NER_ENTITY_LABELS):
                return "entity", SPACY_NER_ENTITY_LABELS[h.label]
    # Spacy-only noun-chunk path: discard if below NP_ONLY_DISCARD.
    if cluster.extractors == {"spacy"}:
        is_np_only = all(h.source == "noun_chunk" for h in cluster.hits)
        if is_np_only and score < NP_ONLY_DISCARD:
            return "discard", None
    # Default Concept route; threshold-driven stage/discard decided downstream.
    return "concept", None


# --- MMR diversification (spec §6) ---


def _mmr_select(
    scored: list[tuple[_Cluster, float]],
    k: int = MMR_TOP_K,
    lambda_: float = MMR_LAMBDA,
) -> tuple[list[_Cluster], set]:
    """Return (selected_in_rank_order, discounted_canons).

    discounted_canons holds canons that were beaten by a kept neighbour with
    Jaccard similarity > 0; they still pass through but get mmr_discounted=True.
    """
    pool = sorted(scored, key=lambda x: x[1], reverse=True)
    selected: list[_Cluster] = []
    discounted: set = set()
    while pool and len(selected) < k:
        best_idx = 0
        best_mmr = float("-inf")
        for i, (c, s) in enumerate(pool):
            sim = max(
                (_jaccard(c.canon, sc.canon) for sc in selected),
                default=0.0,
            )
            mmr = lambda_ * s - (1 - lambda_) * sim
            if mmr > best_mmr:
                best_mmr = mmr
                best_idx = i
        pick, _ = pool.pop(best_idx)
        # Flag the pick itself if it overlaps with anything already kept —
        # marks it as MMR-discounted-but-still-kept for observability.
        if any(_jaccard(pick.canon, sc.canon) > 0 for sc in selected):
            discounted.add(pick.canon)
        # Also flag any remaining pool members overlapping with selected.
        for c, _ in pool:
            if any(_jaccard(c.canon, sc.canon) > 0 for sc in selected):
                discounted.add(c.canon)
        selected.append(pick)
    return selected, discounted


# --- Public entry point ---


def merge_extractor_outputs(inp: MergeInput) -> list[Candidate]:
    """Merge three extractors' hits into a ranked list of Candidates.

    Per-Highlight pipeline:
      1. Pre-merge filter (drop PRON noun-chunks).
      2. Cluster hits (offset → canon → substring).
      3. Score each cluster (asymmetric base + bonuses).
      4. Route each cluster (entity / concept / discard via gliner-then-spacy typing).
      5. MMR diversification (top-K with Jaccard similarity).
      6. Apply commit/stage/discard thresholds on the final score.
    """
    filtered = _filter_pre_merge(inp.hits)
    clusters = _cluster_hits(filtered)

    scored: list[tuple[_Cluster, float, float, str, int, str, Optional[str]]] = []
    for c in clusters:
        score, base_value, base_source, n = _extraction_score(c)
        route, kind = _route(c, score)
        scored.append((c, score, base_value, base_source, n, route, kind))

    # MMR on scored clusters that are not pre-routed to discard.
    keepable = [(c, s) for c, s, _, _, _, route, _ in scored if route != "discard"]
    _, discounted = _mmr_select(keepable, k=MMR_TOP_K, lambda_=MMR_LAMBDA)

    out: list[Candidate] = []
    for c, score, base_value, base_source, n, route, kind in scored:
        if route == "discard":
            final_route = "discard"
        elif score >= COMMIT_THRESHOLD:
            final_route = "entity" if route == "entity" else "concept"
        elif score >= STAGE_THRESHOLD:
            final_route = "stage"
        else:
            final_route = "discard"

        typed_labels = [
            (h.extractor_id, h.label) for h in c.hits if h.label is not None
        ]
        out.append(Candidate(
            canon=c.canon,
            display=c.display,
            route=final_route,
            kind=kind if final_route == "entity" else None,
            extraction_score=score,
            base_source=base_source,
            base_value=base_value,
            agreement_n=n,
            extractors=sorted(c.extractors),
            typed_labels=typed_labels,
            weak_join=c.weak_join,
            mmr_discounted=c.canon in discounted,
            offsets=c.offsets(),
        ))

    # Sort by score descending for deterministic output ordering.
    out.sort(key=lambda x: x.extraction_score, reverse=True)
    return out
