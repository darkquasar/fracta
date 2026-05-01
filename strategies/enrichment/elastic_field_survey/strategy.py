"""Survey Elasticsearch indices and their field mappings for knowledge graph enrichment."""

from fracta_strategies import Strategy, step


class ElasticFieldSurvey(Strategy):

    @step("Load index metadata from staged tables")
    def load_indices(self, ctx):
        """Read pre-staged es_indices table from DuckDB."""
        rows = ctx.duckdb.execute(
            "SELECT index, docs, size FROM es_indices"
        ).fetchall()
        return [
            {"index": row[0], "docs": row[1], "size": row[2]}
            for row in rows
        ]

    @step("Load field mappings from staged tables")
    def load_mappings(self, ctx, load_indices):
        """Read pre-staged es_mappings table and group by index."""
        results = []
        for idx_info in load_indices[:50]:
            index_name = idx_info["index"]
            fields = ctx.duckdb.execute(
                "SELECT field_name, field_type FROM es_mappings WHERE index = ?",
                [index_name],
            ).fetchall()
            results.append({
                "index": index_name,
                "docs": idx_info["docs"],
                "field_count": len(fields),
                "fields": [
                    {"name": row[0], "type": row[1]}
                    for row in fields
                ],
            })
        return results

    @step("Build summary via DuckDB")
    def summarize(self, ctx, load_indices, load_mappings):
        """Aggregate index and mapping data into a summary."""
        total_indices = ctx.duckdb.execute(
            "SELECT count(*) FROM es_indices"
        ).fetchone()[0]
        total_docs = ctx.duckdb.execute(
            "SELECT sum(CAST(docs AS BIGINT)) FROM es_indices"
        ).fetchone()[0]

        return {
            "total_indices": total_indices,
            "total_docs": total_docs or 0,
            "indices": load_mappings,
        }
