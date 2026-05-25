"""Strategy runner -- long-lived Python sidecar that executes strategies over a Unix socket."""

import argparse
import ast
import datetime
import glob as globmod
import importlib.util
import json
import os
import shutil
import socket
import sys


class _ExtendedEncoder(json.JSONEncoder):
    """JSON encoder that handles datetime, Decimal, UUID, numpy, set, and other non-serializable types."""
    def default(self, obj):
        import decimal
        import uuid

        if isinstance(obj, (datetime.datetime, datetime.date)):
            return obj.isoformat()
        if isinstance(obj, datetime.timedelta):
            return obj.total_seconds()
        if isinstance(obj, bytes):
            return obj.decode("utf-8", errors="replace")
        if isinstance(obj, decimal.Decimal):
            return float(obj)
        if isinstance(obj, uuid.UUID):
            return str(obj)
        if isinstance(obj, (set, frozenset)):
            return list(obj)
        # numpy scalar types (if numpy is available)
        try:
            import numpy as np
            if isinstance(obj, (np.integer,)):
                return int(obj)
            if isinstance(obj, (np.floating,)):
                return float(obj)
            if isinstance(obj, (np.bool_,)):
                return bool(obj)
            if isinstance(obj, np.ndarray):
                return obj.tolist()
        except ImportError:
            pass
        return super().default(obj)

import duckdb
import yaml


def _try_import_falkordb():
    """Import FalkorDB if available, return None otherwise."""
    try:
        from falkordb import FalkorDB
        return FalkorDB
    except ImportError:
        return None


def _invalidate_sibling_modules(strategy_path):
    """Pop sibling .py files from sys.modules before reloading a strategy.

    Strategies often have helpers next to strategy.py (render.py, merge.py).
    Those siblings get imported into sys.modules as top-level names — so when
    the strategy is reloaded (e.g. between strategy_run invocations on a
    long-lived runner), the helper modules stay cached and operator in-place
    edits to them are invisible. Walk the strategy directory and remove every
    sibling Python file's module entry before reloading. Closes Bug 22.
    """
    import sys

    strategy_dir = os.path.dirname(os.path.abspath(strategy_path))
    try:
        entries = os.listdir(strategy_dir)
    except OSError:
        return
    for entry in entries:
        if not entry.endswith(".py"):
            continue
        if entry in ("__init__.py", "strategy.py"):
            continue
        mod_name = entry[:-3]
        sys.modules.pop(mod_name, None)


def load_strategy_module(path):
    """Load a Python module from a file path.

    Before loading, evict sibling helper modules from sys.modules so live
    edits to render.py / merge.py / etc. take effect on the next run.
    """
    _invalidate_sibling_modules(path)
    spec = importlib.util.spec_from_file_location("strategy_mod", path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def _wrap_graph_with_execute_alias(graph):
    """Attach a deprecated graph.execute alias forwarding to graph.query.

    The FalkorDB Python SDK's Graph object only exposes .query(). Some
    older example strategies were authored against a hypothetical .execute()
    method that never shipped — this alias lets them run while emitting a
    DeprecationWarning so authors notice and migrate. Closes Bug 11 back-compat.
    """
    if graph is None or hasattr(graph, "_fracta_execute_aliased"):
        return graph
    import warnings

    original_query = graph.query

    def _execute(*args, **kwargs):
        warnings.warn(
            "Graph.execute() is deprecated; use Graph.query() instead. "
            "This alias is provided for backward compatibility with older "
            "strategy code and will be removed in a future release.",
            DeprecationWarning,
            stacklevel=2,
        )
        return original_query(*args, **kwargs)

    try:
        graph.execute = _execute
        graph._fracta_execute_aliased = True
    except (AttributeError, TypeError):
        # Some graph implementations forbid attribute assignment; skip silently.
        pass
    return graph


def discover_strategies(strategy_dir):
    """Scan for directory-based strategies (contract.yaml + strategy.py)."""
    strategies = []
    if not os.path.isdir(strategy_dir):
        return strategies

    for dirpath, dirnames, filenames in os.walk(strategy_dir):
        # Skip framework packages, caches, hidden dirs, venvs
        dirnames[:] = sorted(d for d in dirnames if not d.startswith(("_", ".")))

        # Directory with contract.yaml + strategy.py
        if "contract.yaml" in filenames and "strategy.py" in filenames:
            contract_path = os.path.join(dirpath, "contract.yaml")
            with open(contract_path, "r") as f:
                contract = yaml.safe_load(f)
            if contract and "name" in contract:
                rel_path = os.path.relpath(dirpath, strategy_dir)
                strategies.append({
                    "name": contract["name"],
                    "description": contract.get("description", ""),
                    "tags": contract.get("tags", []),
                    "params": contract.get("params", {}),
                    "requires": contract.get("requires", {}),
                    "discovery": contract.get("discovery", {}),
                    "version": str(contract.get("version", "")),
                    "file": os.path.join(rel_path, "strategy.py"),
                    "contract_path": os.path.join(rel_path, "contract.yaml"),
                })
            # Don't descend into this dir further
            dirnames[:] = []
            continue

    return strategies


def find_strategy_class(mod):
    """Find the Strategy subclass in a module (not Strategy itself)."""
    from fracta_strategies import Strategy

    for attr_name in dir(mod):
        attr = getattr(mod, attr_name)
        if (
            isinstance(attr, type)
            and issubclass(attr, Strategy)
            and attr is not Strategy
        ):
            return attr
    return None


def handle_list(strategy_dir):
    """Handle {"action": "list"} -- return all discovered strategies."""
    strategies = discover_strategies(strategy_dir)
    return {"status": "ok", "strategies": strategies}


def handle_describe(strategy_dir, name):
    """Handle {"action": "describe", "strategy": "name"} -- return full contract metadata."""
    strategies = discover_strategies(strategy_dir)
    for s in strategies:
        if s["name"] == name:
            return {"status": "ok", "strategy": s}
    return {"status": "error", "error": f"Strategy '{name}' not found"}


def handle_create(strategy_dir, name, code, contract_yaml, force=False):
    """Handle {"action": "create"} -- write a new strategy directory.

    Creates <strategy_dir>/<category>/<name>/contract.yaml + strategy.py

    Version-aware behavior:
    - If strategy doesn't exist: create it.
    - If strategy exists at same version: error unless force=True.
    - If strategy exists at lower version: archive old to .archive/<slug>/v<old>/, write new.
    """
    # Validate Python syntax
    try:
        ast.parse(code, filename=name + ".py")
    except SyntaxError as e:
        return {"status": "error", "error": f"Syntax error: {e}"}

    if not contract_yaml:
        return {"status": "error", "error": "Contract YAML is required"}

    try:
        contract = yaml.safe_load(contract_yaml)
    except yaml.YAMLError as e:
        return {"status": "error", "error": f"Invalid YAML: {e}"}

    if not isinstance(contract, dict):
        return {"status": "error", "error": "Contract must be a YAML mapping"}
    if "name" not in contract:
        return {"status": "error", "error": "Contract must have a 'name' field"}
    if "description" not in contract:
        return {"status": "error", "error": "Contract must have a 'description' field"}
    if "tags" not in contract or not contract["tags"]:
        return {"status": "error", "error": "Contract must have at least one tag"}

    new_version = str(contract.get("version", "1"))

    # Determine subdirectory from first tag
    category = contract["tags"][0]
    slug = name.replace("-", "_")
    strategy_path = os.path.join(strategy_dir, category, slug)

    if os.path.exists(strategy_path):
        # Read existing version from contract.yaml
        existing_contract_path = os.path.join(strategy_path, "contract.yaml")
        existing_version = ""
        if os.path.exists(existing_contract_path):
            with open(existing_contract_path, "r") as f:
                existing_contract = yaml.safe_load(f)
            if existing_contract:
                existing_version = str(existing_contract.get("version", "1"))

        if existing_version == new_version and not force:
            return {
                "status": "error",
                "error": (
                    f"Strategy '{name}' already exists at version {existing_version}. "
                    f"Use force=true to overwrite, or bump the version in contract.yaml."
                ),
            }

        if existing_version and existing_version != new_version:
            # Archive old version before writing new one
            archive_dir = os.path.join(strategy_dir, ".archive", slug, f"v{existing_version}")
            os.makedirs(archive_dir, exist_ok=True)
            for fname in os.listdir(strategy_path):
                src = os.path.join(strategy_path, fname)
                dst = os.path.join(archive_dir, fname)
                if os.path.isfile(src):
                    shutil.copy2(src, dst)

        # Overwrite: force=True or version bump — clear and rewrite
        for fname in os.listdir(strategy_path):
            fpath = os.path.join(strategy_path, fname)
            if os.path.isfile(fpath):
                os.unlink(fpath)
    else:
        os.makedirs(strategy_path, exist_ok=True)

    with open(os.path.join(strategy_path, "contract.yaml"), "w") as f:
        f.write(contract_yaml)
    with open(os.path.join(strategy_path, "strategy.py"), "w") as f:
        f.write(code)

    rel_path = os.path.relpath(strategy_path, strategy_dir)
    return {
        "status": "ok",
        "directory": rel_path,
        "name": contract["name"],
        "version": new_version,
        "tags": contract.get("tags", []),
        "description": contract.get("description", ""),
        "sources": contract.get("requires", {}).get("sources", []),
    }


DEFAULT_STAGING_DIR = "/tmp/fracta-staging"
CONN_IDLE_TIMEOUT = 1800  # 30 min — covers longest background staging periods


def cleanup_stale_parquet(staging_dir=DEFAULT_STAGING_DIR):
    """Remove leftover Parquet files and run-id directories from previous runs."""
    if not os.path.isdir(staging_dir):
        return
    for name in os.listdir(staging_dir):
        path = os.path.join(staging_dir, name)
        if name.endswith(".parquet"):
            # Legacy flat Parquet files
            try:
                os.unlink(path)
            except OSError:
                pass
        elif os.path.isdir(path):
            # Run-id directories from crashed runs
            try:
                shutil.rmtree(path)
            except OSError:
                pass


def validate_staging_contract(db, contract):
    """Validate that staged DuckDB tables match the contract's requirements.

    Returns a list of error strings (empty if all required tables and columns present).
    """
    requires = contract.get("requires", {})
    required_tables = requires.get("tables", {})
    errors = []

    staged = set()
    try:
        staged = {row[0] for row in db.execute(
            "SELECT table_name FROM information_schema.tables WHERE table_schema='main'"
        ).fetchall()}
    except Exception:
        pass

    for table_name, table_spec in required_tables.items():
        if table_spec.get("optional", False):
            continue
        if table_name not in staged:
            errors.append(f"Required table '{table_name}' not staged")
            continue
        actual_cols = {row[0] for row in db.execute(
            "SELECT column_name FROM information_schema.columns WHERE table_name = ?",
            [table_name]
        ).fetchall()}
        for col_name in table_spec.get("columns", {}):
            if col_name not in actual_cols:
                errors.append(f"Table '{table_name}' missing column '{col_name}'")

    return errors


def validate_with_manifest(db, manifest):
    """Validate staged tables against the staging manifest.

    Returns a list of error strings (empty if all required tables are present and valid).
    """
    errors = []

    staged_tables = set()
    try:
        staged_tables = {row[0] for row in db.execute(
            "SELECT table_name FROM information_schema.tables WHERE table_schema='main'"
        ).fetchall()}
    except Exception:
        pass

    # Also count views
    try:
        staged_views = {row[0] for row in db.execute(
            "SELECT view_name FROM information_schema.tables WHERE table_schema='main' AND table_type='VIEW'"
        ).fetchall()}
        staged_tables |= staged_views
    except Exception:
        pass

    for table, meta in manifest.items():
        mode = meta.get("mode", "")
        required = meta.get("required", True)
        is_staged = meta.get("staged", False)

        # Native tables that are not staged are populated at runtime — skip validation
        if mode == "native" and not is_staged:
            continue

        if is_staged:
            if table not in staged_tables:
                errors.append(f"Staged table '{table}' not found in DuckDB")
            else:
                # Check expected columns exist in the staged table
                expected_columns = meta.get("columns", [])
                if expected_columns:
                    try:
                        actual_columns = {
                            row[0]
                            for row in db.execute(
                                f"SELECT column_name FROM information_schema.columns WHERE table_name = '{table}'"
                            ).fetchall()
                        }
                        for col in expected_columns:
                            if col not in actual_columns:
                                errors.append(f"Table '{table}' missing expected column '{col}'")
                    except duckdb.CatalogException:
                        pass  # table may be a VIEW with no metadata entry — skip column check
                    except Exception as e:
                        errors.append(f"Table '{table}' column validation failed: {e}")
        elif required:
            errors.append(f"Required table '{table}' not staged")

    return errors


def _load_parquet_table(db, tbl_name, parquet_path, lazy_threshold=50_000):
    """Load a Parquet file (or glob pattern) into DuckDB as a TABLE or VIEW."""
    if "*" in parquet_path:
        matching_files = globmod.glob(parquet_path)
        if not matching_files:
            return False
        row_count = sum(
            db.execute(
                "SELECT count(*) FROM parquet_metadata(?)", [f]
            ).fetchone()[0]
            for f in matching_files
        )
    elif os.path.exists(parquet_path):
        row_count = db.execute(
            "SELECT count(*) FROM parquet_metadata(?)", [parquet_path]
        ).fetchone()[0]
    else:
        return False

    # DuckDB doesn't support prepared parameters in CREATE VIEW ... AS SELECT FROM
    # read_parquet(?), so we use string interpolation. Parquet paths come from our
    # own staging system (not user input), so this is safe.
    escaped_path = parquet_path.replace("'", "''")
    if row_count > lazy_threshold:
        db.execute(
            f"CREATE VIEW \"{tbl_name}\" AS SELECT * FROM read_parquet('{escaped_path}')"
        )
    else:
        db.execute(
            f'CREATE TABLE "{tbl_name}" AS SELECT * FROM read_parquet(?)',
            [parquet_path],
        )
    return True


def _collect_run_dirs(manifest, staging_root=None):
    """Extract unique run-id directories from manifest parquet_paths.

    Only collects directories that:
    - Have a non-empty basename (not the staging root itself)
    - Are under staging_root when provided (safety against rmtree on arbitrary paths)
    """
    run_dirs = set()
    if not manifest:
        return run_dirs
    for meta in manifest.values():
        ppath = meta.get("parquet_path", "")
        if not ppath:
            continue
        parent = os.path.dirname(ppath)
        if not parent or not os.path.basename(parent):
            continue
        # Safety: if staging_root is known, verify parent is under it
        if staging_root:
            try:
                real_parent = os.path.realpath(parent)
                real_root = os.path.realpath(staging_root)
                if not real_parent.startswith(real_root + os.sep):
                    continue  # skip — not under staging root
            except (OSError, ValueError):
                continue
        run_dirs.add(parent)
    return run_dirs


def handle_run(strategy_dir, name, params, graph_client, graph_name="fracta_knowledge",
               staging_manifest=None, staging_dir=None,
               gateway_url=None, agent_task=None):
    """Handle {"action": "run"} -- load, instantiate, execute a strategy."""
    from fracta_strategies import StrategyContext

    strategies = discover_strategies(strategy_dir)
    target = None
    for s in strategies:
        if s["name"] == name:
            target = s
            break

    if not target:
        return {"status": "error", "error": f"Strategy '{name}' not found"}

    target_file = target["file"]
    path = os.path.join(strategy_dir, target_file)
    mod = load_strategy_module(path)

    strategy_cls = find_strategy_class(mod)
    if not strategy_cls:
        return {"status": "error", "error": f"No Strategy subclass found in {target_file}"}

    # Fresh DuckDB connection per execution for isolation
    db = duckdb.connect()
    db.execute("SET memory_limit = '400MB'")
    os.makedirs("/tmp/duckdb-spill", exist_ok=True)
    db.execute("SET temp_directory = '/tmp/duckdb-spill'")

    if staging_manifest:
        # Manifest-based loading: load tables according to their mode and staging status.
        for tbl_name, meta in staging_manifest.items():
            mode = meta.get("mode", "")
            is_staged = meta.get("staged", False)
            parquet_path = meta.get("parquet_path", "")

            if mode == "native" and not is_staged:
                continue  # strategy populates at runtime

            if is_staged and parquet_path:
                _load_parquet_table(db, tbl_name, parquet_path)

    # Validate staging
    if staging_manifest:
        validation_errors = validate_with_manifest(db, staging_manifest)
    else:
        contract_path = os.path.join(strategy_dir, target["contract_path"])
        with open(contract_path, "r") as f:
            contract_data = yaml.safe_load(f)
        validation_errors = validate_staging_contract(db, contract_data)
    if validation_errors:
        db.close()
        return {"status": "error", "error": "; ".join(validation_errors)}

    # Resolve graph handle: if FalkorDB client exists, select the default graph
    graph = None
    if graph_client is not None:
        graph = graph_client.select_graph(graph_name)
        graph = _wrap_graph_with_execute_alias(graph)

    # Create MCP gateway client if both URL and task are available
    mcp_client = None
    if gateway_url and agent_task:
        from fracta_strategies.mcp_client import MCPGatewayClient
        mcp_client = MCPGatewayClient(gateway_url, agent_task)

    # Bug 10: enforce requires.mcp at load time. Refuse to start a strategy
    # that declares requires.mcp: true when mcp_client is None — that path
    # silently passed None and failed per-call mid-run, hiding the root cause
    # (gateway_access not configured or runner not wired with --gateway-url).
    contract_path = os.path.join(strategy_dir, target["contract_path"])
    with open(contract_path, "r") as f:
        contract_for_requires = yaml.safe_load(f) or {}
    requires_mcp = (
        bool(contract_for_requires.get("requires", {}).get("mcp", False))
    )
    if requires_mcp and mcp_client is None:
        db.close()
        return {
            "status": "error",
            "error": (
                f"Strategy '{name}' declares requires.mcp: true but ctx.mcp is None. "
                "Configure strategy.gateway_access: true in the gateway config, and "
                "ensure the strategy runner is started with --gateway-url and "
                "--agent-task (or that they arrive in the per-request payload)."
            ),
        }

    ctx = StrategyContext(
        graph=graph,
        duckdb=db,
        params=params,
        mcp=mcp_client,
    )

    # Collect run directories for cleanup before execution (in case manifest
    # references get lost on error).
    effective_staging_dir = staging_dir or DEFAULT_STAGING_DIR
    run_dirs = _collect_run_dirs(staging_manifest, staging_root=effective_staging_dir)

    instance = strategy_cls()
    try:
        result = instance.execute(ctx)
    finally:
        db.close()
        # Clean up run-id directories (manifest-based, one rmtree per run)
        for run_dir in run_dirs:
            if os.path.isdir(run_dir):
                shutil.rmtree(run_dir, ignore_errors=True)

    if result.get("trace", {}).get("error"):
        resp = {
            "status": "error",
            "error": result["trace"]["error"],
            "result": result["result"],
            "trace": result["trace"],
        }
        if "partial_results" in result:
            resp["partial_results"] = result["partial_results"]
        if result.get("partial_results_truncated"):
            resp["partial_results_truncated"] = True
            resp["omitted_steps"] = result.get("omitted_steps", [])
        return resp

    return {
        "status": "ok",
        "result": result["result"],
        "trace": result["trace"],
    }


def handle_request(request, strategy_dir, graph_client, graph_name="fracta_knowledge",
                   gateway_url=None, agent_task=None):
    """Route a request to the appropriate handler."""
    action = request.get("action")

    if action == "list":
        return handle_list(strategy_dir)
    elif action == "describe":
        return handle_describe(strategy_dir, request.get("strategy", ""))
    elif action == "create":
        return handle_create(
            strategy_dir,
            request.get("name", ""),
            request.get("code", ""),
            contract_yaml=request.get("contract"),
            force=request.get("force", False),
        )
    elif action == "run":
        # Per-request override: JSON request fields take precedence over CLI defaults.
        effective_gw_url = request.get("gateway_url", gateway_url)
        effective_agent_task = request.get("agent_task", agent_task)

        return handle_run(
            strategy_dir,
            request.get("strategy", ""),
            request.get("params", {}),
            graph_client,
            graph_name,
            staging_manifest=request.get("staging_manifest"),
            staging_dir=request.get("staging_dir"),
            gateway_url=effective_gw_url,
            agent_task=effective_agent_task,
        )
    else:
        return {"status": "error", "error": f"Unknown action: {action}"}


def handle_connection(conn, strategy_dir, graph_client, graph_name="fracta_knowledge",
                      gateway_url=None, agent_task=None):
    """Handle a single connection -- read newline-delimited JSON requests."""
    buf = b""
    while True:
        data = conn.recv(4096)
        if not data:
            break
        buf += data

        while b"\n" in buf:
            line, buf = buf.split(b"\n", 1)
            line = line.strip()
            if not line:
                continue

            try:
                request = json.loads(line)
            except json.JSONDecodeError as e:
                response = {"status": "error", "error": f"Invalid JSON: {e}"}
                conn.sendall(json.dumps(response, cls=_ExtendedEncoder).encode() + b"\n")
                continue

            response = handle_request(request, strategy_dir, graph_client, graph_name,
                                      gateway_url=gateway_url, agent_task=agent_task)
            conn.sendall(json.dumps(response, cls=_ExtendedEncoder).encode() + b"\n")


def serve(sock_path, strategy_dir, graph_client, graph_name="fracta_knowledge",
          staging_dir=None, gateway_url=None, agent_task=None):
    """Main server loop -- listen on Unix socket, handle JSON requests."""
    # Clean up stale Parquet files and run directories from previous crashes
    cleanup_stale_parquet(staging_dir or DEFAULT_STAGING_DIR)

    # Clean up stale socket
    if os.path.exists(sock_path):
        os.unlink(sock_path)

    server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    server.bind(sock_path)
    server.listen(1)

    # Signal readiness to stdout (Go sidecar client watches for this)
    print(f"READY {sock_path}", flush=True)

    while True:
        conn, _ = server.accept()
        conn.settimeout(CONN_IDLE_TIMEOUT)
        try:
            handle_connection(conn, strategy_dir, graph_client, graph_name,
                              gateway_url=gateway_url, agent_task=agent_task)
        except Exception as e:
            sys.stderr.write(f"Connection error: {e}\n")
        finally:
            conn.close()


def main():
    parser = argparse.ArgumentParser(description="fracta strategy runner sidecar")
    parser.add_argument(
        "--socket",
        default="/tmp/fracta-strategy.sock",
        help="Unix socket path (default: /tmp/fracta-strategy.sock)",
    )
    parser.add_argument(
        "--strategy-dir",
        default="./strategies",
        help="Directory containing strategy directories (default: ./strategies)",
    )
    parser.add_argument(
        "--graph-addr",
        default=None,
        help="FalkorDB address as host:port (e.g., localhost:6379). If omitted, graph is unavailable.",
    )
    parser.add_argument(
        "--graph-name",
        default="fracta_knowledge",
        help="FalkorDB graph name (default: fracta_knowledge)",
    )
    parser.add_argument(
        "--staging-dir",
        default=None,
        help="Staging directory for Parquet files (default: /tmp/fracta-staging)",
    )
    parser.add_argument(
        "--gateway-url",
        default=None,
        help="MCP gateway base URL for mid-execution tool calls (e.g. http://fracta-gateway:8080)",
    )
    parser.add_argument(
        "--agent-task",
        default=None,
        help="Agent task name for gateway tool visibility scope (default for all runs)",
    )
    args = parser.parse_args()

    # Initialize FalkorDB client if address provided
    graph_client = None
    if args.graph_addr:
        FalkorDB = _try_import_falkordb()
        if FalkorDB is None:
            sys.stderr.write("Warning: --graph-addr provided but falkordb package not installed\n")
        else:
            host, port_str = args.graph_addr.rsplit(":", 1)
            try:
                graph_client = FalkorDB(host=host, port=int(port_str))
                sys.stderr.write(f"Connected to FalkorDB at {args.graph_addr}\n")
            except Exception as e:
                sys.stderr.write(f"Warning: could not connect to FalkorDB at {args.graph_addr}: {e}\n")

    serve(args.socket, args.strategy_dir, graph_client, args.graph_name,
          staging_dir=args.staging_dir,
          gateway_url=args.gateway_url,
          agent_task=args.agent_task)


if __name__ == "__main__":
    main() 
