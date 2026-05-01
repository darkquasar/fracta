"""Tests for runner.py: contract-based strategy discovery and creation."""

import sys
import os

# runner.py is a script at project root, not inside the package.
# Add it to sys.path so we can import its functions for testing.
_strategies_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if _strategies_dir not in sys.path:
    sys.path.insert(0, _strategies_dir)

from runner import (
    discover_strategies,
    handle_create,
    validate_staging_contract,
    validate_with_manifest,
    _load_parquet_table,
    _collect_run_dirs,
    _ExtendedEncoder,
)

import datetime
import decimal
import json
import uuid

import duckdb


# --- Contract-based discovery tests ---


def test_discover_contract_based_strategy(tmp_path):
    """Directory with contract.yaml + strategy.py is discovered."""
    strat_dir = tmp_path / "correlation" / "my_strat"
    strat_dir.mkdir(parents=True)

    (strat_dir / "contract.yaml").write_text(
        'name: "my-strategy"\n'
        'description: "Test strategy."\n'
        "tags: [correlation]\n"
        "params:\n"
        "  ip:\n"
        "    type: str\n"
        "    required: true\n"
        "requires:\n"
        "  graph: true\n"
    )
    (strat_dir / "strategy.py").write_text(
        "from fracta_strategies import Strategy, step\n\n"
        "class MyStrat(Strategy):\n"
        "    @step('do')\n"
        "    def do_it(self, ctx):\n"
        "        return 'ok'\n"
    )

    results = discover_strategies(str(tmp_path))
    assert len(results) == 1
    s = results[0]
    assert s["name"] == "my-strategy"
    assert s["tags"] == ["correlation"]
    assert s["requires"]["graph"] is True
    assert s["file"] == os.path.join("correlation", "my_strat", "strategy.py")
    assert s["contract_path"] == os.path.join("correlation", "my_strat", "contract.yaml")


def test_discover_skips_dir_without_contract(tmp_path):
    """A directory with strategy.py but no contract.yaml is NOT discovered."""
    strat_dir = tmp_path / "orphan"
    strat_dir.mkdir()
    (strat_dir / "strategy.py").write_text("x = 1\n")

    results = discover_strategies(str(tmp_path))
    assert len(results) == 0


def test_discover_skips_dir_without_strategy_py(tmp_path):
    """A directory with contract.yaml but no strategy.py is NOT discovered."""
    strat_dir = tmp_path / "incomplete"
    strat_dir.mkdir()
    (strat_dir / "contract.yaml").write_text('name: "test"\ndescription: "x"\ntags: [t]\n')

    results = discover_strategies(str(tmp_path))
    assert len(results) == 0


def test_discover_skips_contract_without_name(tmp_path):
    """contract.yaml missing 'name' field is skipped."""
    strat_dir = tmp_path / "bad"
    strat_dir.mkdir()
    (strat_dir / "contract.yaml").write_text("description: 'no name'\ntags: [t]\n")
    (strat_dir / "strategy.py").write_text("x = 1\n")

    results = discover_strategies(str(tmp_path))
    assert len(results) == 0


def test_discover_skips_hidden_and_underscore_dirs(tmp_path):
    """Directories starting with _ or . are skipped entirely."""
    hidden = tmp_path / ".venv" / "some_strat"
    hidden.mkdir(parents=True)
    (hidden / "contract.yaml").write_text('name: "hidden"\ndescription: "x"\ntags: [t]\n')
    (hidden / "strategy.py").write_text("x = 1\n")

    internal = tmp_path / "__pycache__" / "cached"
    internal.mkdir(parents=True)
    (internal / "contract.yaml").write_text('name: "cached"\ndescription: "x"\ntags: [t]\n')
    (internal / "strategy.py").write_text("x = 1\n")

    results = discover_strategies(str(tmp_path))
    assert len(results) == 0


def test_discover_flat_py_files_ignored(tmp_path):
    """Flat .py files with METADATA dicts are no longer discovered."""
    (tmp_path / "old_strat.py").write_text(
        'METADATA = {"name": "old", "description": "legacy", "tags": ["test"], "params": {}}\n'
    )
    results = discover_strategies(str(tmp_path))
    assert len(results) == 0


def test_discover_nonexistent_dir():
    """Non-existent strategy directory returns empty list."""
    results = discover_strategies("/nonexistent/path")
    assert results == []


# --- handle_create with contract YAML ---


def test_handle_create_contract_format(tmp_path):
    """handle_create with contract_yaml creates directory structure."""
    code = (
        "from fracta_strategies import Strategy, step\n\n"
        "class TestStrat(Strategy):\n"
        "    @step('do')\n"
        "    def do_it(self, ctx):\n"
        "        return 'ok'\n"
    )
    contract = (
        'name: "test-strategy"\n'
        'description: "A test strategy."\n'
        "tags: [correlation, test]\n"
    )

    result = handle_create(str(tmp_path), "test_strategy", code, contract_yaml=contract)
    assert result["status"] == "ok"
    assert result["name"] == "test-strategy"
    assert result["tags"] == ["correlation", "test"]
    assert "directory" in result

    # Verify files were created
    strat_dir = tmp_path / "correlation" / "test_strategy"
    assert (strat_dir / "contract.yaml").exists()
    assert (strat_dir / "strategy.py").exists()


def test_handle_create_contract_missing_name(tmp_path):
    """Contract without name is rejected."""
    code = "x = 1\n"
    contract = 'description: "No name."\ntags: [test]\n'

    result = handle_create(str(tmp_path), "bad", code, contract_yaml=contract)
    assert result["status"] == "error"
    assert "name" in result["error"]


def test_handle_create_contract_invalid_yaml(tmp_path):
    """Invalid YAML is rejected."""
    code = "x = 1\n"
    contract = ":\n  - bad yaml: [unclosed"

    result = handle_create(str(tmp_path), "bad", code, contract_yaml=contract)
    assert result["status"] == "error"


def test_handle_create_contract_duplicate_dir(tmp_path):
    """Creating a strategy that already exists fails."""
    code = (
        "from fracta_strategies import Strategy, step\n\n"
        "class S(Strategy):\n"
        "    @step('do')\n"
        "    def do_it(self, ctx):\n"
        "        return 'ok'\n"
    )
    contract = 'name: "dup"\ndescription: "Duplicate."\ntags: [test]\n'

    # First creation succeeds
    result1 = handle_create(str(tmp_path), "dup", code, contract_yaml=contract)
    assert result1["status"] == "ok"

    # Second creation fails
    result2 = handle_create(str(tmp_path), "dup", code, contract_yaml=contract)
    assert result2["status"] == "error"
    assert "already exists" in result2["error"]


def test_handle_create_contract_syntax_error(tmp_path):
    """Python syntax error is caught before writing."""
    code = "def broken(\n"
    contract = 'name: "bad"\ndescription: "Bad code."\ntags: [test]\n'

    result = handle_create(str(tmp_path), "bad", code, contract_yaml=contract)
    assert result["status"] == "error"
    assert "Syntax error" in result["error"]


def test_handle_create_no_contract(tmp_path):
    """handle_create without contract_yaml returns error."""
    code = "x = 1\n"
    result = handle_create(str(tmp_path), "test", code, contract_yaml=None)
    assert result["status"] == "error"
    assert "Contract YAML is required" in result["error"]


# --- validate_staging_contract tests ---


def test_validate_staging_contract_all_present():
    """No errors when all required tables and columns are staged."""
    db = duckdb.connect()
    db.execute('CREATE TABLE auth_events (identity_arn VARCHAR, source_ip VARCHAR)')

    contract = {
        "requires": {
            "tables": {
                "auth_events": {
                    "columns": {
                        "identity_arn": {"type": "VARCHAR"},
                        "source_ip": {"type": "VARCHAR"},
                    }
                }
            }
        }
    }
    errors = validate_staging_contract(db, contract)
    db.close()
    assert errors == []


def test_validate_staging_contract_missing_table():
    """Error when required table is not staged."""
    db = duckdb.connect()
    contract = {
        "requires": {
            "tables": {
                "auth_events": {
                    "columns": {"identity_arn": {"type": "VARCHAR"}},
                }
            }
        }
    }
    errors = validate_staging_contract(db, contract)
    db.close()
    assert len(errors) == 1
    assert "auth_events" in errors[0]
    assert "not staged" in errors[0]


def test_validate_staging_contract_missing_column():
    """Error when required column is missing from staged table."""
    db = duckdb.connect()
    db.execute('CREATE TABLE auth_events (identity_arn VARCHAR)')

    contract = {
        "requires": {
            "tables": {
                "auth_events": {
                    "columns": {
                        "identity_arn": {"type": "VARCHAR"},
                        "source_ip": {"type": "VARCHAR"},
                    }
                }
            }
        }
    }
    errors = validate_staging_contract(db, contract)
    db.close()
    assert len(errors) == 1
    assert "source_ip" in errors[0]


def test_validate_staging_contract_optional_table_skipped():
    """Optional tables are not flagged when missing."""
    db = duckdb.connect()
    contract = {
        "requires": {
            "tables": {
                "auth_events": {
                    "optional": True,
                    "columns": {"identity_arn": {"type": "VARCHAR"}},
                }
            }
        }
    }
    errors = validate_staging_contract(db, contract)
    db.close()
    assert errors == []


def test_validate_staging_contract_no_tables():
    """Contract with no table requirements passes validation."""
    db = duckdb.connect()
    contract = {"requires": {"graph": True}}
    errors = validate_staging_contract(db, contract)
    db.close()
    assert errors == []


# --- validate_with_manifest tests ---


def test_validate_with_manifest_all_staged():
    """No errors when all staged tables exist in DuckDB."""
    db = duckdb.connect()
    db.execute('CREATE TABLE alerts (alert_id VARCHAR, severity VARCHAR)')

    manifest = {
        "alerts": {"mode": "fracta_mcp_gateway", "required": True, "staged": True, "parquet_path": "/tmp/x.parquet"},
    }
    errors = validate_with_manifest(db, manifest)
    db.close()
    assert errors == []


def test_validate_with_manifest_native_skipped():
    """Native tables that are not staged do not produce errors."""
    db = duckdb.connect()
    manifest = {
        "computed": {"mode": "native", "required": False, "staged": False},
    }
    errors = validate_with_manifest(db, manifest)
    db.close()
    assert errors == []


def test_validate_with_manifest_required_not_staged():
    """Required table that is not staged produces an error."""
    db = duckdb.connect()
    manifest = {
        "enrichment": {"mode": "mcp", "required": True, "staged": False},
    }
    errors = validate_with_manifest(db, manifest)
    db.close()
    assert len(errors) == 1
    assert "enrichment" in errors[0]
    assert "not staged" in errors[0]


def test_validate_with_manifest_staged_but_missing():
    """Staged table that doesn't exist in DuckDB produces an error."""
    db = duckdb.connect()
    manifest = {
        "alerts": {"mode": "fracta_mcp_gateway", "required": True, "staged": True, "parquet_path": "/tmp/x.parquet"},
    }
    errors = validate_with_manifest(db, manifest)
    db.close()
    assert len(errors) == 1
    assert "alerts" in errors[0]
    assert "not found" in errors[0]


def test_validate_with_manifest_optional_not_staged():
    """Optional table that is not staged does not produce an error."""
    db = duckdb.connect()
    manifest = {
        "extra": {"mode": "mcp", "required": False, "staged": False},
    }
    errors = validate_with_manifest(db, manifest)
    db.close()
    assert errors == []


# --- _load_parquet_table tests ---


def test_load_parquet_table_file(tmp_path):
    """Load a single Parquet file into DuckDB."""
    db = duckdb.connect()
    # Create a small Parquet file
    db.execute(f"COPY (SELECT 1 AS id, 'test' AS name) TO '{tmp_path}/test.parquet' (FORMAT PARQUET)")

    result = _load_parquet_table(db, "test_table", str(tmp_path / "test.parquet"))
    assert result is True

    rows = db.execute("SELECT * FROM test_table").fetchall()
    assert len(rows) == 1
    assert rows[0] == (1, "test")
    db.close()


def test_load_parquet_table_missing_file():
    """Missing Parquet file returns False."""
    db = duckdb.connect()
    result = _load_parquet_table(db, "missing", "/nonexistent/path.parquet")
    assert result is False
    db.close()


def test_load_parquet_table_large_creates_view(tmp_path):
    """Tables above threshold get created as VIEWs."""
    db = duckdb.connect()
    # Create a Parquet file (size doesn't matter, we set threshold to 0)
    db.execute(f"COPY (SELECT * FROM range(10) t(id)) TO '{tmp_path}/big.parquet' (FORMAT PARQUET)")

    # Use threshold=0 so any nonzero row count triggers VIEW
    result = _load_parquet_table(db, "big_table", str(tmp_path / "big.parquet"), lazy_threshold=0)
    assert result is True

    # Verify it's a view, not a table
    info = db.execute(
        "SELECT table_type FROM information_schema.tables WHERE table_name = 'big_table'"
    ).fetchone()
    assert info[0] == "VIEW"
    db.close()


# --- _collect_run_dirs tests ---


def test_collect_run_dirs_from_manifest():
    """Extract run directories from manifest parquet paths."""
    manifest = {
        "alerts": {"parquet_path": "/tmp/fracta-staging/abc123/alerts.parquet"},
        "events": {"parquet_path": "/tmp/fracta-staging/abc123/events.parquet"},
        "native": {},
    }
    run_dirs = _collect_run_dirs(manifest, staging_root="/tmp/fracta-staging")
    assert run_dirs == {"/tmp/fracta-staging/abc123"}


def test_collect_run_dirs_empty_manifest():
    """Empty or None manifest returns empty set."""
    assert _collect_run_dirs(None) == set()
    assert _collect_run_dirs({}) == set()


def test_collect_run_dirs_no_parquet_paths():
    """Manifest entries without parquet_path produce no run dirs."""
    manifest = {
        "computed": {"mode": "native", "staged": False},
        "pending": {"mode": "mcp", "staged": False},
    }
    assert _collect_run_dirs(manifest) == set()


def test_collect_run_dirs_rejects_out_of_root(tmp_path):
    """Paths outside staging_root are rejected."""
    staging_root = str(tmp_path / "staging")
    os.makedirs(staging_root, exist_ok=True)
    manifest = {
        "alerts": {"parquet_path": "/etc/evil/abc123/alerts.parquet"},
        "events": {"parquet_path": f"{staging_root}/abc123/events.parquet"},
    }
    run_dirs = _collect_run_dirs(manifest, staging_root=staging_root)
    # Only the in-root path is collected
    assert run_dirs == {f"{staging_root}/abc123"}


def test_collect_run_dirs_custom_staging_root(tmp_path):
    """Custom staging root works for run dir collection."""
    custom_root = str(tmp_path / "custom-staging")
    os.makedirs(custom_root, exist_ok=True)
    manifest = {
        "alerts": {"parquet_path": f"{custom_root}/run42/alerts.parquet"},
    }
    run_dirs = _collect_run_dirs(manifest, staging_root=custom_root)
    assert run_dirs == {f"{custom_root}/run42"}


# --- validate_with_manifest column validation tests ---


def test_validate_with_manifest_checks_columns():
    """Manifest with expected columns validates they exist in DuckDB."""
    db = duckdb.connect()
    db.execute("CREATE TABLE alerts (alert_id VARCHAR, severity VARCHAR)")
    manifest = {
        "alerts": {
            "mode": "fracta_mcp_gateway",
            "required": True,
            "staged": True,
            "parquet_path": "/tmp/x.parquet",
            "columns": ["alert_id", "severity"],
        },
    }
    errors = validate_with_manifest(db, manifest)
    db.close()
    assert errors == []


def test_validate_with_manifest_missing_column():
    """Missing column in DuckDB table produces a validation error."""
    db = duckdb.connect()
    db.execute("CREATE TABLE alerts (alert_id VARCHAR)")
    manifest = {
        "alerts": {
            "mode": "fracta_mcp_gateway",
            "required": True,
            "staged": True,
            "parquet_path": "/tmp/x.parquet",
            "columns": ["alert_id", "severity"],
        },
    }
    errors = validate_with_manifest(db, manifest)
    db.close()
    assert len(errors) == 1
    assert "severity" in errors[0]
    assert "missing" in errors[0].lower()


def test_validate_with_manifest_no_columns_skips_check():
    """When manifest doesn't include columns, column check is skipped."""
    db = duckdb.connect()
    db.execute("CREATE TABLE alerts (alert_id VARCHAR)")
    manifest = {
        "alerts": {
            "mode": "fracta_mcp_gateway",
            "required": True,
            "staged": True,
            "parquet_path": "/tmp/x.parquet",
            # no "columns" key
        },
    }
    errors = validate_with_manifest(db, manifest)
    db.close()
    assert errors == []


# --- S2: Extended JSON encoder tests ---


def test_extended_encoder_datetime():
    """datetime objects are serialized as ISO strings."""
    dt = datetime.datetime(2026, 4, 1, 12, 30, 0)
    result = json.loads(json.dumps({"dt": dt}, cls=_ExtendedEncoder))
    assert result["dt"] == "2026-04-01T12:30:00"


def test_extended_encoder_date():
    """date objects are serialized as ISO strings."""
    d = datetime.date(2026, 4, 1)
    result = json.loads(json.dumps({"d": d}, cls=_ExtendedEncoder))
    assert result["d"] == "2026-04-01"


def test_extended_encoder_timedelta():
    """timedelta objects are serialized as total seconds."""
    td = datetime.timedelta(hours=1, minutes=30)
    result = json.loads(json.dumps({"td": td}, cls=_ExtendedEncoder))
    assert result["td"] == 5400.0


def test_extended_encoder_bytes():
    """bytes objects are decoded to UTF-8 strings."""
    b = b"hello world"
    result = json.loads(json.dumps({"b": b}, cls=_ExtendedEncoder))
    assert result["b"] == "hello world"


def test_extended_encoder_decimal():
    """Decimal values are serialized as floats."""
    d = decimal.Decimal("3.14159")
    result = json.loads(json.dumps({"d": d}, cls=_ExtendedEncoder))
    assert abs(result["d"] - 3.14159) < 1e-5


def test_extended_encoder_uuid():
    """UUID values are serialized as strings."""
    u = uuid.UUID("12345678-1234-5678-1234-567812345678")
    result = json.loads(json.dumps({"u": u}, cls=_ExtendedEncoder))
    assert result["u"] == "12345678-1234-5678-1234-567812345678"


def test_extended_encoder_set():
    """Sets are serialized as lists."""
    s = {1, 2, 3}
    result = json.loads(json.dumps({"s": s}, cls=_ExtendedEncoder))
    assert sorted(result["s"]) == [1, 2, 3]


def test_extended_encoder_frozenset():
    """Frozensets are serialized as lists."""
    fs = frozenset(["a", "b"])
    result = json.loads(json.dumps({"fs": fs}, cls=_ExtendedEncoder))
    assert sorted(result["fs"]) == ["a", "b"]


def test_extended_encoder_mixed():
    """Mixed DuckDB-returnable types in a single dict."""
    data = {
        "dt": datetime.datetime(2026, 1, 1),
        "dec": decimal.Decimal("99.99"),
        "uid": uuid.UUID("abcdef01-2345-6789-abcd-ef0123456789"),
        "tags": {"x", "y"},
        "raw": b"binary data",
    }
    result = json.loads(json.dumps(data, cls=_ExtendedEncoder))
    assert result["dt"] == "2026-01-01T00:00:00"
    assert abs(result["dec"] - 99.99) < 0.01
    assert result["uid"] == "abcdef01-2345-6789-abcd-ef0123456789"
    assert sorted(result["tags"]) == ["x", "y"]
    assert result["raw"] == "binary data"
