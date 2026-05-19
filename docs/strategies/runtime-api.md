---
title: Strategy Runtime API
description: The StrategyContext fields and capabilities available inside @step methods
---

# Strategy Runtime API

Every `@step` method receives a `ctx` (StrategyContext) object. This page documents each field and how to use it effectively.

## ctx.duckdb — Columnar Data Engine

**Type:** `duckdb.DuckDBPyConnection`
**Always available:** Yes (fresh per-run, 400MB memory, spill-to-disk)

All pre-staged tables are loaded into this DuckDB instance before execution begins.

### Querying pre-staged tables

```python
@step("Count high-severity alerts")
def count_alerts(self, ctx):
    result = ctx.duckdb.execute("""
        SELECT severity, COUNT(*) as cnt
        FROM alerts
        WHERE severity >= ?
        GROUP BY severity
        ORDER BY cnt DESC
    """, [ctx.params.get("min_severity", 3)]).fetchall()
    return {"by_severity": [{"level": r[0], "count": r[1]} for r in result]}
```

### Creating intermediate tables

```python
@step("Build candidate set")
def build_candidates(self, ctx):
    ctx.duckdb.execute("""
        CREATE TABLE candidates AS
        SELECT DISTINCT src_ip, COUNT(*) as hit_count
        FROM dns_queries
        WHERE query_name LIKE '%.suspicious.tld'
        GROUP BY src_ip
        HAVING hit_count > 10
    """)
    return ctx.duckdb.execute("SELECT COUNT(*) FROM candidates").fetchone()[0]
```

### Inserting data mid-execution

Used with `ctx.mcp` to stage follow-up data into DuckDB for SQL joins:

```python
@step("Enrich and stage")
def enrich(self, ctx, find_ips):
    rows = []
    for ip in find_ips["suspicious"][:20]:
        result = ctx.mcp.call_tool("elastic.platform_core_search", {
            "query": f"source.ip:{ip}", "size": 100
        })
        for hit in result.get("hits", []):
            rows.append((ip, hit.get("action"), hit.get("timestamp")))

    if rows:
        ctx.duckdb.execute(
            "CREATE TABLE enrichment (ip VARCHAR, action VARCHAR, ts VARCHAR)"
        )
        ctx.duckdb.executemany("INSERT INTO enrichment VALUES (?, ?, ?)", rows)
    return {"staged": len(rows)}
```

### Best practices

- Use parameterized queries (`?` placeholders) to avoid SQL injection from params
- Prefer SQL for aggregation/joins over Python loops
- Tables persist across steps within the same run (fresh DuckDB per run)
- Memory limit is 400MB; DuckDB spills to disk automatically for large results

---

## ctx.graph — Knowledge Graph Access

**Type:** `FalkorDB graph client` or `None`
**Available when:** `requires.graph: true` in contract AND graph is configured

### Querying the graph

```python
@step("Find related systems")
def find_systems(self, ctx):
    if not ctx.graph:
        return {"error": "graph not available"}

    result = ctx.graph.query(
        "MATCH (ip:IP {address: $ip})-[:OBSERVED_ON]->(h:Host) "
        "RETURN h.hostname, h.os, h.last_seen",
        {"ip": ctx.params["target_ip"]}
    )
    return [{"host": r[0], "os": r[1], "last_seen": r[2]}
            for r in result.result_set]
```

### Writing to the graph

```python
@step("Record finding")
def record(self, ctx, correlate):
    if not ctx.graph or not correlate["confirmed"]:
        return {"recorded": False}

    ctx.graph.query(
        "MERGE (f:Finding {id: $id}) "
        "SET f.type = 'impossible_travel', f.severity = 'high', "
        "    f.detected_at = $ts",
        {"id": correlate["finding_id"], "ts": correlate["timestamp"]}
    )
    return {"recorded": True}
```

### Best practices

- Always check `if ctx.graph:` before using — strategies should degrade gracefully
- Use parameterized Cypher (`$param`) not f-strings to avoid injection
- Graph queries are for relationships and metadata, not bulk data

---

## ctx.mcp — Gateway Tool Access (Mid-Execution)

**Type:** `MCPGatewayClient` or `None`
**Available when:** Gateway is configured AND `strategy.gateway_access: true`

Call MCP tools through the fracta gateway during execution for targeted follow-up queries that depend on intermediate results.

### Listing available tools

```python
@step("Discover capabilities")
def discover(self, ctx):
    if not ctx.mcp:
        return {"tools": []}
    tools = ctx.mcp.list_tools()
    return {"tools": [t["name"] for t in tools]}
```

### Calling a tool

```python
@step("Fetch CloudTrail for suspicious identity")
def fetch_cloudtrail(self, ctx, identify_suspect):
    if not ctx.mcp:
        return {"skipped": "no gateway"}

    arn = identify_suspect["suspect_arn"]
    result = ctx.mcp.call_tool("elastic.platform_core_search", {
        "query": f"user.arn:{arn} AND event.action:ConsoleLogin",
        "index": "logs-aws.cloudtrail-*",
        "size": 50,
    })
    return {"logins": result.get("hits", [])}
```

### Error handling

```python
from fracta_strategies import MCPToolError, MCPGatewayConnectionError

@step("Safe enrichment")
def enrich(self, ctx, candidates):
    results = []
    for ip in candidates["ips"]:
        try:
            data = ctx.mcp.call_tool("vendor.search_alerts", {"ip": ip})
            results.append({"ip": ip, "alerts": data})
        except MCPToolError as e:
            results.append({"ip": ip, "error": str(e)})
        except MCPGatewayConnectionError:
            return {"partial": results, "error": "gateway down"}
    return {"enriched": results}
```

### Best practices

- Always check `if ctx.mcp:` — strategies must work without it (degrade, not crash)
- Use for **targeted** follow-ups (10s of queries), not bulk fetching (use bindings for that)
- Handle `MCPToolError` per-call for resilience
- Results from `ctx.mcp` are raw dicts — INSERT into DuckDB if you need SQL joins
- Tool names are dot-namespaced: `server.tool_name` (e.g., `elastic.platform_core_search`)

---

## ctx.params — Validated Input Parameters

**Type:** `dict`
**Always available:** Yes

Parameters passed by the caller, validated and type-coerced per the contract schema.

```python
@step("Filter by time range")
def filter(self, ctx):
    start = ctx.params["time_start"]   # guaranteed present (required in contract)
    threshold = ctx.params.get("severity_threshold", 50)  # uses contract default
    return ctx.duckdb.execute(
        "SELECT * FROM events WHERE ts >= ? AND severity >= ?",
        [start, threshold]
    ).fetchall()
```

### What the runtime guarantees

- Required params are present (error returned before execution if missing)
- Types are coerced: `"42"` → `42` for int params, `"true"` → `True` for bool
- Defaults from contract are applied for optional params not provided by caller
- Extra params not in the contract are silently dropped

---

## Combining Context Fields

```python
@step("Full investigation")
def investigate(self, ctx, detect_anomalies):
    # 1. Query pre-staged data (DuckDB)
    ips = ctx.duckdb.execute(
        "SELECT DISTINCT src_ip FROM anomalies WHERE score > ?",
        [ctx.params.get("threshold", 80)]
    ).fetchall()

    # 2. Enrich via knowledge graph
    known_bad = set()
    if ctx.graph:
        for (ip,) in ips:
            result = ctx.graph.query(
                "MATCH (i:IP {address: $ip})-[:TAGGED]->(t:Tag {name: 'malicious'}) RETURN i",
                {"ip": ip}
            )
            if result.result_set:
                known_bad.add(ip)

    # 3. Fetch targeted follow-up data via MCP
    new_findings = []
    if ctx.mcp:
        for (ip,) in ips:
            if ip in known_bad:
                continue
            try:
                data = ctx.mcp.call_tool("elastic.platform_core_search", {
                    "query": f"source.ip:{ip} AND event.category:network",
                    "size": 20
                })
                if data.get("hits"):
                    new_findings.append({"ip": ip, "connections": len(data["hits"])})
            except MCPToolError:
                pass

    return {
        "total_anomalies": len(ips),
        "known_malicious": list(known_bad),
        "new_findings": new_findings,
    }
``` 
