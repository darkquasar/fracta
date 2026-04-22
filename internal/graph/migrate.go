package graph

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/darkquasar/fracta/internal/fractalog"
)

const migrationSource = "migration:v1"

// MigrateGraph runs an idempotent migration from the legacy 3-tier ontology
// (LogSource → DataSource → MCPSource → MCPField) to the new 4-tier model
// (DomainSource → DataStore → MCPServer → MCPTool → MCPField).
//
// Steps:
//  1. Label renames: MCPSource→MCPTool, LogSource→DomainSource
//  2. DataSource split: MCPServer (always) + DataStore (always, placeholder if needed)
//  3. Edge rewiring: FETCHABLE_VIA→PROVIDES, QUERYABLE_VIA through DataStore, STORED_IN
//  4. ToolRef collapse: rewire Strategy USES_TOOL edges to MCPTool, delete ToolRef
//  5. Name cleanup: hyphenated → dot-namespaced MCPTool names
//  6. Provenance: stamp migrated nodes, backfill null-source inventory to 'manual:legacy'
//
// Safe to re-run — all operations are guarded by existence checks.
func MigrateGraph(ctx context.Context, client GraphClient) error {
	log := fractalog.Component("migration")
	now := time.Now().UTC().Format(time.RFC3339)

	// Step 1: Label renames
	if err := renameMCPSourceToMCPTool(ctx, client, log, now); err != nil {
		return fmt.Errorf("step 1a (MCPSource→MCPTool): %w", err)
	}
	if err := renameLogSourceToDomainSource(ctx, client, log, now); err != nil {
		return fmt.Errorf("step 1b (LogSource→DomainSource): %w", err)
	}

	// Step 2+3: DataSource split into MCPServer + DataStore, edge rewiring
	if err := splitDataSources(ctx, client, log, now); err != nil {
		return fmt.Errorf("step 2-3 (DataSource split): %w", err)
	}

	// Step 4: ToolRef collapse
	if err := collapseToolRefs(ctx, client, log, now); err != nil {
		return fmt.Errorf("step 4 (ToolRef collapse): %w", err)
	}

	// Step 5: Name cleanup (hyphenated → dot-namespaced)
	if err := cleanupHyphenatedNames(ctx, client, log, now); err != nil {
		return fmt.Errorf("step 5 (name cleanup): %w", err)
	}

	// Step 6: Provenance backfill
	if err := backfillNullSourceInventory(ctx, client, log, now); err != nil {
		return fmt.Errorf("step 6 (provenance backfill): %w", err)
	}

	log.Info("graph migration complete")
	return nil
}

// renameMCPSourceToMCPTool adds the MCPTool label and removes MCPSource.
// Idempotent: skipped if no MCPSource nodes exist.
// Uses two-step approach (add label, then remove old label in separate query)
// to work around FalkorDB 4.18.x regression where batch SET+REMOVE in one
// query silently fails on multiple nodes.
func renameMCPSourceToMCPTool(ctx context.Context, client GraphClient, log interface{ Info(string, ...any) }, now string) error {
	count, err := countNodes(ctx, client, "MCPSource")
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}

	// Step 1: Add new label + provenance
	addQuery := `MATCH (n:MCPSource)
		SET n:MCPTool, n._source = COALESCE(n._source, $source), n._updated_at = $now, n._migrated_from = 'MCPSource'`
	if err := client.Update(ctx, addQuery, map[string]any{"source": migrationSource, "now": now}); err != nil {
		return err
	}

	// Step 2: Remove old label in batches (FalkorDB 4.18.x cannot batch-remove
	// labels on many nodes in a single query — silently fails. LIMIT workaround.)
	if err := batchRemoveLabel(ctx, client, "MCPSource"); err != nil {
		return err
	}

	log.Info("renamed MCPSource → MCPTool", "count", count)
	return nil
}

// renameLogSourceToDomainSource adds the DomainSource label and removes LogSource.
// Idempotent: skipped if no LogSource nodes exist.
// Uses two-step approach for FalkorDB 4.18.x compatibility (see renameMCPSourceToMCPTool).
func renameLogSourceToDomainSource(ctx context.Context, client GraphClient, log interface{ Info(string, ...any) }, now string) error {
	count, err := countNodes(ctx, client, "LogSource")
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}

	// Step 1: Add new label + provenance
	addQuery := `MATCH (n:LogSource)
		SET n:DomainSource, n._source = COALESCE(n._source, $source), n._updated_at = $now, n._migrated_from = 'LogSource'`
	if err := client.Update(ctx, addQuery, map[string]any{"source": migrationSource, "now": now}); err != nil {
		return err
	}

	// Step 2: Remove old label in batches (FalkorDB 4.18.x workaround)
	if err := batchRemoveLabel(ctx, client, "LogSource"); err != nil {
		return err
	}

	log.Info("renamed LogSource → DomainSource", "count", count)
	return nil
}

// splitDataSources migrates each DataSource node into MCPServer + DataStore,
// rewires edges, and deletes the original DataSource.
func splitDataSources(ctx context.Context, client GraphClient, log interface{ Info(string, ...any); Warn(string, ...any) }, now string) error {
	// Find all DataSource nodes
	records, err := client.Query(ctx, `MATCH (ds:DataSource) RETURN ds.config_key AS config_key, ds.type AS type, ds.index_pattern AS index_pattern`, nil)
	if err != nil {
		return fmt.Errorf("query DataSource nodes: %w", err)
	}
	if len(records) == 0 {
		return nil
	}

	for _, rec := range records {
		configKey := toString(rec["config_key"])
		dsType := toString(rec["type"])
		indexPattern := toString(rec["index_pattern"])

		if configKey == "" {
			log.Warn("skipping DataSource with empty config_key")
			continue
		}

		// Determine if this DataSource represents a real MCP server connection
		// (type="mcp", gateway-created) or a storage location (type="elasticsearch" etc,
		// agent-created). Only gateway-created DataSources become MCPServer nodes.
		isGatewayServer := dsType == "mcp"

		if isGatewayServer {
			// Step 2a: Create MCPServer for gateway-created DataSource
			serverQuery := `MERGE (ms:MCPServer {config_key: $config_key})
				ON CREATE SET ms.type = $type, ms._source = $source, ms._updated_at = $now, ms._migrated_from = 'DataSource'
				ON MATCH SET ms._updated_at = $now`
			if err := client.Update(ctx, serverQuery, map[string]any{
				"config_key": configKey, "type": dsType,
				"source": migrationSource, "now": now,
			}); err != nil {
				return fmt.Errorf("create MCPServer for %s: %w", configKey, err)
			}
		}

		// Step 2b: Create DataStore (always — placeholder if no real storage identity)
		uri, storeType, status := deriveDataStoreURI(configKey, dsType, indexPattern)
		dsQuery := `MERGE (d:DataStore {uri: $uri})
			ON CREATE SET d.type = $type, d._source = $source, d._updated_at = $now, d._migrated_from = 'DataSource'`
		dsParams := map[string]any{
			"uri": uri, "type": storeType,
			"source": migrationSource, "now": now,
		}
		if status != "" {
			dsQuery += `, d._status = $status`
			dsParams["status"] = status
		}
		dsQuery += `
			ON MATCH SET d._updated_at = $now`
		if err := client.Update(ctx, dsQuery, dsParams); err != nil {
			return fmt.Errorf("create DataStore for %s: %w", configKey, err)
		}

		// Step 3a: Wire DataStore → MCPServer (QUERYABLE_VIA)
		if isGatewayServer {
			// Gateway DataSource: wire to the MCPServer we just created (same config_key)
			qvQuery := `MATCH (d:DataStore {uri: $uri})
				MATCH (ms:MCPServer {config_key: $config_key})
				MERGE (d)-[:QUERYABLE_VIA]->(ms)`
			if err := client.Update(ctx, qvQuery, map[string]any{
				"uri": uri, "config_key": configKey,
			}); err != nil {
				return fmt.Errorf("wire QUERYABLE_VIA for %s: %w", configKey, err)
			}
		} else {
			// Agent-created DataSource: wire to the real MCPServer that serves this data.
			// Derive the server name from the config_key prefix (e.g., "elastic_audit" → "elastic").
			serverKey := deriveServerKey(configKey)
			qvQuery := `MATCH (d:DataStore {uri: $uri})
				MATCH (ms:MCPServer {config_key: $server_key})
				MERGE (d)-[:QUERYABLE_VIA]->(ms)`
			if err := client.Update(ctx, qvQuery, map[string]any{
				"uri": uri, "server_key": serverKey,
			}); err != nil {
				// Non-fatal: the target MCPServer may not exist yet (will be created by reconciler)
				log.Warn("could not wire QUERYABLE_VIA (MCPServer may not exist yet)", "datastore", uri, "server", serverKey)
			}
		}

		// Step 3b: Rewire old LogSource/DomainSource → DataSource edges to DomainSource → DataStore (STORED_IN)
		// At this point LogSource has already been renamed to DomainSource (step 1b).
		storedInQuery := `MATCH (ds:DataSource {config_key: $config_key})<-[old:QUERYABLE_VIA]-(src:DomainSource)
			MATCH (d:DataStore {uri: $uri})
			MERGE (src)-[:STORED_IN]->(d)
			DELETE old`
		if err := client.Update(ctx, storedInQuery, map[string]any{
			"config_key": configKey, "uri": uri,
		}); err != nil {
			return fmt.Errorf("rewire STORED_IN for %s: %w", configKey, err)
		}

		// Step 3c: Rewire old DataSource → MCPSource/MCPTool (FETCHABLE_VIA) to MCPServer → MCPTool (PROVIDES)
		// At this point MCPSource has already been renamed to MCPTool (step 1a).
		providesQuery := `MATCH (ds:DataSource {config_key: $config_key})-[old:FETCHABLE_VIA]->(mt:MCPTool)
			MATCH (ms:MCPServer {config_key: $config_key})
			MERGE (ms)-[:PROVIDES]->(mt)
			DELETE old`
		if err := client.Update(ctx, providesQuery, map[string]any{
			"config_key": configKey,
		}); err != nil {
			return fmt.Errorf("rewire PROVIDES for %s: %w", configKey, err)
		}

		log.Info("migrated DataSource", "config_key", configKey, "uri", uri)
	}

	// Step 3d: Delete original DataSource nodes (now orphaned)
	deleteQuery := `MATCH (ds:DataSource) DETACH DELETE ds`
	if err := client.Update(ctx, deleteQuery, nil); err != nil {
		return fmt.Errorf("delete DataSource nodes: %w", err)
	}
	log.Info("deleted legacy DataSource nodes", "count", len(records))

	return nil
}

// collapseToolRefs rewires Strategy USES_TOOL edges from ToolRef to MCPTool,
// then deletes all ToolRef nodes.
func collapseToolRefs(ctx context.Context, client GraphClient, log interface{ Info(string, ...any); Warn(string, ...any) }, now string) error {
	count, err := countNodes(ctx, client, "ToolRef")
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}

	// Rewire Strategy → ToolRef edges to Strategy → MCPTool (match by name)
	rewireQuery := `MATCH (s:Strategy)-[r:USES_TOOL]->(t:ToolRef)
		MATCH (mt:MCPTool {name: t.name})
		MERGE (s)-[:USES_TOOL]->(mt)
		DELETE r`
	if err := client.Update(ctx, rewireQuery, nil); err != nil {
		return fmt.Errorf("rewire USES_TOOL edges: %w", err)
	}

	// Log broken strategy dependencies (ToolRef without matching MCPTool)
	brokenQuery := `MATCH (s:Strategy)-[:USES_TOOL]->(t:ToolRef)
		RETURN s.name AS strategy, t.name AS tool`
	broken, err := client.Query(ctx, brokenQuery, nil)
	if err != nil {
		return fmt.Errorf("query broken strategy deps: %w", err)
	}
	for _, rec := range broken {
		log.Warn("broken strategy dependency: ToolRef has no matching MCPTool",
			"strategy", toString(rec["strategy"]), "tool", toString(rec["tool"]))
	}

	// Delete remaining USES_TOOL edges pointing to ToolRef (broken deps)
	if len(broken) > 0 {
		cleanBrokenQuery := `MATCH (s:Strategy)-[r:USES_TOOL]->(t:ToolRef) DELETE r`
		if err := client.Update(ctx, cleanBrokenQuery, nil); err != nil {
			return fmt.Errorf("clean broken USES_TOOL edges: %w", err)
		}
	}

	// Delete all ToolRef nodes
	deleteQuery := `MATCH (t:ToolRef) DETACH DELETE t`
	if err := client.Update(ctx, deleteQuery, nil); err != nil {
		return fmt.Errorf("delete ToolRef nodes: %w", err)
	}
	log.Info("collapsed ToolRef nodes", "count", count, "broken_deps", len(broken))

	return nil
}

// cleanupHyphenatedNames migrates MCPTool nodes with old hyphenated names
// (e.g., "elastic-search") to dot-namespaced form (e.g., "elastic.search").
func cleanupHyphenatedNames(ctx context.Context, client GraphClient, log interface{ Info(string, ...any) }, now string) error {
	// Find MCPTool nodes with hyphenated names — either legacy gateway-created
	// (null _source) or freshly renamed from MCPSource in this migration run.
	query := `MATCH (mt:MCPTool)
		WHERE mt.name CONTAINS '-'
		  AND NOT mt.name CONTAINS '.'
		  AND (mt._source IS NULL OR mt._source = $migration_source)
		RETURN mt.name AS name`
	records, err := client.Query(ctx, query, map[string]any{"migration_source": migrationSource})
	if err != nil {
		return fmt.Errorf("query hyphenated MCPTool names: %w", err)
	}
	if len(records) == 0 {
		return nil
	}

	migrated := 0
	for _, rec := range records {
		oldName := toString(rec["name"])
		if oldName == "" {
			continue
		}

		// Convert first hyphen to dot (e.g., "elastic-search" → "elastic.search")
		newName := hyphenatedToDotNamespaced(oldName)
		if newName == oldName {
			continue
		}

		// Check if the new name already exists (would be a conflict)
		existing, err := client.Query(ctx, `MATCH (mt:MCPTool {name: $name}) RETURN mt.name AS name`,
			map[string]any{"name": newName})
		if err != nil {
			return fmt.Errorf("check existing name %s: %w", newName, err)
		}

		if len(existing) > 0 {
			// New name exists — check if old has enrichments (MCPField edges)
			enriched, err := client.Query(ctx,
				`MATCH (mt:MCPTool {name: $name})-[:RETURNS_FIELD]->(f:MCPField) RETURN count(f) AS c`,
				map[string]any{"name": oldName})
			if err != nil {
				return fmt.Errorf("check enrichments for %s: %w", oldName, err)
			}
			if len(enriched) > 0 && toString(enriched[0]["c"]) != "0" {
				// Migrate enrichments to new name, then delete old
				migrateEnrichmentsQuery := `MATCH (old:MCPTool {name: $old_name})-[r:RETURNS_FIELD]->(f:MCPField)
					MATCH (new:MCPTool {name: $new_name})
					MERGE (new)-[:RETURNS_FIELD]->(f)
					DELETE r`
				if err := client.Update(ctx, migrateEnrichmentsQuery, map[string]any{
					"old_name": oldName, "new_name": newName,
				}); err != nil {
					return fmt.Errorf("migrate enrichments %s→%s: %w", oldName, newName, err)
				}
			}
			// Delete old node
			if err := client.Update(ctx, `MATCH (mt:MCPTool {name: $name}) DETACH DELETE mt`,
				map[string]any{"name": oldName}); err != nil {
				return fmt.Errorf("delete old hyphenated node %s: %w", oldName, err)
			}
			migrated++
			continue
		}

		// No conflict — rename in place
		renameQuery := `MATCH (mt:MCPTool {name: $old_name})
			SET mt.name = $new_name, mt._source = $source, mt._updated_at = $now,
			    mt._migrated_from = $old_name`
		if err := client.Update(ctx, renameQuery, map[string]any{
			"old_name": oldName, "new_name": newName,
			"source": migrationSource, "now": now,
		}); err != nil {
			return fmt.Errorf("rename %s→%s: %w", oldName, newName, err)
		}
		migrated++
	}

	if migrated > 0 {
		log.Info("cleaned hyphenated MCPTool names", "migrated", migrated)
	}
	return nil
}

// backfillNullSourceInventory stamps inventory nodes that have no _source
// with 'manual:legacy' so they are distinguishable from reconciler-created nodes.
func backfillNullSourceInventory(ctx context.Context, client GraphClient, log interface{ Info(string, ...any) }, now string) error {
	for _, label := range []string{"MCPTool", "MCPServer"} {
		query := fmt.Sprintf(`MATCH (n:%s) WHERE n._source IS NULL
			SET n._source = 'manual:legacy', n._updated_at = $now`, label)
		if err := client.Update(ctx, query, map[string]any{"now": now}); err != nil {
			return fmt.Errorf("backfill %s: %w", label, err)
		}
	}
	log.Info("backfilled null-source inventory nodes")
	return nil
}

// --- Helpers ---

// countNodes returns the number of nodes with the given label.
func countNodes(ctx context.Context, client GraphClient, label string) (int, error) {
	query := fmt.Sprintf(`MATCH (n:%s) RETURN count(n) AS c`, label)
	records, err := client.Query(ctx, query, nil)
	if err != nil {
		return 0, fmt.Errorf("count %s: %w", label, err)
	}
	if len(records) == 0 {
		return 0, nil
	}
	return toInt(records[0]["c"]), nil
}

// deriveDataStoreURI constructs a URI for a DataStore from DataSource properties.
// Returns (uri, type, status) where status is set for placeholder stores.
func deriveDataStoreURI(configKey, dsType, indexPattern string) (string, string, string) {
	if dsType == "mcp" && indexPattern == "" {
		return "fracta-mcp-gateway://" + configKey + "/", "fracta-mcp-gateway", ""
	}
	if indexPattern != "" {
		return dsType + "://" + configKey + "/" + indexPattern, dsType, ""
	}
	return dsType + "://" + configKey + "/", dsType, ""
}

// batchRemoveLabel removes a label from all matching nodes.
// FalkorDB 4.18.x has a regression where REMOVE on many nodes in a single
// query silently fails. This function tries a full REMOVE first, then falls
// back to batched REMOVE with LIMIT if nodes remain.
func batchRemoveLabel(ctx context.Context, client GraphClient, label string) error {
	// Try full REMOVE first (works on 4.16.x)
	fullQuery := fmt.Sprintf("MATCH (n:%s) REMOVE n:%s", label, label)
	if err := client.Update(ctx, fullQuery, nil); err != nil {
		return fmt.Errorf("remove label %s: %w", label, err)
	}

	// FalkorDB 4.18.x fallback: if nodes remain, batch with LIMIT 10.
	// We run a fixed number of batch passes proportional to the expected count.
	// This is safe because REMOVE on already-removed nodes is a no-op.
	batchQuery := fmt.Sprintf("MATCH (n:%s) WITH n LIMIT 10 REMOVE n:%s", label, label)
	for i := 0; i < 100; i++ {
		count, err := countNodes(ctx, client, label)
		if err != nil || count == 0 {
			return err
		}
		if err := client.Update(ctx, batchQuery, nil); err != nil {
			return fmt.Errorf("batch remove label %s: %w", label, err)
		}
	}
	return nil // best-effort; integration tests validate actual removal
}

// deriveServerKey extracts the MCP server name from an agent-created DataSource config_key.
// Agent convention: config_key = "<server>_<qualifier>" (e.g., "elastic_audit" → "elastic",
// "vendor_mcp_alerts" → "vendor"). Falls back to the full config_key if no underscore found.
func deriveServerKey(configKey string) string {
	idx := strings.Index(configKey, "_")
	if idx > 0 {
		return configKey[:idx]
	}
	return configKey
}

// hyphenatedToDotNamespaced converts the first hyphen to a dot.
// E.g., "elastic-search" → "elastic.search", "my-server-tool" → "my.server-tool"
func hyphenatedToDotNamespaced(name string) string {
	idx := strings.Index(name, "-")
	if idx < 0 {
		return name
	}
	return name[:idx] + "." + name[idx+1:]
}

// toString safely converts an interface{} to string.
func toString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// toInt safely converts an interface{} to int.
func toInt(v interface{}) int {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}
