"""Tests for the fracta_strategies base classes: Strategy, @step, topo sort."""

import warnings

from fracta_strategies import Strategy, StrategyContext, step
from fracta_strategies.base import _build_partial_results


def test_single_step():
    class OneStep(Strategy):
        @step("only step")
        def do_it(self, ctx):
            return 42

    result = OneStep().execute(StrategyContext())
    assert result["result"] == 42
    assert len(result["trace"]["steps"]) == 1
    assert result["trace"]["steps"][0]["status"] == "ok"


def test_two_step_chain():
    class TwoSteps(Strategy):
        @step("first")
        def first(self, ctx):
            return "hello"

        @step("second")
        def second(self, ctx, first):
            return f"{first} world"

    result = TwoSteps().execute(StrategyContext())
    assert result["result"] == "hello world"
    assert len(result["trace"]["steps"]) == 2


def test_step_with_params():
    class ParamStrategy(Strategy):
        @step("greet")
        def greet(self, ctx):
            return f"hi {ctx.params['name']}"

    ctx = StrategyContext(params={"name": "fracta"})
    result = ParamStrategy().execute(ctx)
    assert result["result"] == "hi fracta"


def test_step_error_produces_trace():
    class FailStrategy(Strategy):
        @step("boom")
        def boom(self, ctx):
            raise ValueError("deliberate")

    result = FailStrategy().execute(StrategyContext())
    assert result["result"] is None
    assert result["trace"]["error"] == "deliberate"
    assert result["trace"]["steps"][0]["status"] == "error"
    # S10.D: traceback is included in both step entry and trace
    assert "traceback" in result["trace"]["steps"][0]
    assert "ValueError: deliberate" in result["trace"]["steps"][0]["traceback"]
    assert "traceback" in result["trace"]
    assert "ValueError: deliberate" in result["trace"]["traceback"]


def test_explicit_depends():
    order = []

    class Ordered(Strategy):
        @step("b", depends=["a_step"])
        def b_step(self, ctx):
            order.append("b")
            return "b"

        @step("a")
        def a_step(self, ctx):
            order.append("a")
            return "a"

    Ordered().execute(StrategyContext())
    assert order == ["a", "b"]


# --- S1: Partial result preservation tests ---


def test_partial_results_on_failure():
    """3-step DAG where step 2 fails: partial_results has step 1 output."""

    class ThreeSteps(Strategy):
        @step("step_one")
        def step_one(self, ctx):
            return {"count": 10}

        @step("step_two")
        def step_two(self, ctx, step_one):
            raise ValueError("step 2 failed")

        @step("step_three")
        def step_three(self, ctx, step_two):
            return "never reached"

    result = ThreeSteps().execute(StrategyContext())
    assert result["result"] is None
    assert result["trace"]["error"] == "step 2 failed"
    assert "partial_results" in result
    assert result["partial_results"]["step_one"] == {"count": 10}
    assert "step_two" not in result["partial_results"]
    assert "step_three" not in result["partial_results"]


def test_partial_results_truncation_per_step():
    """Step output >16KB is replaced with truncation stub."""

    class BigOutput(Strategy):
        @step("big_step")
        def big_step(self, ctx):
            return {"data": "x" * 20000, "key2": "val"}

        @step("fail_step")
        def fail_step(self, ctx, big_step):
            raise RuntimeError("boom")

    result = BigOutput().execute(StrategyContext())
    assert result["partial_results"]["big_step"]["_truncated"] is True
    assert "data" in result["partial_results"]["big_step"]["keys"]
    assert "key2" in result["partial_results"]["big_step"]["keys"]
    assert result.get("partial_results_truncated") is True


def test_partial_results_truncation_total():
    """Total >64KB triggers omitted_steps."""

    class ManyBigSteps(Strategy):
        @step("a")
        def a(self, ctx):
            return "x" * 15000  # just under 16KB per-step limit

        @step("b")
        def b(self, ctx, a):
            return "y" * 15000

        @step("c")
        def c(self, ctx, b):
            return "z" * 15000

        @step("d")
        def d(self, ctx, c):
            return "w" * 15000

        @step("e")
        def e(self, ctx, d):
            return "v" * 15000

        @step("fail")
        def fail(self, ctx, e):
            raise RuntimeError("boom")

    result = ManyBigSteps().execute(StrategyContext())
    assert result["partial_results_truncated"] is True
    assert len(result["omitted_steps"]) > 0


def test_partial_results_empty_when_first_step_fails():
    """If the first step fails, partial_results is empty."""

    class FirstFails(Strategy):
        @step("only")
        def only(self, ctx):
            raise RuntimeError("instant failure")

    result = FirstFails().execute(StrategyContext())
    assert result["partial_results"] == {}


# --- S1: _no_partial opt-out test ---


def test_no_partial_excludes_step_from_partial_results():
    """Step function with _no_partial = True is excluded from partial_results."""

    class WithOptOut(Strategy):
        @step("included")
        def included(self, ctx):
            return {"visible": True}

        @step("excluded")
        def excluded(self, ctx, included):
            return {"big_internal_data": "should not appear"}

        @step("fail")
        def fail(self, ctx, excluded):
            raise RuntimeError("boom")

    # Mark the step function as _no_partial
    WithOptOut.excluded._no_partial = True

    result = WithOptOut().execute(StrategyContext())
    assert result["result"] is None
    assert "included" in result["partial_results"]
    assert result["partial_results"]["included"] == {"visible": True}
    assert "excluded" not in result["partial_results"]


# --- S5: Unknown dependency warning tests ---


def test_unknown_dependency_warning():
    """Step with unknown dependency name emits a warning."""

    class BadDep(Strategy):
        @step("oops", depends=["nonexistent"])
        def oops(self, ctx):
            return "ok"

    with warnings.catch_warnings(record=True) as w:
        warnings.simplefilter("always")
        BadDep().execute(StrategyContext())
        assert len(w) == 1
        assert "nonexistent" in str(w[0].message)
        assert "oops" in str(w[0].message)


# --- S10.D: Traceback emission tests ---


def test_traceback_bounded_excerpt():
    """Traceback excerpt is bounded to last 20 lines, max 4KB."""

    class DeepFail(Strategy):
        @step("deep")
        def deep(self, ctx):
            # Create a deep call stack to produce a long traceback
            def level(n):
                if n <= 0:
                    raise RuntimeError("deep failure")
                return level(n - 1)
            return level(50)

    result = DeepFail().execute(StrategyContext())
    tb = result["trace"]["traceback"]
    assert "RuntimeError: deep failure" in tb
    # Bounded: at most 20 lines
    assert len(tb.splitlines()) <= 20
    # Bounded: at most 4KB
    assert len(tb) <= 4096


def test_traceback_emitted_to_stderr(capsys):
    """Traceback is printed to stderr."""

    class StderrFail(Strategy):
        @step("oops")
        def oops(self, ctx):
            raise TypeError("bad type")

    StderrFail().execute(StrategyContext())
    captured = capsys.readouterr()
    assert "TypeError: bad type" in captured.err
    assert "Traceback" in captured.err


# --- S12: ctx.config removed ---


def test_strategy_context_no_config():
    """StrategyContext no longer has a config attribute."""
    ctx = StrategyContext()
    assert not hasattr(ctx, "config")
    assert hasattr(ctx, "graph")
    assert hasattr(ctx, "duckdb")
    assert hasattr(ctx, "params")
