"""Strategy base class, @step decorator, and StrategyContext for fracta strategy pipelines."""

import inspect
import json
import sys
import time
import traceback
import warnings


class StrategyContext:
    """Execution context injected into every strategy run.

    Attributes:
        graph:  FalkorDB graph client (None if no graph configured).
        duckdb: DuckDB connection (fresh per execution for isolation).
        params: Validated input parameters from the caller.
    """

    __slots__ = ("graph", "duckdb", "params")

    def __init__(self, *, graph=None, duckdb=None, params=None):
        self.graph = graph
        self.duckdb = duckdb
        self.params = params or {}


class Step:
    """Metadata for a single pipeline step."""

    def __init__(self, name, fn, depends):
        self.name = name
        self.fn = fn
        self.depends = depends


def step(name, depends=None):
    """Decorator that marks a method as a pipeline step.

    Dependencies are inferred from method parameter names that match
    other step method names. The ``depends`` parameter can override this
    for explicit ordering.
    """

    def decorator(fn):
        fn._step_meta = Step(name, fn, depends or [])
        return fn

    return decorator


def _topo_sort(steps):
    """Topologically sort steps by their dependencies (Kahn's algorithm)."""
    by_fn = {s.fn.__name__: s for s in steps}
    in_degree = {s.fn.__name__: 0 for s in steps}
    dependents = {s.fn.__name__: [] for s in steps}

    for s in steps:
        for dep in s.depends:
            if dep in by_fn:
                in_degree[s.fn.__name__] += 1
                dependents[dep].append(s.fn.__name__)

    queue = [name for name, deg in in_degree.items() if deg == 0]
    order = []

    while queue:
        queue.sort()
        name = queue.pop(0)
        order.append(by_fn[name])
        for dep_name in dependents[name]:
            in_degree[dep_name] -= 1
            if in_degree[dep_name] == 0:
                queue.append(dep_name)

    if len(order) != len(steps):
        raise ValueError("Cycle detected in step dependencies")

    return order


_PARTIAL_STEP_LIMIT = 16 * 1024   # 16KB per step
_PARTIAL_TOTAL_LIMIT = 64 * 1024  # 64KB total


def _build_partial_results(results, step_fns=None):
    """Build truncated partial_results dict from completed step outputs.

    Returns (partial_results, truncated, omitted_steps).
    Steps whose function has _no_partial = True are excluded.
    Per-step outputs > 16KB are replaced with {"_truncated": true, "keys": [...]}.
    Total serialized size is capped at 64KB; excess steps are dropped.

    step_fns: optional dict mapping step fn.__name__ -> fn, used to check _no_partial.
    """
    partial = {}
    truncated = False
    omitted = []
    if step_fns is None:
        step_fns = {}

    for step_name, value in results.items():
        # Check _no_partial opt-out on the step function (not the return value)
        fn = step_fns.get(step_name)
        if fn is not None and getattr(fn, "_no_partial", False):
            continue

        try:
            serialized = json.dumps(value, default=str)
        except (TypeError, ValueError):
            serialized = json.dumps(str(value))

        if len(serialized) > _PARTIAL_STEP_LIMIT:
            # Replace with truncation stub
            keys = list(value.keys()) if isinstance(value, dict) else []
            partial[step_name] = {"_truncated": True, "keys": keys}
            truncated = True
        else:
            partial[step_name] = value

    # Check total size
    try:
        total_serialized = json.dumps(partial, default=str)
    except (TypeError, ValueError):
        total_serialized = json.dumps(str(partial))

    if len(total_serialized) > _PARTIAL_TOTAL_LIMIT:
        truncated = True
        # Remove steps from the end until under limit
        step_names = list(partial.keys())
        while len(total_serialized) > _PARTIAL_TOTAL_LIMIT and step_names:
            removed = step_names.pop()
            omitted.append(removed)
            del partial[removed]
            try:
                total_serialized = json.dumps(partial, default=str)
            except (TypeError, ValueError):
                total_serialized = json.dumps(str(partial))

    return partial, truncated, omitted


class Strategy:
    """Base class for fracta strategies. Subclasses define @step methods."""

    def execute(self, ctx):
        """Run all steps in topological order, returning result + trace."""
        steps = []
        for attr_name in dir(self):
            attr = getattr(self, attr_name)
            if hasattr(attr, "_step_meta"):
                steps.append(attr._step_meta)

        # Infer dependencies from parameter names
        step_fn_names = {s.fn.__name__ for s in steps}
        for s in steps:
            # S5: Warn on unknown dependency names
            for dep in s.depends:
                if dep not in step_fn_names:
                    warnings.warn(f"Step '{s.name}' depends on unknown step '{dep}'")

            sig = inspect.signature(s.fn)
            param_names = [
                p for p in sig.parameters if p not in ("self", "ctx")
            ]
            all_deps = set(s.depends)
            for p in param_names:
                if p in step_fn_names:
                    all_deps.add(p)
            s.depends = list(all_deps)

        order = _topo_sort(steps)
        results = {}
        trace = []

        for s in order:
            kwargs = {}
            sig = inspect.signature(s.fn)
            for p_name in sig.parameters:
                if p_name in ("self", "ctx"):
                    continue
                if p_name in results:
                    kwargs[p_name] = results[p_name]

            start = time.monotonic()
            try:
                result = s.fn(self, ctx, **kwargs)
                elapsed_ms = round((time.monotonic() - start) * 1000)
                results[s.fn.__name__] = result
                trace.append(
                    {"name": s.name, "status": "ok", "duration_ms": elapsed_ms}
                )
            except Exception as e:
                elapsed_ms = round((time.monotonic() - start) * 1000)

                # Emit full traceback to stderr for server-side observability.
                tb_full = traceback.format_exc()
                print(tb_full, file=sys.stderr, flush=True)

                # Bounded excerpt: last 20 lines, capped at 4KB.
                tb_lines = tb_full.splitlines()
                tb_excerpt = "\n".join(tb_lines[-20:])
                if len(tb_excerpt) > 4096:
                    tb_excerpt = tb_excerpt[-4096:]

                trace.append(
                    {
                        "name": s.name,
                        "status": "error",
                        "duration_ms": elapsed_ms,
                        "error": str(e),
                        "traceback": tb_excerpt,
                    }
                )
                step_fns = {s.fn.__name__: s.fn for s in order}
                partial, truncated, omitted = _build_partial_results(results, step_fns)
                resp = {
                    "result": None,
                    "partial_results": partial,
                    "trace": {
                        "steps": trace,
                        "total_duration_ms": sum(
                            t["duration_ms"] for t in trace
                        ),
                        "error": str(e),
                        "traceback": tb_excerpt,
                    },
                }
                if truncated:
                    resp["partial_results_truncated"] = True
                    resp["omitted_steps"] = omitted
                return resp

        final_key = order[-1].fn.__name__
        return {
            "result": results[final_key],
            "trace": {
                "steps": trace,
                "total_duration_ms": sum(t["duration_ms"] for t in trace),
            },
        }
