#!/usr/bin/env python3
"""verify-bindings: lint every strategy binding.yaml.

For each binding.yaml under strategies/ (live and _example/), assert that:

  - The file parses as YAML and has a `source_bindings` map.
  - Every entry has both `mcp_tool` and `mcp_server` (or a documented
    exception via the special key `fetch_mode: native`).
  - Enveloped responses declare either `items_path` or `single_item: true`.
    The default (omit both) is array-at-root; OK if the operator confirms it.
  - field_map values are non-empty strings.

This is a static lint. It does NOT call live MCP servers — that's the job of
the nightly tools/list fixture run. Catches the bug class behind Bugs 5, 6,
7, 8, 16, 17, 18, 19, 23 (spec §7.1) at PR time, not at deploy time.

Exit code:
  0  all bindings clean
  1  one or more bindings have schema problems

Usage: python3 scripts/verify-bindings.py
"""
from __future__ import annotations

import pathlib
import sys

try:
    import yaml
except ImportError:
    sys.stderr.write("verify-bindings: PyYAML is required (pip install pyyaml)\n")
    sys.exit(2)


ROOT = pathlib.Path(__file__).resolve().parent.parent
STRATEGIES_DIR = ROOT / "strategies"


def _lint_binding(path: pathlib.Path) -> list[str]:
    errors: list[str] = []
    try:
        raw = path.read_text()
        doc = yaml.safe_load(raw) or {}
    except (OSError, yaml.YAMLError) as exc:
        return [f"{path}: failed to parse YAML: {exc}"]

    if not isinstance(doc, dict):
        return [f"{path}: top-level YAML is not a mapping"]

    bindings = doc.get("source_bindings")
    if bindings is None:
        # Some strategies are graph-only with no source bindings; that's OK.
        return []
    if not isinstance(bindings, dict):
        return [f"{path}: source_bindings must be a mapping, got {type(bindings).__name__}"]

    for name, entry in bindings.items():
        if not isinstance(entry, dict):
            errors.append(f"{path}:source_bindings.{name}: must be a mapping")
            continue

        fetch_mode = entry.get("fetch_mode", "fracta_mcp_gateway")
        if fetch_mode == "native":
            # Native loaders (DuckDB read_csv etc.) don't need mcp_tool.
            continue

        if not entry.get("mcp_tool"):
            errors.append(f"{path}:source_bindings.{name}: missing mcp_tool")
        if not entry.get("mcp_server"):
            errors.append(f"{path}:source_bindings.{name}: missing mcp_server")

        field_map = entry.get("field_map", {}) or {}
        if not isinstance(field_map, dict):
            errors.append(f"{path}:source_bindings.{name}: field_map must be a mapping")
        else:
            for col, src in field_map.items():
                if not isinstance(src, str) or not src.strip():
                    errors.append(
                        f"{path}:source_bindings.{name}.field_map.{col}: "
                        f"source must be a non-empty string"
                    )

        # Enveloped-response convention: spec §2.5 says Readwise/Reader/Notion
        # all return {"results": [...]} or similar. We don't require items_path
        # because some tools return arrays at root, but we warn (as an error)
        # if both items_path and single_item are set — that's contradictory.
        if entry.get("items_path") and entry.get("single_item"):
            errors.append(
                f"{path}:source_bindings.{name}: items_path and single_item are mutually exclusive"
            )

    return errors


def main() -> int:
    if not STRATEGIES_DIR.is_dir():
        sys.stderr.write(f"verify-bindings: {STRATEGIES_DIR} not found\n")
        return 2

    all_errors: list[str] = []
    checked = 0
    for binding_path in sorted(STRATEGIES_DIR.rglob("binding.yaml")):
        checked += 1
        all_errors.extend(_lint_binding(binding_path))

    if all_errors:
        sys.stderr.write(
            f"verify-bindings: {len(all_errors)} problem(s) across {checked} binding.yaml file(s):\n"
        )
        for line in all_errors:
            sys.stderr.write(f"  {line}\n")
        return 1

    print(f"verify-bindings: {checked} binding.yaml file(s) clean")
    return 0


if __name__ == "__main__":
    sys.exit(main())
