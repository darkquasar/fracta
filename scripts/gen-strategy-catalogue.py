#!/usr/bin/env python3
"""Generate Mintlify catalogue pages from strategies/.

Walks strategies/, reads each strategy's contract.yaml + strategy.py + optional
README.md, and emits one MDX page per strategy under
docs/strategies/catalogue/<domain>/<category>/<slug>.mdx, plus a generated
sidebar fragment so docs.json knows what to include.

Security posture (intentional, do not relax without review):
  - contract.yaml is parsed with yaml.safe_load only
  - strategy.py is parsed with ast.parse only — never imported, never exec'd
  - README.md is treated as inert text — sliced in verbatim, never rendered

Run locally:  make docs-gen
Run in CI:    .github/workflows/strategy-catalogue.yml
"""

from __future__ import annotations

import argparse
import ast
import json
import os
import sys
from dataclasses import dataclass, field
from pathlib import Path

import yaml


# ── Paths ─────────────────────────────────────────────────────────────────────

REPO_ROOT = Path(__file__).resolve().parent.parent
STRATEGIES_DIR = REPO_ROOT / "strategies"
CATALOGUE_DIR = REPO_ROOT / "docs" / "strategies" / "catalogue"
SIDEBAR_FRAGMENT = CATALOGUE_DIR / "_sidebar.json"
DOCS_JSON = REPO_ROOT / "docs" / "docs.json"


# ── Models ────────────────────────────────────────────────────────────────────

@dataclass
class StepNode:
    """One @step in a strategy.py — extracted via AST, never executed."""
    func_name: str            # the Python function name, e.g. "load_data"
    label: str                # the human-readable name from @step("...")
    deps: list[str] = field(default_factory=list)  # other @step func_names this depends on


@dataclass
class Strategy:
    """One discovered strategy — what the catalogue page describes."""
    domain: str               # e.g. "security"
    category: str             # e.g. "enrichment"
    slug: str                 # directory name, e.g. "elastic_field_survey"
    contract: dict            # parsed contract.yaml
    steps: list[StepNode]
    readme: str | None        # raw README.md content, or None
    rel_path: str             # path under strategies/, e.g. "security/enrichment/elastic_field_survey"


# ── Discovery ─────────────────────────────────────────────────────────────────

def discover_strategies(root: Path) -> list[Strategy]:
    """Walk root looking for directories with contract.yaml + strategy.py.

    Skips any directory whose name starts with '_' or '.' (matches runner rule).
    Mirrors strategies/runner.py:discover_strategies().
    """
    strategies: list[Strategy] = []
    if not root.is_dir():
        return strategies

    for dirpath, dirnames, filenames in os.walk(root):
        # Skip framework packages, caches, hidden dirs, venvs, _example/, etc.
        dirnames[:] = sorted(d for d in dirnames if not d.startswith(("_", ".")))

        if "contract.yaml" in filenames and "strategy.py" in filenames:
            d = Path(dirpath)
            rel = d.relative_to(root)
            parts = rel.parts
            if len(parts) < 3:
                # We only catalogue domain/category/slug. Flatter layouts (e.g.
                # strategies/foo/) are valid for the runner but ambiguous to
                # catalogue, so skip with a warning.
                print(
                    f"warn: skipping {rel} — expected <domain>/<category>/<slug>, got {len(parts)} levels",
                    file=sys.stderr,
                )
                # Don't descend further into this strategy's tree.
                dirnames[:] = []
                continue

            domain, category, slug = parts[0], parts[1], parts[-1]

            try:
                contract = _parse_contract(d / "contract.yaml")
                steps = _parse_steps(d / "strategy.py")
            except Exception as exc:
                # A bad strategy shouldn't fail the whole catalogue build —
                # log it and skip. CI will surface the warn.
                print(f"warn: skipping {rel}: {exc}", file=sys.stderr)
                dirnames[:] = []
                continue

            readme_path = d / "README.md"
            readme = readme_path.read_text() if readme_path.is_file() else None

            strategies.append(
                Strategy(
                    domain=domain,
                    category=category,
                    slug=slug,
                    contract=contract,
                    steps=steps,
                    readme=readme,
                    rel_path=str(rel),
                )
            )

            # Don't descend into a strategy directory's children.
            dirnames[:] = []

    return strategies


def _parse_contract(path: Path) -> dict:
    """Read contract.yaml. safe_load only — never load arbitrary objects."""
    with path.open("r") as f:
        contract = yaml.safe_load(f)
    if not isinstance(contract, dict) or "name" not in contract:
        raise ValueError(f"contract.yaml missing required 'name' field")
    return contract


def _parse_steps(path: Path) -> list[StepNode]:
    """Extract @step methods from strategy.py via AST. No imports, no exec."""
    source = path.read_text()
    tree = ast.parse(source, filename=str(path))

    steps: list[StepNode] = []
    step_func_names: set[str] = set()  # for filtering deps

    # Find Strategy subclass(es). We don't actually verify it inherits from
    # Strategy (that would require resolving names) — we look for any class
    # whose methods carry @step decorators. Same effect, simpler logic.
    for node in ast.walk(tree):
        if not isinstance(node, ast.ClassDef):
            continue
        for item in node.body:
            if not isinstance(item, (ast.FunctionDef, ast.AsyncFunctionDef)):
                continue
            label = _step_label(item)
            if label is None:
                continue
            step_func_names.add(item.name)

    # Second pass: build StepNodes with deps filtered to known step names.
    for node in ast.walk(tree):
        if not isinstance(node, ast.ClassDef):
            continue
        for item in node.body:
            if not isinstance(item, (ast.FunctionDef, ast.AsyncFunctionDef)):
                continue
            label = _step_label(item)
            if label is None:
                continue

            # Args (excluding self, ctx) that match other step function names
            # are dependency edges.
            arg_names = [a.arg for a in item.args.args if a.arg not in ("self", "ctx")]
            deps = [a for a in arg_names if a in step_func_names]

            steps.append(StepNode(func_name=item.name, label=label, deps=deps))

    return steps


def _step_label(func: ast.FunctionDef | ast.AsyncFunctionDef) -> str | None:
    """If this function has @step("Label"), return the Label. Else None.

    Recognizes both @step(...) and @step (no args). For @step alone, falls
    back to the function name as the label.
    """
    for dec in func.decorator_list:
        # @step("My label") → ast.Call with func=ast.Name(id='step')
        if isinstance(dec, ast.Call) and isinstance(dec.func, ast.Name) and dec.func.id == "step":
            if dec.args and isinstance(dec.args[0], ast.Constant) and isinstance(dec.args[0].value, str):
                return dec.args[0].value
            return func.name  # @step(...) but no string arg
        # bare @step → ast.Name(id='step')
        if isinstance(dec, ast.Name) and dec.id == "step":
            return func.name
    return None


# ── Rendering ─────────────────────────────────────────────────────────────────

def render_page(s: Strategy) -> str:
    """Render one MDX page for a strategy."""
    contract = s.contract
    name = contract.get("name", s.slug)
    description = contract.get("description", "").strip()
    version = contract.get("version", "—")
    tags = contract.get("tags", []) or []
    params = contract.get("params", {}) or {}
    requires = contract.get("requires", {}) or {}
    tables = requires.get("tables", {}) or {}

    out: list[str] = []

    # Frontmatter
    out.append("---")
    out.append(f'title: "{_yaml_str(name)}"')
    if description:
        # Mintlify frontmatter description is one line; first sentence is fine.
        first = description.replace("\n", " ").strip().split(". ")[0].rstrip(".")
        out.append(f'description: "{_yaml_str(first)}"')
    out.append("---")
    out.append("")

    # AUTOGEN banner — readers and reviewers should see it immediately.
    out.append("{/* AUTOGENERATED by scripts/gen-strategy-catalogue.py — do not edit. */}")
    out.append(f"{{/* Source: strategies/{s.rel_path}/ */}}")
    out.append("")

    # Header block
    out.append(f"# {name}")
    out.append("")
    if description:
        out.append(description)
        out.append("")

    out.append("| | |")
    out.append("|---|---|")
    out.append(f"| **Domain** | `{s.domain}` |")
    out.append(f"| **Category** | `{s.category}` |")
    out.append(f"| **Version** | `{version}` |")
    if tags:
        out.append(f"| **Tags** | {', '.join(f'`{t}`' for t in tags)} |")
    out.append(f"| **Source** | [`strategies/{s.rel_path}/`](https://github.com/darkquasar/fracta/tree/main/strategies/{s.rel_path}) |")
    out.append("")

    # Author-controlled README — verbatim, no templating.
    if s.readme:
        out.append("## About")
        out.append("")
        out.append(s.readme.strip())
        out.append("")

    # AST-derived step DAG (Mermaid).
    if s.steps:
        out.append("## Steps")
        out.append("")
        out.append(_render_step_diagram(s.steps))
        out.append("")
        out.append("| Step | Function | Depends on |")
        out.append("|---|---|---|")
        for st in s.steps:
            deps = ", ".join(f"`{d}`" for d in st.deps) if st.deps else "—"
            out.append(f"| {st.label} | `{st.func_name}` | {deps} |")
        out.append("")

    # Parameters table.
    if params:
        out.append("## Parameters")
        out.append("")
        out.append("| Name | Type | Required | Default | Description |")
        out.append("|---|---|---|---|---|")
        for pname, pdef in params.items():
            ptype = (pdef or {}).get("type", "—")
            preq = "yes" if (pdef or {}).get("required") else "no"
            pdef_default = (pdef or {}).get("default", "—")
            pdesc = (pdef or {}).get("description", "")
            out.append(f"| `{pname}` | `{ptype}` | {preq} | `{pdef_default}` | {pdesc} |")
        out.append("")

    # Required tables.
    if tables:
        out.append("## Required tables")
        out.append("")
        for tname, tdef in tables.items():
            tdef = tdef or {}
            tdesc = tdef.get("description", "")
            optional = "optional" if tdef.get("optional") else "required"
            cols = tdef.get("columns", {}) or {}
            out.append(f"### `{tname}` ({optional})")
            out.append("")
            if tdesc:
                out.append(tdesc)
                out.append("")
            if cols:
                out.append("| Column | Type | Semantic |")
                out.append("|---|---|---|")
                for cname, cdef in cols.items():
                    cdef = cdef or {}
                    out.append(f"| `{cname}` | `{cdef.get('type', '—')}` | {cdef.get('semantic', '—')} |")
                out.append("")

    return "\n".join(out)


def _render_step_diagram(steps: list[StepNode]) -> str:
    """Render the @step DAG as a Mermaid flowchart."""
    lines = ["```mermaid", "flowchart TD"]
    for st in steps:
        lines.append(f'    {st.func_name}["{_mermaid_str(st.label)}"]')
    for st in steps:
        for dep in st.deps:
            lines.append(f"    {dep} --> {st.func_name}")
    lines.append("```")
    return "\n".join(lines)


def _yaml_str(s: str) -> str:
    """Escape a string for embedding inside a YAML double-quoted scalar."""
    return s.replace("\\", "\\\\").replace('"', '\\"')


def _mermaid_str(s: str) -> str:
    """Escape a string for embedding inside a Mermaid node label."""
    # Mermaid quotes within labels can confuse the parser; replace them.
    return s.replace('"', "'")


# ── Sidebar ───────────────────────────────────────────────────────────────────

def render_sidebar(strategies: list[Strategy]) -> dict:
    """Build the sidebar fragment for docs.json's catalogue subgroup.

    Shape: a Mintlify nav group with one nested group per domain, each
    containing one entry per strategy under that domain (paths are
    rooted at the docs/ tree).
    """
    return {
        "group": "Catalogue",
        "pages": _catalogue_pages(strategies),
    }


def _catalogue_pages(strategies: list[Strategy]) -> list:
    """The 'pages' value that goes inside the Catalogue subgroup."""
    by_domain: dict[str, list[Strategy]] = {}
    for s in sorted(strategies, key=lambda x: (x.domain, x.category, x.slug)):
        by_domain.setdefault(s.domain, []).append(s)

    domain_groups = []
    for domain, items in sorted(by_domain.items()):
        domain_groups.append({
            "group": domain,
            "pages": [
                f"strategies/catalogue/{s.domain}/{s.category}/{s.slug}"
                for s in items
            ],
        })
    return domain_groups


def update_docs_json(strategies: list[Strategy]) -> str:
    """Return docs.json with the Catalogue subgroup's `pages` regenerated.

    Touches only that one nested array — everything else is preserved
    byte-for-byte (we re-serialize with the same 2-space indent + trailing
    newline that the file was authored with).
    """
    original = DOCS_JSON.read_text()
    data = json.loads(original)

    strategies_group = next(
        (g for g in data["navigation"]["groups"] if g.get("group") == "Strategies"),
        None,
    )
    if strategies_group is None:
        raise RuntimeError("docs.json: 'Strategies' top-level group not found")

    catalogue_subgroup = next(
        (p for p in strategies_group["pages"]
         if isinstance(p, dict) and p.get("group") == "Catalogue"),
        None,
    )
    if catalogue_subgroup is None:
        raise RuntimeError(
            "docs.json: 'Catalogue' subgroup not found under 'Strategies'. "
            "Add a placeholder before running the generator."
        )

    catalogue_subgroup["pages"] = _catalogue_pages(strategies)

    return json.dumps(data, indent=2) + "\n"


# ── Driver ────────────────────────────────────────────────────────────────────

def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--check",
        action="store_true",
        help="Generate to a temp dir and diff against the committed tree. "
             "Exit 0 if identical, 1 otherwise. Used by CI to detect 'forgot to regen'.",
    )
    args = parser.parse_args()

    strategies = discover_strategies(STRATEGIES_DIR)
    print(f"discovered {len(strategies)} strategies", file=sys.stderr)

    if args.check:
        return _check_mode(strategies)

    # Wipe the catalogue tree before regenerating, so deletions propagate.
    # We only touch docs/strategies/catalogue/, never anything else under docs/.
    if CATALOGUE_DIR.is_dir():
        for path in sorted(CATALOGUE_DIR.rglob("*"), reverse=True):
            if path.is_file():
                path.unlink()
            elif path.is_dir():
                path.rmdir()

    CATALOGUE_DIR.mkdir(parents=True, exist_ok=True)

    for s in strategies:
        page_dir = CATALOGUE_DIR / s.domain / s.category
        page_dir.mkdir(parents=True, exist_ok=True)
        page_path = page_dir / f"{s.slug}.mdx"
        page_path.write_text(render_page(s))
        print(f"  wrote {page_path.relative_to(REPO_ROOT)}", file=sys.stderr)

    sidebar = render_sidebar(strategies)
    SIDEBAR_FRAGMENT.write_text(json.dumps(sidebar, indent=2) + "\n")
    print(f"  wrote {SIDEBAR_FRAGMENT.relative_to(REPO_ROOT)}", file=sys.stderr)

    # Update docs.json's Catalogue subgroup. Only that one nested array
    # changes — all other fields are preserved byte-for-byte.
    new_docs_json = update_docs_json(strategies)
    if new_docs_json != DOCS_JSON.read_text():
        DOCS_JSON.write_text(new_docs_json)
        print(f"  wrote {DOCS_JSON.relative_to(REPO_ROOT)} (Catalogue pages updated)", file=sys.stderr)
    else:
        print(f"  {DOCS_JSON.relative_to(REPO_ROOT)} already up to date", file=sys.stderr)

    return 0


def _check_mode(strategies: list[Strategy]) -> int:
    """Compare what we *would* generate against what's committed. Used by CI."""
    import difflib

    drift = False
    for s in strategies:
        page_path = CATALOGUE_DIR / s.domain / s.category / f"{s.slug}.mdx"
        expected = render_page(s)
        actual = page_path.read_text() if page_path.is_file() else ""
        if expected != actual:
            drift = True
            print(f"\nDrift in {page_path.relative_to(REPO_ROOT)}:", file=sys.stderr)
            for line in difflib.unified_diff(
                actual.splitlines(),
                expected.splitlines(),
                fromfile="committed",
                tofile="expected",
                lineterm="",
            ):
                print(line, file=sys.stderr)

    expected_sidebar = json.dumps(render_sidebar(strategies), indent=2) + "\n"
    actual_sidebar = SIDEBAR_FRAGMENT.read_text() if SIDEBAR_FRAGMENT.is_file() else ""
    if expected_sidebar != actual_sidebar:
        drift = True
        print(f"\nDrift in {SIDEBAR_FRAGMENT.relative_to(REPO_ROOT)}", file=sys.stderr)

    expected_docs_json = update_docs_json(strategies)
    actual_docs_json = DOCS_JSON.read_text()
    if expected_docs_json != actual_docs_json:
        drift = True
        print(f"\nDrift in {DOCS_JSON.relative_to(REPO_ROOT)} (Catalogue subgroup)", file=sys.stderr)

    if drift:
        print("\nrun `make docs-gen` and commit the result.", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
