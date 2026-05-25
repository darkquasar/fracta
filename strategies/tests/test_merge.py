"""Unit tests for merge_extractor_outputs (highlight_distill).

Exercises:
- gliner-only candidate → score from sigmoid
- keybert-only candidate → rank tier (HIGH=0.70/MEDIUM=0.50/LOW=0.30)
- spacy-only candidate → noun-chunk prior (0.30) / NER prior (0.55)
- 3-extractor agreement → +0.30 bonus
- 2-extractor agreement → +0.15 bonus
- weak_substring_join penalty (-0.10)
- commit / stage / discard thresholds
- PRON noun-chunk filter (pre-merge)
- Route classification (entity / concept / stage / discard)
- Ownership-seam invariant on returned Candidate shape
"""

import importlib.util
import math
import os
import sys

import pytest


_HERE = os.path.dirname(os.path.abspath(__file__))
_MERGE_PATH = os.path.join(
    _HERE, "..", "knowledge-garden",
    "enrichment", "highlight_distill", "merge.py",
)


def _load_merge_module():
    spec = importlib.util.spec_from_file_location("kg_merge", _MERGE_PATH)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


@pytest.fixture(scope="module")
def merge_mod():
    return _load_merge_module()


def _hit(merge_mod, extractor_id, source, span, **kw):
    return merge_mod.ExtractorHit(
        extractor_id=extractor_id, source=source, span=span, **kw,
    )


def _by_canon(cands):
    return {c.canon: c for c in cands}


# --- (a) gliner-only candidate ---------------------------------------------


def test_gliner_only_uses_sigmoid_base(merge_mod):
    inp = merge_mod.MergeInput(
        highlight_id="hl-1",
        highlight_text="Karl Popper said something.",
        hits=[_hit(merge_mod, "gliner", "ner", "Karl Popper",
                   label="Person", score=0.91, start=0, end=11)],
    )
    cands = merge_mod.merge_extractor_outputs(inp)
    by = _by_canon(cands)
    assert "karl popper" in by
    c = by["karl popper"]
    # base = 0.91 (gliner), n=1 → no agreement bonus,
    # typed_labels_agree trivially true for single gliner Person → +0.05
    assert c.base_source == "gliner"
    assert c.base_value == pytest.approx(0.91)
    assert c.extraction_score == pytest.approx(0.91 + 0.05)
    assert c.route == "entity"
    assert c.kind == "person"
    assert c.agreement_n == 1


# --- (b) keybert-only candidate, rank tiers --------------------------------


@pytest.mark.parametrize("rank,expected_base", [
    (1, 0.70), (3, 0.70),               # HIGH
    (4, 0.50), (10, 0.50),              # MEDIUM
    (11, 0.30), (20, 0.30),             # LOW
])
def test_keybert_only_rank_tiers(merge_mod, rank, expected_base):
    inp = merge_mod.MergeInput(
        highlight_id="hl-2",
        highlight_text="some text",
        hits=[_hit(merge_mod, "keybert", "keyphrase", "phrase",
                   score=0.6, rank=rank)],
    )
    cands = merge_mod.merge_extractor_outputs(inp)
    c = _by_canon(cands)["phrase"]
    assert c.base_source == "keybert_rank"
    assert c.base_value == pytest.approx(expected_base)
    assert c.extraction_score == pytest.approx(expected_base)
    assert c.agreement_n == 1


# --- (c) spacy-only: NER vs noun-chunk priors ------------------------------


def test_spacy_ner_only_uses_ner_prior(merge_mod):
    inp = merge_mod.MergeInput(
        highlight_id="hl-3", highlight_text="Apple released a product.",
        hits=[_hit(merge_mod, "spacy", "ner", "Apple",
                   label="ORG", start=0, end=5)],
    )
    cands = merge_mod.merge_extractor_outputs(inp)
    c = _by_canon(cands)["apple"]
    assert c.base_source == "spacy_ner_prior"
    assert c.base_value == pytest.approx(0.55)
    # typed_labels_agree → +0.05 (single ORG)
    assert c.extraction_score == pytest.approx(0.55 + 0.05)
    assert c.route == "entity"
    assert c.kind == "org"


def test_spacy_np_only_uses_np_prior_and_stages(merge_mod):
    inp = merge_mod.MergeInput(
        highlight_id="hl-4", highlight_text="An interesting machine.",
        hits=[_hit(merge_mod, "spacy", "noun_chunk", "interesting machine",
                   start=3, end=22, root_pos="NOUN")],
    )
    cands = merge_mod.merge_extractor_outputs(inp)
    c = _by_canon(cands)["interesting machine"]
    assert c.base_source == "spacy_np_prior"
    assert c.base_value == pytest.approx(0.30)
    assert c.extraction_score == pytest.approx(0.30)
    # 0.30 ≤ score < 0.40 → stage; but NP_ONLY_DISCARD = 0.35, so
    # score=0.30 < 0.35 → routed to discard at routing time before threshold logic.
    # Verify final route reflects discard from the NP_ONLY_DISCARD path:
    assert c.route == "discard"


def test_pron_noun_chunk_filtered_pre_merge(merge_mod):
    inp = merge_mod.MergeInput(
        highlight_id="hl-pron", highlight_text="It works.",
        hits=[_hit(merge_mod, "spacy", "noun_chunk", "It",
                   start=0, end=2, root_pos="PRON")],
    )
    cands = merge_mod.merge_extractor_outputs(inp)
    # The pronoun should be filtered before clustering; no cluster produced.
    assert "it" not in _by_canon(cands)


# --- (d) all-three agreement ------------------------------------------------


def test_three_extractor_agreement_bonus(merge_mod):
    inp = merge_mod.MergeInput(
        highlight_id="hl-5",
        highlight_text="Karl Popper wrote about falsifiability.",
        hits=[
            _hit(merge_mod, "gliner", "ner", "Karl Popper",
                 label="Person", score=0.94, start=0, end=11),
            _hit(merge_mod, "spacy", "ner", "Karl Popper",
                 label="PERSON", start=0, end=11),
            _hit(merge_mod, "keybert", "keyphrase", "karl popper",
                 score=0.55, rank=3),
        ],
    )
    cands = merge_mod.merge_extractor_outputs(inp)
    c = _by_canon(cands)["karl popper"]
    assert set(c.extractors) == {"gliner", "spacy", "keybert"}
    assert c.agreement_n == 3
    # base = 0.94 (gliner) + 0.30 (agreement) + 0.05 (type consistency) → clip(1.29) = 1.0
    assert c.extraction_score == pytest.approx(1.0)
    assert c.route == "entity"
    assert c.kind == "person"


def test_two_extractor_agreement_bonus(merge_mod):
    inp = merge_mod.MergeInput(
        highlight_id="hl-6",
        highlight_text="A method called MMR.",
        hits=[
            _hit(merge_mod, "gliner", "ner", "MMR",
                 label="Method", score=0.42, start=16, end=19),
            _hit(merge_mod, "spacy", "noun_chunk", "MMR",
                 start=16, end=19, root_pos="PROPN"),
        ],
    )
    cands = merge_mod.merge_extractor_outputs(inp)
    c = _by_canon(cands)["mmr"]
    assert c.agreement_n == 2
    # base 0.42 (gliner) + 0.15 (n=2) = 0.57; no type-consistency bonus
    # because gliner Method maps to no Entity kind, leaving the kinds-set
    # empty (typed_labels_agree requires exactly one coarse kind).
    assert c.extraction_score == pytest.approx(0.57)
    assert c.route == "concept"  # gliner Method → Concept


# --- (e) weak_substring_join penalty ---------------------------------------


def test_weak_join_penalty(merge_mod):
    """Keybert short-form joins existing gliner cluster via substring; -0.10."""
    inp = merge_mod.MergeInput(
        highlight_id="hl-7",
        highlight_text="Principle of falsifiability matters; the principle is...",
        hits=[
            _hit(merge_mod, "gliner", "ner", "principle of falsifiability",
                 label="Theory", score=0.80, start=0, end=27),
            _hit(merge_mod, "keybert", "keyphrase", "principle",
                 score=0.40, rank=2),
        ],
    )
    cands = merge_mod.merge_extractor_outputs(inp)
    # The keybert "principle" should attach to the gliner cluster
    # via substring fallback, flagging weak_join=True.
    c = _by_canon(cands).get("principle of falsifiability")
    assert c is not None
    assert c.weak_join is True
    # base 0.80 (gliner Theory) + 0.15 (n=2) - 0.10 (weak join) = 0.85.
    # No type-consistency bonus: gliner Theory maps to no Entity kind.
    assert c.extraction_score == pytest.approx(0.85)


# --- (f) threshold routing --------------------------------------------------


def test_commit_threshold_routes_to_concept_or_entity(merge_mod):
    inp = merge_mod.MergeInput(
        highlight_id="hl-cmt",
        highlight_text="text",
        hits=[_hit(merge_mod, "gliner", "ner", "ConceptX",
                   label="Concept", score=0.50, start=0, end=8)],
    )
    cands = merge_mod.merge_extractor_outputs(inp)
    c = _by_canon(cands)["conceptx"]
    # 0.50 + 0.05 = 0.55 ≥ 0.40 → concept
    assert c.extraction_score >= merge_mod.COMMIT_THRESHOLD
    assert c.route == "concept"


def test_stage_band(merge_mod):
    """0.30 ≤ score < 0.40 routes to stage (keybert rank>10 = LOW = 0.30)."""
    # keybert LOW tier is exactly 0.30, so use a 2-extractor with a slightly
    # below-threshold combination: keybert MEDIUM 0.50 alone is > 0.40, so
    # instead use a gliner-only with a score that lands in the stage band.
    inp = merge_mod.MergeInput(
        highlight_id="hl-stage",
        highlight_text="x",
        hits=[_hit(merge_mod, "gliner", "ner", "FringeIdea",
                   label="Concept", score=0.30, start=0, end=10)],
    )
    cands = merge_mod.merge_extractor_outputs(inp)
    c = _by_canon(cands)["fringeidea"]
    # 0.30 + 0.05 (type consistency, trivially) = 0.35
    # 0.35 in [0.30, 0.40) → stage
    assert merge_mod.STAGE_THRESHOLD <= c.extraction_score < merge_mod.COMMIT_THRESHOLD
    assert c.route == "stage"


def test_discard_below_stage_threshold(merge_mod):
    """Score below STAGE_THRESHOLD (and not NP-only-discard) → discard.

    keybert rank > 10 → LOW tier 0.30 → equals STAGE_THRESHOLD → stage.
    To land strictly below, push it down with weak_join.
    """
    inp = merge_mod.MergeInput(
        highlight_id="hl-discard",
        highlight_text="garbage text here",
        hits=[
            _hit(merge_mod, "gliner", "ner", "garbage text",
                 label="Concept", score=0.20, start=0, end=12),
            _hit(merge_mod, "keybert", "keyphrase", "garbage",
                 score=0.10, rank=20),
        ],
    )
    cands = merge_mod.merge_extractor_outputs(inp)
    # gliner cluster keeps base 0.20; keybert "garbage" should substring-join
    # via "garbage" ⊂ "garbage text" → weak_join True
    # 0.20 + 0.15 (n=2) + 0.05 (single typed) - 0.10 (weak join) = 0.30 → stage
    # so to truly hit discard, lower further:
    inp2 = merge_mod.MergeInput(
        highlight_id="hl-discard2",
        highlight_text="x",
        hits=[_hit(merge_mod, "gliner", "ner", "WeakHit",
                   label="Concept", score=0.10, start=0, end=7)],
    )
    cands = merge_mod.merge_extractor_outputs(inp2)
    c = _by_canon(cands)["weakhit"]
    # 0.10 + 0.05 = 0.15 < 0.30 → discard
    assert c.extraction_score < merge_mod.STAGE_THRESHOLD
    assert c.route == "discard"


# --- ownership-seam invariant: Candidate shape -----------------------------


def test_candidate_returns_provenance_for_audit(merge_mod):
    """base_source / base_value / typed_labels / weak_join must be populated
    so downstream observers can audit asymmetric scoring decisions."""
    inp = merge_mod.MergeInput(
        highlight_id="hl-prov",
        highlight_text="A theory of relativity.",
        hits=[
            _hit(merge_mod, "gliner", "ner", "theory of relativity",
                 label="Theory", score=0.80, start=2, end=22),
            _hit(merge_mod, "keybert", "keyphrase", "theory of relativity",
                 score=0.65, rank=1),
        ],
    )
    cands = merge_mod.merge_extractor_outputs(inp)
    c = _by_canon(cands)["theory of relativity"]
    assert c.base_source == "gliner"  # gliner wins where present
    assert isinstance(c.base_value, float)
    assert c.agreement_n == 2
    assert ("gliner", "Theory") in c.typed_labels
    assert c.weak_join is False
    # extraction_score must never exceed 1.0 (clip invariant)
    assert 0.0 <= c.extraction_score <= 1.0


def test_offset_overlap_collapses_gliner_and_spacy(merge_mod):
    """gliner and spacy NER on the same span should land in one cluster."""
    inp = merge_mod.MergeInput(
        highlight_id="hl-overlap", highlight_text="Apple is a company.",
        hits=[
            _hit(merge_mod, "gliner", "ner", "Apple",
                 label="Organisation", score=0.85, start=0, end=5),
            _hit(merge_mod, "spacy", "ner", "Apple",
                 label="ORG", start=0, end=5),
        ],
    )
    cands = merge_mod.merge_extractor_outputs(inp)
    assert len(cands) == 1
    c = cands[0]
    assert c.agreement_n == 2
    # base 0.85 (gliner) + 0.15 (n=2) + 0.05 (both Org) = clip(1.05) = 1.0
    assert c.extraction_score == pytest.approx(1.0)
    assert c.route == "entity"
    assert c.kind == "org"


def test_mmr_diversification_flag_set(merge_mod):
    """Two clusters with token overlap → second is marked mmr_discounted.

    Uses spans at non-overlapping offsets so the clustering pass keeps them
    distinct (no offset-overlap collapse); their canonical keys share a
    token, so MMR Jaccard similarity flags the lower-scoring one.
    """
    inp = merge_mod.MergeInput(
        highlight_id="hl-mmr",
        highlight_text=("principle of falsifiability ... and also "
                        "falsifiability later in the text"),
        hits=[
            _hit(merge_mod, "gliner", "ner", "principle of falsifiability",
                 label="Theory", score=0.80, start=0, end=27),
            _hit(merge_mod, "gliner", "ner", "falsifiability",
                 label="Concept", score=0.75, start=41, end=55),
        ],
    )
    cands = merge_mod.merge_extractor_outputs(inp)
    by = _by_canon(cands)
    assert "principle of falsifiability" in by
    assert "falsifiability" in by
    # The shorter form scores 0.75; the longer scores 0.80. MMR keeps the
    # higher-scoring "principle of falsifiability" first, then flags
    # "falsifiability" as a token-Jaccard-overlapping neighbour.
    assert by["falsifiability"].mmr_discounted is True
