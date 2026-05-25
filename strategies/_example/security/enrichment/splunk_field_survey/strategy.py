"""Survey Splunk indexes and their extracted fields for knowledge graph enrichment.

Three-step DAG, all DuckDB reads against pre-staged tables:

  1. load_indexes        — read splunk_indexes
  2. load_field_summary  — read splunk_field_summary, group by index+sourcetype
  3. summarize           — aggregate counts and emit a schema view

The strategy does no live Splunk calls — staging happens via the bindings
defined in binding.yaml. Per-environment tuning (index filter, search-time
window) is contract params threaded into the binding.
"""

from fracta_strategies import Strategy, step


class SplunkFieldSurvey(Strategy):

    @step("Load index metadata from staged tables")
    def load_indexes(self, ctx):
        """Read pre-staged splunk_indexes table from DuckDB."""
        rows = ctx.duckdb.execute(
            "SELECT index, event_count, size_bytes, earliest, latest "
            "FROM splunk_indexes"
        ).fetchall()
        return [
            {
                "index": row[0],
                "event_count": row[1],
                "size_bytes": row[2],
                "earliest": row[3],
                "latest": row[4],
            }
            for row in rows
        ]

    @step("Load field summary from staged tables")
    def load_field_summary(self, ctx, load_indexes):
        """Read pre-staged splunk_field_summary and group by index + sourcetype."""
        results = []
        # Cap fan-out so a wide deploy doesn't produce a 10MB summary.
        for idx_info in load_indexes[:50]:
            index_name = idx_info["index"]
            rows = ctx.duckdb.execute(
                "SELECT sourcetype, field_name, field_count, distinct_count "
                "FROM splunk_field_summary WHERE index = ?",
                [index_name],
            ).fetchall()
            # Group fields by sourcetype within the index.
            by_sourcetype: dict[str, list[dict]] = {}
            for sourcetype, field_name, field_count, distinct_count in rows:
                by_sourcetype.setdefault(sourcetype or "<unknown>", []).append({
                    "name": field_name,
                    "count": field_count,
                    "distinct": distinct_count,
                })
            results.append({
                "index": index_name,
                "event_count": idx_info["event_count"],
                "sourcetypes": [
                    {"name": st, "field_count": len(fields), "fields": fields}
                    for st, fields in sorted(by_sourcetype.items())
                ],
            })
        return results

    @step("Build summary via DuckDB")
    def summarize(self, ctx, load_indexes, load_field_summary):
        """Aggregate index and field-summary data into a single summary."""
        total_indexes = ctx.duckdb.execute(
            "SELECT count(*) FROM splunk_indexes"
        ).fetchone()[0]
        total_events = ctx.duckdb.execute(
            "SELECT sum(CAST(event_count AS BIGINT)) FROM splunk_indexes"
        ).fetchone()[0]
        total_sourcetypes = ctx.duckdb.execute(
            "SELECT count(DISTINCT sourcetype) FROM splunk_field_summary"
        ).fetchone()[0]

        return {
            "total_indexes": total_indexes,
            "total_events": total_events or 0,
            "total_sourcetypes": total_sourcetypes,
            "indexes": load_field_summary,
        }
