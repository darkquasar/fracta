
## Knowledge Graph Protocol (MANDATORY)

You have access to graph tools. Every investigation or external data query MUST update the knowledge graph.

### Orientation
Call **graph_schema** at the start of your task to understand existing node/relationship types.
Use **search_tool** to find callable tools by concept, source, or strategy.

### 4-tier resolution chain
```
DomainSource -[:STORED_IN]-> DataStore -[:QUERYABLE_VIA]-> MCPServer -[:PROVIDES]-> MCPTool -[:RETURNS_FIELD]-> MCPField
  (what)                      (where)                       (server)                (callable)                 (schema)
```

### What YOU create vs what the system manages
**You create** (scaffold/discovered — these persist and grow):
- `DomainSource` — a named data stream (e.g., "AWS CloudTrail", "VendorSecurity Alerts")
- `DataStore` — where data physically lives, keyed by URI
- `MCPField` — fields a tool returns, with semantic annotations
- `Semantic` — vocabulary concepts used as semantic property values
- Edges: STORED_IN (DomainSource→DataStore), HAS_FIELD, RETURNS_FIELD
- All discovered entities: Hunt, System, Identity, IP, Event, Finding

**The reconciler manages** (inventory — do NOT create these):
- `MCPServer` — created automatically from fracta.yaml config
- `MCPTool` — created automatically when backends connect
- Edge: PROVIDES (MCPServer→MCPTool)

**You wire** (connecting your knowledge to inventory):
- `QUERYABLE_VIA` — from a DataStore you created to an existing MCPServer
- `STORED_IN` — from a DomainSource you created to a DataStore

### DataStore URI patterns
- Elasticsearch: `elasticsearch://<config_key>/<index-pattern>` (e.g., `elasticsearch://elastic_audit/.ds-logs-audit-platform-*`)
- Snowflake: `snowflake://<account>/<db>/<schema>/<table>`
- S3: `s3://<bucket>/<prefix>/`
- Gateway-only (no physical storage): `fracta-mcp-gateway://<server>/` (e.g., `fracta-mcp-gateway://vendor/`)

### After every external tool query
Call **graph_checkpoint** immediately after querying any external source. Fix every gap it reports before continuing.

When you discover a new data source:
1. MERGE a DomainSource node (set `_source = 'agent:<your-name>'`)
2. MERGE a DataStore with the correct URI pattern
3. Wire: DomainSource -[:STORED_IN]-> DataStore
4. Wire: DataStore -[:QUERYABLE_VIA]-> MCPServer (MATCH the existing MCPServer by config_key)
5. For each key field the tool returns:
   ```cypher
   MERGE (f:MCPField {name: $field_name})
   SET f.type = $field_type, f.semantic = $semantic_concept
   WITH f
   MATCH (mt:MCPTool {name: $tool_name})
   MERGE (mt)-[:RETURNS_FIELD]->(f)
   ```
   - `name` (**required**) = the actual field name from the tool output
   - `type` (**required**) = the field data type (e.g. "keyword", "float", "text", "integer")
   - `semantic` = the vocabulary concept it represents

   Examples across domains:
   | Domain | name | type | semantic |
   |--------|------|------|----------|
   | Security | source.ip | ip | ip_address |
   | Genomics | gene_symbol | keyword | gene_identifier |
   | Finance | close_price | float | price |
   | Research | doi | keyword | document_identifier |
   | Observability | response_time_ms | float | latency |

   **Never create MCPField nodes without `name` and `type`.**

### After every finding
Create nodes for discovered entities: System, Identity, IP, Event, Hunt. Wire relationships appropriately.

### End of investigation
Call **graph_checkpoint** — confirm all_clear before declaring complete.
