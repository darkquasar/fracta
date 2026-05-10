# tests — strategy SDK and runner tests

This is **not a strategy.** It contains the test suite for the strategy
framework itself:

- `test_base.py` — covers `fracta_strategies` (the SDK): the `Strategy`
  base class, the `@step` decorator, dependency-graph topo sort, partial
  result handling.
- `test_runner.py` — covers `runner.py`: discovery, contract validation,
  step execution, error paths.

Run them from the parent directory:

```bash
cd strategies/
uv run pytest
```

## Why the runner ignores this directory

The runner only treats a directory as a strategy if it contains both
`contract.yaml` and `strategy.py`. This directory has neither, so it's
walked past during discovery. No `_` prefix needed for that reason.
