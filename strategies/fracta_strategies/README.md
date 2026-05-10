# fracta_strategies — the strategy SDK

This is **not a strategy.** It is the Python package that strategy authors
import. Every strategy in the repo (and every strategy authored at runtime
through `strategy_create`) starts with:

```python
from fracta_strategies import Strategy, step
```

That import resolves to this directory.

## What lives here

- `Strategy` — base class. Your strategy class inherits from it.
- `step` — decorator that marks a method as a pipeline step.
- `StrategyContext` — the `ctx` object every step receives, exposing
  `ctx.duckdb`, `ctx.graph`, and `ctx.params`.

See `base.py` for the implementation. The full API surface is intentionally
small.

## Why it sits inside `strategies/`

It's co-located with the strategies that use it so that the sidecar can
import both as a single Python package tree, with one `pyproject.toml` and
one `uv.lock`. There is no Python-package reason for the SDK to live
elsewhere.

## Why the directory name does not start with `_`

The directory name is the package import name. Renaming this directory
would break every `from fracta_strategies import ...` statement in:

- `runner.py` (the sidecar entry point)
- `tests/`
- every authored strategy
- `internal/mcpserver/strategy_tools.go`, which instructs strategy-authoring
  agents to use this exact import path

The `_` convention only applies to **strategy directories** — i.e. things
the runner might mistake for a strategy. The runner only treats a directory
as a strategy if it contains both `contract.yaml` and `strategy.py`. This
package has neither, so the runner walks past it without inspection.
Same protection covers `tests/` and any other support directory here.

## Don't put `contract.yaml` or `strategy.py` in this directory

Doing so would make the runner try to load this package as a strategy,
which is not what it is.
