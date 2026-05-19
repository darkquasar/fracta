package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/darkquasar/fracta/internal/contract"
	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/loaders"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/resolve"
	"github.com/darkquasar/fracta/internal/staging"
	"github.com/darkquasar/fracta/internal/strategy"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerStrategyTools registers the strategy MCP tools on the given MCPServer.
// Called only when a Runner is available. gc, resolver, binding, sessions, runStore, and bus may be nil.
func registerStrategyTools(m *server.MCPServer, sc strategy.Runner, gc graph.GraphClient, autoPromote bool, resolver *resolve.Resolver, binding *contract.BindingSpec, fetcher *loaders.MCPFetcher, sessions *strategy.StagingSessionStore, runStore strategy.StagingRunStore, bus events.Bus) {
	m.AddTool(mcp.NewTool("strategy_list",
		mcp.WithDescription("List available hunt strategies. Returns name, description, tags, status, and parameter summary. "+
			"When the knowledge graph is connected, results include governance status and default to showing only "+
			"validated+promoted strategies. Use status=\"all\" to see exploratory strategies too."),
		mcp.WithString("tags",
			mcp.Description("Optional comma-separated tags to filter strategies (e.g. \"hunt,network\")"),
		),
		mcp.WithString("status",
			mcp.Description("Filter by governance status: \"exploratory\", \"validated\", \"promoted\", \"deprecated\", \"retired\", or \"all\". "+
				"Default when graph is connected: \"validated,promoted\". Default without graph: \"all\"."),
		),
	), makeStrategyListHandler(sc, gc, autoPromote))

	m.AddTool(mcp.NewTool("strategy_describe",
		mcp.WithDescription("Get full METADATA details for a specific strategy, including all parameters and their descriptions."),
		mcp.WithString("name",
			mcp.Description("Strategy name"),
			mcp.Required(),
		),
	), makeStrategyDescribeHandler(sc, gc, autoPromote, resolver, binding))

	m.AddTool(mcp.NewTool("strategy_run",
		mcp.WithDescription(
			"Execute a strategy by name. Automatically resolves and stages direct-mode data "+
				"requirements before execution. If MCP data is needed but not yet staged, returns "+
				"status 'pending' with a session_id and instructions — stage the pending tables via "+
				"strategy_stage (passing the session_id), then call strategy_run again with the same session_id. "+
				"Returns the result plus execution trace with per-step timings."),
		mcp.WithString("name",
			mcp.Description("Strategy name to execute"),
			mcp.Required(),
		),
		mcp.WithString("params",
			mcp.Description("Optional JSON string of parameters to pass to the strategy (e.g. {\"ip\": \"10.0.0.1\"})"),
		),
		mcp.WithString("session_id",
			mcp.Description("Session ID from a prior pending response. Merges agent-staged data from this session into the manifest."),
		),
	), makeStrategyRunHandler(sc, gc, resolver, binding, fetcher, sessions, runStore, bus))

	m.AddTool(mcp.NewTool("strategy_create",
		mcp.WithDescription("Register a new strategy from Python source code. Validates syntax, writes the strategy files, and optionally creates a Strategy node in the knowledge graph. Accepts either 'contract' (YAML) or 'metadata' (JSON) for strategy metadata."),
		mcp.WithString("name",
			mcp.Description("Strategy name (used as directory name, e.g. 'correlate-ip' becomes correlate_ip/)"),
			mcp.Required(),
		),
		mcp.WithString("code",
			mcp.Description("Full Python source code for the strategy. Must import from fracta_strategies: 'from fracta_strategies import Strategy, step'"),
			mcp.Required(),
		),
		mcp.WithString("contract",
			mcp.Description("YAML string for contract.yaml (preferred). Defines name, description, tags, params, requires, discovery."),
		),
		mcp.WithString("metadata",
			mcp.Description("JSON string of strategy METADATA (legacy). Use 'contract' parameter instead for new strategies."),
		),
		mcp.WithString("force",
			mcp.Description("Set to \"true\" to overwrite an existing strategy at the same version (development use)."),
		),
	), makeStrategyCreateHandler(sc, gc))

	m.AddTool(mcp.NewTool("strategy_stage",
		mcp.WithDescription("Write MCP results to staging as a named table. Data is written as Parquet files "+
			"scoped to a staging session. Pass session_id from a prior strategy_run 'pending' response to "+
			"associate data with that run. Use append=true for paginated data to accumulate multiple chunks."),
		mcp.WithString("table",
			mcp.Description("Table name in the staging database"),
			mcp.Required(),
		),
		mcp.WithString("columns",
			mcp.Description("JSON array of column names (e.g. [\"src_ip\", \"timestamp\", \"severity\"])"),
			mcp.Required(),
		),
		mcp.WithString("types",
			mcp.Description("Optional JSON array of DuckDB column types (e.g. [\"VARCHAR\", \"TIMESTAMP\", \"BIGINT\"]). When provided, enables typed Parquet columns instead of all-VARCHAR."),
		),
		mcp.WithString("data",
			mcp.Description("JSON array of row arrays (e.g. [[\"10.0.0.1\", \"2026-03-24T00:00:00Z\", \"high\"]])"),
			mcp.Required(),
		),
		mcp.WithString("append",
			mcp.Description("Set to \"true\" to append as a chunk (for paginated data). Each call writes a separate chunk file; all chunks are loaded together at run time."),
		),
		mcp.WithString("session_id",
			mcp.Description("Session ID from a prior strategy_run 'pending' response. Required for concurrent staging workflows."),
		),
	), makeStrategyStageHandler(sc, sessions, runStore))

	// strategy_stage_status: query staging run status (requires run store).
	if runStore != nil {
		m.AddTool(mcp.NewTool("strategy_stage_status",
			mcp.WithDescription("Query the status of a staging run. Returns run-level status, "+
				"per-table progress including rows staged, pages completed, partial data flags, "+
				"retry counts, and what each table is waiting on. Use session_id from a prior "+
				"strategy_run response."),
			mcp.WithString("session_id",
				mcp.Description("Session/run ID from a prior strategy_run response"),
				mcp.Required(),
			),
		), makeStrategyStageStatusHandler(runStore))
	}

	// strategy_resolve requires resolver + binding + loaders to be useful.
	if resolver != nil {
		m.AddTool(mcp.NewTool("strategy_resolve",
			mcp.WithDescription(
				"Resolve and stage data for a strategy. For 'direct' bindings, Go fetches "+
					"data and stages it as Parquet automatically. For 'mcp' bindings, returns "+
					"a plan the agent must follow. For 'strategy_native', indicates the strategy "+
					"will fetch its own data at runtime.",
			),
			mcp.WithString("name",
				mcp.Description("Strategy name"),
				mcp.Required(),
			),
			mcp.WithString("params",
				mcp.Description("JSON object of strategy parameters for query template interpolation"),
			),
			mcp.WithString("tables",
				mcp.Description("Optional JSON array of table names to resolve. Default: all tables in contract."),
			),
		), makeStrategyResolveHandler(sc, resolver, binding, fetcher))
	}

	// strategy_promote requires graph for status updates.
	if gc != nil {
		m.AddTool(mcp.NewTool("strategy_promote",
			mcp.WithDescription("Promote a validated strategy to promoted status. Only strategies in 'validated' status can be promoted. Promoted strategies are eligible for auto-execute and auto-recommend."),
			mcp.WithString("name",
				mcp.Description("Strategy name"),
				mcp.Required(),
			),
			mcp.WithString("version",
				mcp.Description("Strategy version to promote (default: current version)"),
			),
		), makeStrategyPromoteHandler(sc, gc, autoPromote))

		m.AddTool(mcp.NewTool("strategy_match",
			mcp.WithDescription(
				"Find strategies matching a situation. Queries the knowledge graph to rank strategies by "+
					"tag overlap (0.3 weight), semantic field coverage (0.5 weight), and data source overlap "+
					"(0.2 weight). Results weighted by composite_score. Use intent=auto_execute for strategies "+
					"safe to run without human review (promoted only); intent=recommend (default) includes "+
					"validated strategies for human-reviewed execution."),
			mcp.WithString("tags",
				mcp.Description("Comma-separated situation tags to match against strategy tags"),
			),
			mcp.WithString("semantics",
				mcp.Description("Comma-separated semantic types present in the situation (e.g. \"ip_address,hostname,identity_arn\")"),
			),
			mcp.WithString("sources",
				mcp.Description("Comma-separated log source names (e.g. \"CloudTrail,VPCFlowLogs\")"),
			),
			mcp.WithString("intent",
				mcp.Description("Lifecycle intent: \"recommend\" (default, returns validated+promoted) or \"auto_execute\" (promoted only, safe for no-human-in-the-loop)"),
			),
		), makeStrategyMatchHandler(gc, autoPromote))
	}
}

// strategyListEntry extends StrategyInfo with governance status from the graph.
type strategyListEntry struct {
	strategy.StrategyInfo
	Status string `json:"status,omitempty"`
}

func makeStrategyListHandler(sc strategy.Runner, gc graph.GraphClient, autoPromote bool) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		tagsStr := request.GetString("tags", "")
		var tags []string
		if tagsStr != "" {
			for _, t := range strings.Split(tagsStr, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					tags = append(tags, t)
				}
			}
		}

		strategies, err := sc.List(tags...)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("strategy list failed: %v", err)), nil
		}

		// Without graph: return as-is (no status filtering).
		if gc == nil {
			data, err := json.Marshal(strategies)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("marshalling strategies: %v", err)), nil
			}
			return mcp.NewToolResultText(string(data)), nil
		}

		// Parse status filter.
		// Default: show all statuses. Use status="validated,promoted" to restrict.
		statusParam := request.GetString("status", "")
		var allowedStatuses map[string]bool
		if statusParam != "" && statusParam != "all" {
			allowedStatuses = make(map[string]bool)
			for _, s := range strings.Split(statusParam, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					allowedStatuses[s] = true
				}
			}
		}
		// allowedStatuses == nil means "all" — no filtering.

		// Enrich with governance status from graph.
		results := make([]strategyListEntry, 0, len(strategies))
		for _, info := range strategies {
			version := info.Version
			if version == "" {
				version = "1"
			}
			status, err := resolveEffectiveStatus(ctx, gc, info.Name, version, autoPromote)
			if err != nil {
				status = StatusExploratory
			}
			if allowedStatuses != nil && !allowedStatuses[status] {
				continue
			}
			results = append(results, strategyListEntry{
				StrategyInfo: info,
				Status:       status,
			})
		}

		data, err := json.Marshal(results)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshalling strategies: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// describeResult extends StrategyInfo with governance status and resolution plan.
type describeResult struct {
	strategy.StrategyInfo
	Status         string                 `json:"status,omitempty"`
	ResolutionPlan *resolve.ResolutionPlan `json:"resolution_plan,omitempty"`
}

func makeStrategyDescribeHandler(sc strategy.Runner, gc graph.GraphClient, autoPromote bool, resolver *resolve.Resolver, binding *contract.BindingSpec) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, err := request.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: name"), nil
		}

		info, err := sc.Describe(name)
		if err != nil {
			if msg, ok := sidecarErrorMessage(err, sc, name); ok {
				return mcp.NewToolResultError(msg), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("strategy describe failed: %v", err)), nil
		}

		result := describeResult{StrategyInfo: *info}

		// Enrich with governance status from graph via shared helper.
		if gc != nil {
			version := info.Version
			if version == "" {
				version = "1"
			}
			if status, err := resolveEffectiveStatus(ctx, gc, name, version, autoPromote); err == nil {
				result.Status = status
			}
		}

		// If resolver is configured, try to produce a resolution plan.
		if resolver != nil {
			cs := strategyInfoToContractSpec(info)
			if cs != nil && len(cs.Requires.Tables) > 0 {
				plan, err := resolver.Resolve(ctx, cs, binding)
				if err == nil {
					result.ResolutionPlan = plan
				}
				// Non-fatal: describe still works without a resolution plan.
			}
		}

		data, err := json.Marshal(result)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshalling strategy: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

// loadEffectiveBinding loads a per-strategy binding.yaml (if present in the strategy
// directory) and merges it with the global binding. Per-strategy keys take precedence.
func loadEffectiveBinding(sc strategy.Runner, info *strategy.StrategyInfo, globalBinding *contract.BindingSpec) *contract.BindingSpec {
	if sc == nil || info == nil || info.ContractPath == "" {
		return globalBinding
	}
	strategyDir := filepath.Dir(filepath.Join(sc.StrategyDir(), info.ContractPath))
	bindingPath := filepath.Join(strategyDir, "binding.yaml")
	localBinding, err := contract.ParseBindingFile(bindingPath)
	if err != nil {
		return globalBinding
	}
	return mergeBindings(localBinding, globalBinding)
}

// mergeBindings produces a merged BindingSpec where local takes precedence over global.
func mergeBindings(local, global *contract.BindingSpec) *contract.BindingSpec {
	if local == nil {
		return global
	}
	if global == nil {
		return local
	}
	merged := &contract.BindingSpec{
		SourceBindings: make(map[string]contract.SourceBinding),
	}
	for k, v := range global.SourceBindings {
		merged.SourceBindings[k] = v
	}
	for k, v := range local.SourceBindings {
		merged.SourceBindings[k] = v
	}
	return merged
}

// strategyInfoToContractSpec builds a minimal ContractSpec from a StrategyInfo.
// Returns nil if the info doesn't contain enough data for resolution.
func strategyInfoToContractSpec(info *strategy.StrategyInfo) *contract.ContractSpec {
	if info == nil {
		return nil
	}

	cs := &contract.ContractSpec{
		Name:        info.Name,
		Description: info.Description,
		Tags:        info.Tags,
	}

	// Extract params: each entry like {"type": "string", "required": true, "default": "val"}.
	// Skip non-map entries (legacy flat-file strategies may have other keys like "requires").
	if info.Params != nil {
		cs.Params = make(map[string]contract.ParamSpec)
		for pName, pVal := range info.Params {
			pMap, ok := pVal.(map[string]any)
			if !ok {
				continue // skip non-param entries (e.g., legacy "requires" key)
			}
			ps := contract.ParamSpec{}
			if t, ok := pMap["type"].(string); ok {
				ps.Type = t
			}
			if r, ok := pMap["required"].(bool); ok {
				ps.Required = r
			}
			if d, exists := pMap["default"]; exists {
				ps.Default = d
			}
			if desc, ok := pMap["description"].(string); ok {
				ps.Description = desc
			}
			// Only add if it looks like a real param spec (has type or required).
			if ps.Type != "" || ps.Required || ps.Default != nil {
				cs.Params[pName] = ps
			}
		}
	}

	// Extract requires: prefer the dedicated Requires field (contract-based strategies),
	// fall back to Params["requires"] for legacy flat-file strategies.
	reqMap := info.Requires
	if reqMap == nil {
		if req, ok := info.Params["requires"]; ok {
			if rm, ok := req.(map[string]any); ok {
				reqMap = rm
			}
		}
	}
	if reqMap != nil {
		if g, ok := reqMap["graph"].(bool); ok {
			cs.Requires.Graph = g
		}
		if tables, ok := reqMap["tables"].(map[string]any); ok {
			cs.Requires.Tables = make(map[string]contract.TableSpec)
			for tName, tSpec := range tables {
				ts := contract.TableSpec{}
				if tMap, ok := tSpec.(map[string]any); ok {
					if opt, ok := tMap["optional"].(bool); ok {
						ts.Optional = opt
					}
					if cols, ok := tMap["columns"].(map[string]any); ok {
						ts.Columns = make(map[string]contract.ColumnSpec)
						for cName, cSpec := range cols {
							colSpec := contract.ColumnSpec{}
							if cMap, ok := cSpec.(map[string]any); ok {
								if t, ok := cMap["type"].(string); ok {
									colSpec.Type = t
								}
								if s, ok := cMap["semantic"].(string); ok {
									colSpec.Semantic = s
								}
							}
							ts.Columns[cName] = colSpec
						}
					}
				}
				cs.Requires.Tables[tName] = ts
			}
		}
	}

	return cs
}

// strategyRunResponse extends RunResult with auto-resolve information.
type strategyRunResponse struct {
	Status                  string              `json:"status"`                             // "complete", "staging", "pending", "executing", "error"
	SessionID               string              `json:"session_id,omitempty"`               // run ID (replaces staging session ID)
	Result                  any                 `json:"result,omitempty"`
	PartialResults          any                 `json:"partial_results,omitempty"`          // completed step outputs on error (S1)
	PartialResultsTruncated bool                `json:"partial_results_truncated,omitempty"` // true when partial results hit size limit
	OmittedSteps            []string            `json:"omitted_steps,omitempty"`            // step names dropped due to size
	Trace                   *strategy.TraceInfo `json:"trace,omitempty"`
	Error                   string              `json:"error,omitempty"`
	StructuredError         *strategy.StructuredError `json:"structured_error,omitempty"`   // categorized error (S7)
	Staged                  []resolveStaged     `json:"staged,omitempty"`                   // tables auto-resolved
	Pending                 []resolvePending    `json:"pending,omitempty"`                  // tables needing manual MCP fetch
	StagingProgress         map[string]*tableProgress `json:"staging_progress,omitempty"`   // per-table staging progress
	Message                 string              `json:"message,omitempty"`                  // guidance for pending/staging case
}

// tableProgress reports per-table staging status in the response.
type tableProgress struct {
	Status         string `json:"status"`
	RowsStaged     int64  `json:"rows_staged,omitempty"`
	PagesCompleted int    `json:"pages_completed,omitempty"`
	Partial        bool   `json:"partial,omitempty"`
	Error          string `json:"error,omitempty"`
}

func makeStrategyRunHandler(
	sc strategy.Runner,
	gc graph.GraphClient,
	resolver *resolve.Resolver,
	binding *contract.BindingSpec,
	fetcher *loaders.MCPFetcher,
	sessions *strategy.StagingSessionStore,
	runStore strategy.StagingRunStore,
	bus events.Bus,
) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, err := request.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: name"), nil
		}

		var params map[string]any
		paramsStr := request.GetString("params", "")
		if paramsStr != "" {
			if err := json.Unmarshal([]byte(paramsStr), &params); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid params JSON: %v", err)), nil
			}
		}

		sessionID := request.GetString("session_id", "")

		// --- Re-entry path: session_id provided → check existing run state ---
		if sessionID != "" && runStore != nil {
			return handleRunReentry(ctx, sc, gc, name, params, sessionID, sessions, runStore, bus)
		}

		// --- Legacy session lookup (backward compat while StagingSessionStore exists) ---
		var session *strategy.StagingSession
		if sessionID != "" && sessions != nil {
			var ok bool
			session, ok = sessions.Get(sessionID)
			if !ok {
				return mcp.NewToolResultError(fmt.Sprintf("session %q not found (expired or invalid)", sessionID)), nil
			}
		}

		// --- First call: resolve + dispatch ---
		var staged []resolveStaged
		var rr *resolveResult
		var cs *contract.ContractSpec
		if resolver != nil {
			info, err := sc.Describe(name)
			if err != nil {
				if msg, ok := sidecarErrorMessage(err, sc, name); ok {
					return mcp.NewToolResultError(msg), nil
				}
				return mcp.NewToolResultError(fmt.Sprintf("strategy %q not found: %v", name, err)), nil
			}

			cs = strategyInfoToContractSpec(info)

			// S9: normalize params (apply defaults, validate required, coerce types).
			if cs != nil {
				params, err = normalizeParams(params, cs)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("invalid parameters: %v", err)), nil
				}
			}

			// S9: session-params fingerprint validation (legacy session path).
			if session != nil && session.ParamsFingerprint != "" {
				fp := strategy.ComputeParamsFingerprint(params)
				if fp != "" && fp != session.ParamsFingerprint {
					return mcp.NewToolResultError(fmt.Sprintf(
						"session %s was created with different parameters — staged data may be inconsistent, start a new session",
						session.ID)), nil
				}
			}

			if cs != nil && len(cs.Requires.Tables) > 0 {
				effectiveBinding := loadEffectiveBinding(sc, info, binding)

				rr, err = resolveAndStage(ctx, sc, resolver, effectiveBinding, fetcher, cs, params, nil)
				if err != nil {
					resp := strategyRunResponse{
						Status: "error",
						Error:  fmt.Sprintf("auto-resolve failed: %v", err),
						StructuredError: &strategy.StructuredError{
							Message:   fmt.Sprintf("auto-resolve failed: %v", err),
							Category:  "transient",
							Retryable: true,
							Phase:     "resolution",
						},
					}
					data, _ := json.Marshal(resp)
					return mcp.NewToolResultText(string(data)), nil
				}
				staged = rr.Staged

				// Background staging path: large fracta_mcp_gateway tables need async fetch.
				if len(rr.Background) > 0 && runStore != nil && fetcher != nil {
					runID, _ := generateRunID()
					run := &strategy.StagingRun{
						ID:                runID,
						StrategyName:      name,
						Params:            params,
						ParamsFingerprint: strategy.ComputeParamsFingerprint(params),
						Status:            strategy.RunStatusStaging,
						Tables:            buildInitialTableStates(rr, cs),
						CreatedAt:         time.Now(),
						UpdatedAt:         time.Now(),
					}
					if cerr := runStore.Create(ctx, run); cerr != nil {
						fractalog.Component("strategy").Warn("failed to persist staging run", "error", cerr)
					}

					fractalog.Component("strategy").Info("staging.run.created",
						"run_id", runID, "strategy", name,
						"tables_background", len(rr.Background),
						"tables_inline", len(rr.Staged))
					emitStrategyEvent(bus, "run_created", "staging", runID, name, map[string]string{
						"tables_background": strconv.Itoa(len(rr.Background)),
					})

					// Launch background goroutines for large tables.
					startBackgroundStaging(run, rr.Background, effectiveBinding, params, fetcher, sc, runStore, bus)

					// Build progress + pending arrays for the response.
					progress := make(map[string]*tableProgress)
					for _, tp := range rr.Background {
						progress[tp.Table] = &tableProgress{Status: "fetching"}
					}
					for _, s := range rr.Staged {
						progress[s.Table] = &tableProgress{
							Status:     "staged",
							RowsStaged: s.RowsStaged,
						}
					}

					resp := strategyRunResponse{
						Status:          "staging",
						SessionID:       runID,
						Staged:          staged,
						StagingProgress: progress,
						Message: fmt.Sprintf(
							"%d table(s) staging in background. Poll with session_id %q.",
							len(rr.Background), runID),
					}

					// Include pending (mcp) tables if any.
					if len(rr.Pending) > 0 {
						resp.Pending = rr.Pending
					}

					data, _ := json.Marshal(resp)
					return mcp.NewToolResultText(string(data)), nil
				}

				// Check which pending tables are already staged in the session.
				if len(rr.Pending) > 0 {
					sessionTables := make(map[string]bool)
					if session != nil {
						for _, t := range session.Tables() {
							sessionTables[t] = true
						}
					}

					var actuallyPending []resolvePending
					for _, p := range rr.Pending {
						if !sessionTables[p.Table] {
							if ts, ok := cs.Requires.Tables[p.Table]; ok && ts.Optional {
								continue // skip optional un-staged tables
							}
							actuallyPending = append(actuallyPending, p)
						}
					}

					if len(actuallyPending) > 0 {
						// Create a run record for tracking (if store available).
						respSessionID := sessionID
						if respSessionID == "" && runStore != nil {
							runID, _ := generateRunID()
							run := &strategy.StagingRun{
								ID:                runID,
								StrategyName:      name,
								Params:            params,
								ParamsFingerprint: strategy.ComputeParamsFingerprint(params),
								Status:            strategy.RunStatusPending,
								Tables:            buildInitialTableStates(rr, cs),
								CreatedAt:         time.Now(),
								UpdatedAt:         time.Now(),
							}
							if cerr := runStore.Create(ctx, run); cerr != nil {
								fractalog.Component("strategy").Warn("failed to persist staging run", "error", cerr)
							}
							emitStrategyEvent(bus, "run_created", "pending", runID, name, map[string]string{
								"tables_pending": strconv.Itoa(len(actuallyPending)),
							})
							respSessionID = runID
						} else if respSessionID == "" && sessions != nil {
							// Legacy fallback
							newSession := sessions.Create()
							newSession.ParamsFingerprint = strategy.ComputeParamsFingerprint(params)
							respSessionID = newSession.ID
						}

						resp := strategyRunResponse{
							Status:    "pending",
							SessionID: respSessionID,
							Staged:    staged,
							Pending:   actuallyPending,
							Message: fmt.Sprintf(
								"%d table(s) require MCP data. Stage using session_id %q.",
								len(actuallyPending), respSessionID),
						}
						data, _ := json.Marshal(resp)
						return mcp.NewToolResultText(string(data)), nil
					}
				}
			}
		}

		// --- Fast path: all data resolved inline → execute immediately ---
		// Build staging manifest from resolve result for the runner.
		manifest := buildStagingManifest(rr, cs)

		// Merge agent-staged data from session into the manifest.
		if session != nil && manifest != nil {
			for table, path := range session.All() {
				if entry, ok := manifest[table]; ok && !entry.Staged {
					entry.Staged = true
					entry.ParquetPath = path
					manifest[table] = entry
				}
			}
		}

		// All data resolved — execute the strategy.
		runOpts := &strategy.RunOptions{AgentTask: agentTaskFromContext(ctx)}
		result, err := sc.Run(name, params, manifest, runOpts)
		if err != nil {
			emitStrategyEvent(bus, "run_complete", "failure", "", name, map[string]string{
				"phase": "execution",
				"error": err.Error(),
			})
			resp := strategyRunResponse{
				Status: "error",
				Error:  fmt.Sprintf("strategy run failed: %v", err),
				Staged: staged,
				StructuredError: classifyExecutionError(err, name),
			}
			data, _ := json.Marshal(resp)
			return mcp.NewToolResultText(string(data)), nil
		}

		// Terminal run: clean up session (success or failure).
		if session != nil && sessions != nil {
			sessions.Remove(session.ID)
		}

		// Fire-and-forget: record StrategyRun + update scoring in the graph (non-fatal).
		if gc != nil {
			version := ""
			if info, err := sc.Describe(name); err == nil {
				version = info.Version
			}
			stratLog := fractalog.Component("strategy")
			if rerr := recordStrategyRun(ctx, gc, name, version, result); rerr != nil {
				stratLog.Warn("failed to record strategy run in graph", "strategy", name, "error", rerr)
			} else if serr := updateStrategyScoring(ctx, gc, name, version); serr != nil {
				stratLog.Warn("failed to update strategy scoring", "strategy", name, "error", serr)
			}
		}

		// Map result status: "ok" → "complete" (S1 breaking change).
		status := result.Status
		if status == "ok" {
			status = "complete"
		}

		// Emit execution outcome event.
		if status == "complete" {
			emitStrategyEvent(bus, "run_complete", "success", "", name, nil)
		} else if status == "error" {
			emitStrategyEvent(bus, "run_complete", "failure", "", name, map[string]string{
				"phase": "execution",
			})
		}

		resp := strategyRunResponse{
			Status:                  status,
			Result:                  result.Result,
			PartialResults:          result.PartialResults,
			PartialResultsTruncated: result.PartialResultsTruncated,
			OmittedSteps:            result.OmittedSteps,
			Trace:                   &result.Trace,
			Error:                   result.Error,
			Staged:                  staged,
		}

		// Attach StructuredError when the strategy itself reports an error status.
		if status == "error" && result.Error != "" {
			resp.StructuredError = &strategy.StructuredError{
				Message:  result.Error,
				Category: "permanent",
				Phase:    "execution",
			}
		}

		data, err := json.Marshal(resp)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshalling result: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// handleRunReentry handles the re-entry path when a session_id is provided
// and a StagingRunStore is available. Checks run state, CAS-transitions to
// executing if all tables are staged, then runs the strategy.
func handleRunReentry(
	ctx context.Context,
	sc strategy.Runner,
	gc graph.GraphClient,
	name string,
	params map[string]any,
	runID string,
	sessions *strategy.StagingSessionStore,
	runStore strategy.StagingRunStore,
	bus events.Bus,
) (*mcp.CallToolResult, error) {
	log := fractalog.Component("strategy")

	run, err := runStore.Get(ctx, runID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to retrieve run %q: %v", runID, err)), nil
	}
	if run == nil {
		// Fallback: check legacy session store.
		if sessions != nil {
			if _, ok := sessions.Get(runID); ok {
				// Let the legacy path handle it — this shouldn't happen in normal flow
				// but provides backward compat during migration.
				return mcp.NewToolResultError(fmt.Sprintf("run %q not found in run store (legacy session exists — use strategy_stage to complete staging)", runID)), nil
			}
		}
		return mcp.NewToolResultError(fmt.Sprintf("run %q not found (expired or invalid)", runID)), nil
	}

	switch run.Status {
	case strategy.RunStatusComplete:
		// Already done — return cached result.
		resp := strategyRunResponse{
			Status: "complete",
			SessionID: runID,
		}
		if run.Result != nil {
			var result any
			_ = json.Unmarshal(run.Result, &result)
			resp.Result = result
		}
		if run.Trace != nil {
			var trace strategy.TraceInfo
			if err := json.Unmarshal(run.Trace, &trace); err == nil {
				resp.Trace = &trace
			}
		}
		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil

	case strategy.RunStatusFailed:
		resp := strategyRunResponse{
			Status:          "error",
			SessionID:       runID,
			StructuredError: run.Error,
		}
		if run.Error != nil {
			resp.Error = run.Error.Message
		}
		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil

	case strategy.RunStatusExecuting:
		resp := strategyRunResponse{
			Status:    "executing",
			SessionID: runID,
			Message:   "Strategy is currently executing. Poll again shortly.",
		}
		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil

	case strategy.RunStatusStaging:
		// Check error precedence: any required table failed/partial → immediate error.
		if errResp := checkRequiredTableErrors(run); errResp != nil {
			data, _ := json.Marshal(errResp)
			return mcp.NewToolResultText(string(data)), nil
		}
		// Still staging — return progress.
		resp := buildStagingProgressResponse(run)
		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil

	case strategy.RunStatusPending:
		// All auto-staging done; mcp tables awaiting agent.
		resp := buildPendingResponse(run)
		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil

	case strategy.RunStatusStaged:
		// All tables staged → CAS to executing and run.
		won, err := runStore.CASExecute(ctx, runID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("CAS execute failed: %v", err)), nil
		}
		if !won {
			// Lost the race — check if already complete.
			log.Debug("CAS execute lost race", "run_id", runID)
			updatedRun, _ := runStore.Get(ctx, runID)
			if updatedRun != nil && updatedRun.Status == strategy.RunStatusComplete {
				resp := strategyRunResponse{Status: "complete", SessionID: runID}
				if updatedRun.Result != nil {
					var result any
					_ = json.Unmarshal(updatedRun.Result, &result)
					resp.Result = result
				}
				data, _ := json.Marshal(resp)
				return mcp.NewToolResultText(string(data)), nil
			}
			resp := strategyRunResponse{
				Status:    "executing",
				SessionID: runID,
				Message:   "Another caller is executing this run. Poll again shortly.",
			}
			data, _ := json.Marshal(resp)
			return mcp.NewToolResultText(string(data)), nil
		}

		// Won CAS — execute strategy.
		log.Info("staging.run.execute",
			"run_id", runID, "strategy", run.StrategyName)
		manifest := buildManifestFromRun(run, sc)
		reentryOpts := &strategy.RunOptions{AgentTask: agentTaskFromContext(ctx)}
		result, runErr := sc.Run(run.StrategyName, run.Params, manifest, reentryOpts)
		if runErr != nil {
			// Mark as failed in store with structured error.
			structErr := classifyExecutionError(runErr, run.StrategyName)
			_ = runStore.FailRun(ctx, runID, structErr)
			log.Error("staging.run.failed",
				"run_id", runID, "strategy", run.StrategyName,
				"phase", "execution", "error", runErr)
			emitStrategyEvent(bus, "run_complete", "failure", runID, run.StrategyName, map[string]string{
				"phase": "execution",
			})
			resp := strategyRunResponse{
				Status:          "error",
				SessionID:       runID,
				Error:           fmt.Sprintf("strategy run failed: %v", runErr),
				StructuredError: structErr,
			}
			data, _ := json.Marshal(resp)
			return mcp.NewToolResultText(string(data)), nil
		}

		// Map result status: "ok" → "complete" (S1 breaking change).
		status := result.Status
		if status == "ok" {
			status = "complete"
		}

		// Determine terminal run status for persistence.
		terminalStatus := strategy.RunStatusComplete
		if status == "error" {
			terminalStatus = strategy.RunStatusFailed
			// Persist structured error so re-entry can return it.
			_ = runStore.FailRun(ctx, runID, &strategy.StructuredError{
				Message:  result.Error,
				Category: "permanent",
				Phase:    "execution",
			})
		}

		// Persist result + trace with correct terminal status.
		resultJSON, _ := json.Marshal(result.Result)
		traceJSON, _ := json.Marshal(result.Trace)
		if err := runStore.SetResult(ctx, runID, terminalStatus, resultJSON, traceJSON); err != nil {
			log.Warn("failed to persist run result", "run_id", runID, "error", err)
		}

		// Graph recording (non-fatal).
		if gc != nil {
			version := ""
			if info, err := sc.Describe(run.StrategyName); err == nil {
				version = info.Version
			}
			if rerr := recordStrategyRun(ctx, gc, run.StrategyName, version, result); rerr != nil {
				log.Warn("failed to record strategy run in graph", "strategy", run.StrategyName, "error", rerr)
			} else if serr := updateStrategyScoring(ctx, gc, run.StrategyName, version); serr != nil {
				log.Warn("failed to update strategy scoring", "strategy", run.StrategyName, "error", serr)
			}
		}

		// Emit execution outcome event.
		if status == "complete" {
			log.Info("staging.run.complete",
				"run_id", runID, "strategy", run.StrategyName)
			emitStrategyEvent(bus, "run_complete", "success", runID, run.StrategyName, nil)
		} else if status == "error" {
			emitStrategyEvent(bus, "run_complete", "failure", runID, run.StrategyName, map[string]string{
				"phase": "execution",
			})
		}

		resp := strategyRunResponse{
			Status:                  status,
			SessionID:               runID,
			Result:                  result.Result,
			PartialResults:          result.PartialResults,
			PartialResultsTruncated: result.PartialResultsTruncated,
			OmittedSteps:            result.OmittedSteps,
			Trace:                   &result.Trace,
			Error:                   result.Error,
		}

		// Attach StructuredError when the strategy itself reports an error status.
		if status == "error" && result.Error != "" {
			resp.StructuredError = &strategy.StructuredError{
				Message:  result.Error,
				Category: "permanent",
				Phase:    "execution",
			}
		}

		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil

	default:
		// RunStatusCreated — shouldn't normally be seen by callers.
		resp := strategyRunResponse{
			Status:    "staging",
			SessionID: runID,
			Message:   "Run is initializing.",
		}
		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// buildInitialTableStates creates TableState entries from resolve results.
func buildInitialTableStates(rr *resolveResult, cs *contract.ContractSpec) map[string]*strategy.TableState {
	tables := make(map[string]*strategy.TableState)
	if rr == nil {
		return tables
	}
	for _, s := range rr.Staged {
		required := true
		if cs != nil {
			if ts, ok := cs.Requires.Tables[s.Table]; ok {
				required = !ts.Optional
			}
		}
		tables[s.Table] = &strategy.TableState{
			Name:        s.Table,
			FetchMode:   s.FetchMode,
			Required:    required,
			Status:      strategy.TableStatusStaged,
			ParquetPath: s.ParquetPath,
			RowCount:    s.RowsStaged,
		}
	}
	for _, p := range rr.Pending {
		required := true
		if cs != nil {
			if ts, ok := cs.Requires.Tables[p.Table]; ok {
				required = !ts.Optional
			}
		}
		tables[p.Table] = &strategy.TableState{
			Name:      p.Table,
			FetchMode: p.FetchMode,
			Required:  required,
			Status:    strategy.TableStatusAwaitingAgent,
		}
	}
	for _, tp := range rr.Background {
		required := true
		if cs != nil {
			if ts, ok := cs.Requires.Tables[tp.Table]; ok {
				required = !ts.Optional
			}
		}
		tables[tp.Table] = &strategy.TableState{
			Name:      tp.Table,
			FetchMode: "fracta_mcp_gateway",
			Required:  required,
			Status:    strategy.TableStatusPending,
		}
	}
	return tables
}

// checkRequiredTableErrors checks if any required table has failed or partial status.
// Returns a structured error response if so, nil otherwise.
func checkRequiredTableErrors(run *strategy.StagingRun) *strategyRunResponse {
	for _, ts := range run.Tables {
		if !ts.Required {
			continue
		}
		if ts.Status == strategy.TableStatusFailed {
			errMsg := fmt.Sprintf("required table %q failed", ts.Name)
			if ts.Error != nil {
				errMsg = ts.Error.Message
			}
			return &strategyRunResponse{
				Status:    "error",
				SessionID: run.ID,
				Error:     errMsg,
				StructuredError: &strategy.StructuredError{
					Message:  errMsg,
					Category: "permanent",
					Phase:    "staging",
					Detail:   map[string]any{"table": ts.Name},
				},
			}
		}
		if ts.Partial {
			errMsg := fmt.Sprintf("required table %q has partial data", ts.Name)
			return &strategyRunResponse{
				Status:    "error",
				SessionID: run.ID,
				Error:     errMsg,
				StructuredError: &strategy.StructuredError{
					Message:  errMsg,
					Category: "partial",
					Phase:    "staging",
					Detail:   map[string]any{"table": ts.Name, "rows_staged": ts.RowCount},
				},
			}
		}
	}
	return nil
}

// buildStagingProgressResponse builds a "staging" status response with per-table progress.
func buildStagingProgressResponse(run *strategy.StagingRun) *strategyRunResponse {
	progress := make(map[string]*tableProgress)
	var pending []resolvePending
	for _, ts := range run.Tables {
		progress[ts.Name] = &tableProgress{
			Status:         string(ts.Status),
			RowsStaged:     ts.RowCount,
			PagesCompleted: ts.PagesCompleted,
			Partial:        ts.Partial,
		}
		if ts.Error != nil {
			progress[ts.Name].Error = ts.Error.Message
		}
		if ts.Status == strategy.TableStatusAwaitingAgent {
			pending = append(pending, resolvePending{
				Table:     ts.Name,
				FetchMode: ts.FetchMode,
			})
		}
	}
	return &strategyRunResponse{
		Status:          "staging",
		SessionID:       run.ID,
		StagingProgress: progress,
		Pending:         pending,
		Message:         "Background staging in progress. Poll again shortly.",
	}
}

// buildPendingResponse builds a "pending" status response for runs awaiting agent action.
func buildPendingResponse(run *strategy.StagingRun) *strategyRunResponse {
	var pending []resolvePending
	for _, ts := range run.Tables {
		if ts.Status == strategy.TableStatusAwaitingAgent {
			pending = append(pending, resolvePending{
				Table:     ts.Name,
				FetchMode: ts.FetchMode,
			})
		}
	}
	return &strategyRunResponse{
		Status:    "pending",
		SessionID: run.ID,
		Pending:   pending,
		Message:   fmt.Sprintf("%d table(s) require MCP data. Stage using session_id %q.", len(pending), run.ID),
	}
}

// buildManifestFromRun constructs a StagingManifest from a completed StagingRun.
func buildManifestFromRun(run *strategy.StagingRun, sc strategy.Runner) strategy.StagingManifest {
	manifest := make(strategy.StagingManifest)
	for tableName, ts := range run.Tables {
		if ts.Status == strategy.TableStatusStaged {
			manifest[tableName] = strategy.StagingManifestEntry{
				Staged:      true,
				ParquetPath: ts.ParquetPath,
				Partial:     ts.Partial,
			}
		} else if ts.Status == strategy.TableStatusSkipped {
			manifest[tableName] = strategy.StagingManifestEntry{
				Staged: false,
			}
		}
	}
	return manifest
}

func makeStrategyCreateHandler(sc strategy.Runner, gc graph.GraphClient) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, err := request.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: name"), nil
		}

		code, err := request.RequireString("code")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: code"), nil
		}

		// Accept either contract (YAML, preferred) or metadata (JSON, legacy).
		contractYAML := request.GetString("contract", "")
		metadata := request.GetString("metadata", "")
		force := request.GetString("force", "") == "true"

		if contractYAML == "" && metadata == "" {
			return mcp.NewToolResultError("either 'contract' (YAML) or 'metadata' (JSON) parameter is required"), nil
		}

		// If contract YAML is provided, use it for both sidecar creation and graph registration.
		if contractYAML != "" {
			// Parse contract to validate and extract fields for graph registration.
			cs, err := contract.ParseContract([]byte(contractYAML))
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid contract YAML: %v", err)), nil
			}

			// Send to sidecar with contract field.
			if err := sc.CreateWithContract(name, code, contractYAML, force); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("strategy create failed: %v", err)), nil
			}

			// Register in graph if available.
			if gc != nil {
				if err := createStrategyGraphNodesFromContract(ctx, gc, name, cs); err != nil {
					return mcp.NewToolResultText(fmt.Sprintf(
						"Strategy %q created successfully. Warning: graph update failed: %v", name, err,
					)), nil
				}
			}

			return mcp.NewToolResultText(fmt.Sprintf("Strategy %q created and registered.", name)), nil
		}

		// Legacy path: metadata JSON.
		if err := sc.Create(name, code, metadata, force); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("strategy create failed: %v", err)), nil
		}

		if gc != nil {
			if err := createStrategyGraphNodes(ctx, gc, name, metadata); err != nil {
				return mcp.NewToolResultText(fmt.Sprintf(
					"Strategy %q created successfully. Warning: graph update failed: %v", name, err,
				)), nil
			}
		}

		return mcp.NewToolResultText(fmt.Sprintf("Strategy %q created and registered.", name)), nil
	}
}

func makeStrategyPromoteHandler(sc strategy.Runner, gc graph.GraphClient, autoPromote bool) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, err := request.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: name"), nil
		}

		version := request.GetString("version", "")
		if version == "" {
			info, err := sc.Describe(name)
			if err != nil {
				if msg, ok := sidecarErrorMessage(err, sc, name); ok {
					return mcp.NewToolResultError(msg), nil
				}
				return mcp.NewToolResultError(fmt.Sprintf("strategy %q not found: %v", name, err)), nil
			}
			version = info.Version
			if version == "" {
				version = "1"
			}
		}

		// Resolve current effective status (applies any pending auto-transitions first).
		status, err := resolveEffectiveStatus(ctx, gc, name, version, autoPromote)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to resolve status: %v", err)), nil
		}

		if status != StatusValidated {
			return mcp.NewToolResultError(fmt.Sprintf(
				"strategy %q v%s is in status %q — only 'validated' strategies can be promoted",
				name, version, status,
			)), nil
		}

		// Promote: update both StrategyVersion and Strategy nodes.
		if err := gc.Update(ctx,
			"MATCH (v:StrategyVersion {name: $name, version: $version}) SET v.status = 'promoted' "+
				"WITH v MATCH (s:Strategy {name: $name}) WHERE s.version = $version SET s.status = 'promoted'",
			map[string]any{"name": name, "version": version},
		); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("promotion failed: %v", err)), nil
		}

		// Deprecate any previously promoted version — update both StrategyVersion
		// and Strategy.status to keep them in sync (same "both nodes authoritative" model).
		if err := gc.Update(ctx,
			"MATCH (v:StrategyVersion {name: $name}) "+
				"WHERE v.version <> $version AND v.status = 'promoted' "+
				"SET v.status = 'deprecated' "+
				"WITH v MATCH (s:Strategy {name: $name}) WHERE s.version = v.version "+
				"SET s.status = 'deprecated'",
			map[string]any{"name": name, "version": version},
		); err != nil {
			fractalog.Component("strategy").Warn("failed to deprecate old promoted versions", "name", name, "error", err)
		}

		return mcp.NewToolResultText(fmt.Sprintf("Strategy %q v%s promoted.", name, version)), nil
	}
}

// strategyMatchResult represents a single strategy match with scoring breakdown.
type strategyMatchResult struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Version     string  `json:"version"`
	Status      string  `json:"status"`
	Score       float64 `json:"score"`
	TagScore    float64 `json:"tag_score"`
	FieldScore  float64 `json:"field_score"`
	SourceScore float64 `json:"source_score"`
	Reasons     []string `json:"reasons"`
}

// strategyMatchResponse is the response contract for strategy_match.
// It echoes the applied intent so callers can verify the filter.
type strategyMatchResponse struct {
	Intent  string                `json:"intent"`
	Matches []strategyMatchResult `json:"matches"`
}

func makeStrategyMatchHandler(gc graph.GraphClient, autoPromote bool) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// intent: "recommend" (default) or "auto_execute"
		intent := request.GetString("intent", "recommend")
		if intent != "recommend" && intent != "auto_execute" {
			return mcp.NewToolResultError("intent must be \"recommend\" or \"auto_execute\""), nil
		}

		// Lifecycle filtering per spec §5.6.
		allowedStatuses := map[string]bool{StatusValidated: true, StatusPromoted: true}
		if intent == "auto_execute" {
			allowedStatuses = map[string]bool{StatusPromoted: true}
		}

		// Parse comma-separated input params.
		var tags, semantics, sources []string
		for _, pair := range []struct {
			param string
			dest  *[]string
		}{
			{request.GetString("tags", ""), &tags},
			{request.GetString("semantics", ""), &semantics},
			{request.GetString("sources", ""), &sources},
		} {
			if pair.param != "" {
				for _, s := range strings.Split(pair.param, ",") {
					s = strings.TrimSpace(s)
					if s != "" {
						*pair.dest = append(*pair.dest, strings.ToLower(s))
					}
				}
			}
		}

		// Query strategies with their graph context and current version's composite_score.
		rows, err := gc.Query(ctx,
			"MATCH (s:Strategy) "+
				"OPTIONAL MATCH (s)-[:HAS_VERSION]->(cv:StrategyVersion {version: s.version}) "+
				"OPTIONAL MATCH (s)-[:USES_SOURCE]->(d:DomainSource) "+
				"OPTIONAL MATCH (s)-[:USES_SOURCE]->(d2:DomainSource)-[:HAS_FIELD]->(ft:FieldType) "+
				"WITH s, cv, "+
				"collect(DISTINCT d.name) AS source_names, "+
				"collect(DISTINCT ft.semantic) AS field_semantics "+
				"RETURN s.name AS name, s.description AS description, s.tags AS tags, "+
				"s.version AS current_version, cv.composite_score AS composite_score, "+
				"source_names, field_semantics",
			nil,
		)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("graph query failed: %v", err)), nil
		}

		var matches []strategyMatchResult
		for _, row := range rows {
			name := stringVal(row, "name", "")
			if name == "" {
				continue
			}
			description := stringVal(row, "description", "")
			tagsStr := stringVal(row, "tags", "")
			currentVersion := stringVal(row, "current_version", "1")

			// Resolve effective status via shared helper.
			status, _ := resolveEffectiveStatus(ctx, gc, name, currentVersion, autoPromote)
			if !allowedStatuses[status] {
				continue
			}

			// Parse strategy tags.
			var stratTags []string
			if tagsStr != "" {
				for _, t := range strings.Split(tagsStr, ",") {
					t = strings.TrimSpace(t)
					if t != "" {
						stratTags = append(stratTags, strings.ToLower(t))
					}
				}
			}

			// Parse source names.
			var stratSources []string
			if arr, ok := row["source_names"].([]interface{}); ok {
				for _, v := range arr {
					if s, ok := v.(string); ok && s != "" {
						stratSources = append(stratSources, strings.ToLower(s))
					}
				}
			}

			// Parse semantic fields via Strategy→USES_SOURCE→DomainSource→HAS_FIELD→FieldType path.
			var stratSemantics []string
			if arr, ok := row["field_semantics"].([]interface{}); ok {
				for _, v := range arr {
					if s, ok := v.(string); ok && s != "" {
						stratSemantics = append(stratSemantics, strings.ToLower(s))
					}
				}
			}

			// Fetch composite_score from graph query result (default 1.0 if not scored yet).
			compositeScore := floatVal(row, "composite_score", 1.0)

			// Three-phase scoring per spec §5.5 weights: tag 0.3, semantic 0.5, source 0.2.
			var reasons []string
			tagScore := computeOverlapScore(tags, stratTags)
			if tagScore > 0 {
				reasons = append(reasons, fmt.Sprintf("tag overlap: %.0f%%", tagScore*100))
			}
			semanticScore := computeOverlapScore(semantics, stratSemantics)
			if semanticScore > 0 {
				reasons = append(reasons, fmt.Sprintf("semantic coverage: %.0f%%", semanticScore*100))
			}
			sourceScore := computeOverlapScore(sources, stratSources)
			if sourceScore > 0 {
				reasons = append(reasons, fmt.Sprintf("source overlap: %.0f%%", sourceScore*100))
			}

			// Weighted overlap per spec: 0.3*tag + 0.5*semantic + 0.2*source.
			// Then weighted by composite_score per spec §5.5: "Results weighted by composite_score."
			overlapScore := 0.3*tagScore + 0.5*semanticScore + 0.2*sourceScore
			if overlapScore <= 0 {
				continue
			}
			score := overlapScore * compositeScore

			matches = append(matches, strategyMatchResult{
				Name:        name,
				Description: description,
				Version:     currentVersion,
				Status:      status,
				Score:       score,
				TagScore:    tagScore,
				FieldScore:  semanticScore,
				SourceScore: sourceScore,
				Reasons:     reasons,
			})
		}

		sortStrategyMatches(matches)
		if len(matches) > 5 {
			matches = matches[:5]
		}

		resp := strategyMatchResponse{Intent: intent, Matches: matches}
		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// extractKeywords splits text into lowercase words, filtering out short stop words.
func extractKeywords(text string) []string {
	stopWords := map[string]bool{
		"a": true, "an": true, "the": true, "is": true, "in": true, "on": true,
		"of": true, "to": true, "for": true, "and": true, "or": true, "with": true,
		"from": true, "by": true, "at": true, "as": true, "it": true, "be": true,
		"this": true, "that": true, "are": true, "was": true, "do": true, "has": true,
	}
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-')
	})
	var keywords []string
	for _, w := range words {
		if len(w) >= 2 && !stopWords[w] {
			keywords = append(keywords, w)
		}
	}
	return keywords
}

// computeOverlapScore computes the fraction of query terms found in candidates.
// Returns 0.0 if query is empty.
func computeOverlapScore(query, candidates []string) float64 {
	if len(query) == 0 {
		return 0
	}
	candSet := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		candSet[c] = true
	}
	hits := 0
	for _, q := range query {
		if candSet[q] {
			hits++
			continue
		}
		// Substring match: check if query term is a substring of any candidate.
		for _, c := range candidates {
			if strings.Contains(c, q) || strings.Contains(q, c) {
				hits++
				break
			}
		}
	}
	return float64(hits) / float64(len(query))
}

// sortStrategyMatches sorts matches by score descending.
func sortStrategyMatches(matches []strategyMatchResult) {
	for i := 1; i < len(matches); i++ {
		for j := i; j > 0 && matches[j].Score > matches[j-1].Score; j-- {
			matches[j], matches[j-1] = matches[j-1], matches[j]
		}
	}
}

// countFindings applies the findings heuristic: count top-level map keys that match
// known finding patterns. Returns 0 for non-map results.
func countFindings(result any) int {
	resultMap, ok := result.(map[string]any)
	if !ok {
		// Try []any — count slice length if it looks like a findings list.
		if arr, ok := result.([]any); ok {
			return len(arr)
		}
		return 0
	}
	findingPatterns := []string{"finding", "alert", "hit", "match", "result", "detection", "event", "incident", "indicator"}
	count := 0
	for key := range resultMap {
		keyLower := strings.ToLower(key)
		for _, pattern := range findingPatterns {
			if strings.Contains(keyLower, pattern) {
				// Count the value: if it's a slice, count elements; otherwise count 1.
				if arr, ok := resultMap[key].([]any); ok {
					count += len(arr)
				} else {
					count++
				}
				break
			}
		}
	}
	return count
}

// recordStrategyRun creates a StrategyRun node linked to the StrategyVersion.
// This is fire-and-forget: errors are logged, not propagated.
func recordStrategyRun(ctx context.Context, gc graph.GraphClient, name, version string, result *strategy.RunResult) error {
	if version == "" {
		version = "1"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	runID, _ := generateRunID()

	ranToCompletion := result.Status == "ok"
	durationMs := result.Trace.TotalDurationMs
	stepCount := len(result.Trace.Steps)
	// Findings count heuristic: count top-level keys in result that match known finding
	// patterns (e.g., "finding", "alert", "hit", "match", "result", "detection", "event").
	// Non-map results and unrelated keys (e.g., "metadata", "summary") don't inflate scoring.
	findingsCount := countFindings(result.Result)
	status := "success"
	if !ranToCompletion {
		status = "failure"
	}

	return gc.Update(ctx,
		"MERGE (v:StrategyVersion {name: $name, version: $version}) "+
			"CREATE (r:StrategyRun {id: $id, started_at: $started_at, status: $status, "+
			"duration_ms: $duration_ms, step_count: $step_count, findings_count: $findings_count, "+
			"ran_to_completion: $ran_to_completion}) "+
			"MERGE (v)-[:HAS_RUN]->(r)",
		map[string]any{
			"name": name, "version": version, "id": runID, "started_at": now,
			"status": status, "duration_ms": durationMs, "step_count": stepCount,
			"findings_count": findingsCount, "ran_to_completion": ranToCompletion,
		},
	)
}

// updateStrategyScoring atomically updates scoring on the StrategyVersion node
// using aggregation over StrategyRun nodes. Single Cypher mutation avoids races.
func updateStrategyScoring(ctx context.Context, gc graph.GraphClient, name, version string) error {
	if version == "" {
		version = "1"
	}
	// Atomic: count runs, compute reliability + efficiency + composite in one query.
	// Property names match graph-schema/threat-hunting/nodes/strategy_version.yaml.
	return gc.Update(ctx,
		"MATCH (v:StrategyVersion {name: $name, version: $version})-[:HAS_RUN]->(r:StrategyRun) "+
			"WITH v, count(r) AS total, "+
			"sum(CASE WHEN r.ran_to_completion THEN 1 ELSE 0 END) AS successes, "+
			"sum(r.findings_count) AS total_findings, "+
			"sum(r.duration_ms) AS total_duration "+
			"SET v.total_runs = total, "+
			"v.successful_runs = successes, "+
			"v.reliability = CASE WHEN total > 0 THEN toFloat(successes) / total ELSE 0.0 END, "+
			"v.efficiency = CASE WHEN total_duration > 0 THEN toFloat(total_findings) / (toFloat(total_duration) / 1000.0) ELSE 0.0 END, "+
			"v.composite_score = CASE WHEN total > 0 THEN "+
			"0.6 * (toFloat(successes) / total) + 0.4 * (CASE WHEN total_duration > 0 THEN toFloat(total_findings) / (toFloat(total_duration) / 1000.0) ELSE 0.0 END) "+
			"ELSE 0.0 END",
		map[string]any{"name": name, "version": version},
	)
}

// createStrategyGraphNodes parses METADATA JSON to find requires.sources and
// creates (:Strategy)-[:USES_SOURCE]->(:DomainSource) edges in the graph.
func createStrategyGraphNodes(ctx context.Context, gc graph.GraphClient, name, metadataJSON string) error {
	var meta struct {
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
		Version     string   `json:"version"`
		Requires    struct {
			Sources []string `json:"sources"`
		} `json:"requires"`
	}
	if err := json.Unmarshal([]byte(metadataJSON), &meta); err != nil {
		return fmt.Errorf("parsing metadata: %w", err)
	}

	version := meta.Version
	if version == "" {
		version = "1"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	source := fmt.Sprintf("strategy:%s", name)

	// Create or update Strategy node with version + StrategyVersion
	tagsStr := strings.Join(meta.Tags, ",")
	if err := gc.Update(ctx,
		"MERGE (s:Strategy {name: $name}) "+
			"SET s.description = $desc, s.tags = $tags, s.version = $version, s.status = COALESCE(s.status, 'exploratory'), "+
			"s._source = $source, s._updated_at = $now "+
			"MERGE (v:StrategyVersion {name: $name, version: $version}) "+
			"SET v.created_at = $now, v.status = COALESCE(v.status, 'exploratory'), "+
			"v._source = $source, v._updated_at = $now "+
			"MERGE (s)-[:HAS_VERSION]->(v)",
		map[string]any{"name": name, "desc": meta.Description, "tags": tagsStr, "version": version, "now": now, "source": source},
	); err != nil {
		return fmt.Errorf("creating Strategy/StrategyVersion nodes: %w", err)
	}

	// Create USES_SOURCE edges for each required source (DomainSource, not LogSource)
	for _, src := range meta.Requires.Sources {
		if err := gc.Update(ctx,
			"MATCH (s:Strategy {name: $name}) MERGE (d:DomainSource {name: $src}) SET d._source = $source, d._updated_at = $now MERGE (s)-[:USES_SOURCE]->(d)",
			map[string]any{"name": name, "src": src, "source": source, "now": now},
		); err != nil {
			return fmt.Errorf("creating USES_SOURCE edge for %s: %w", src, err)
		}
	}

	return nil
}

// createStrategyGraphNodesFromContract creates graph nodes from a parsed ContractSpec.
func createStrategyGraphNodesFromContract(ctx context.Context, gc graph.GraphClient, name string, cs *contract.ContractSpec) error {
	version := cs.Version
	if version == "" {
		version = "1"
	}
	tagsStr := strings.Join(cs.Tags, ",")
	now := time.Now().UTC().Format(time.RFC3339)
	source := fmt.Sprintf("strategy:%s", name)

	// Create/update Strategy node with version + status, and StrategyVersion node.
	if err := gc.Update(ctx,
		"MERGE (s:Strategy {name: $name}) "+
			"SET s.description = $desc, s.tags = $tags, s.version = $version, s.status = COALESCE(s.status, 'exploratory'), "+
			"s._source = $source, s._updated_at = $now "+
			"MERGE (v:StrategyVersion {name: $name, version: $version}) "+
			"SET v.created_at = $now, v.status = COALESCE(v.status, 'exploratory'), "+
			"v._source = $source, v._updated_at = $now "+
			"MERGE (s)-[:HAS_VERSION]->(v)",
		map[string]any{"name": name, "desc": cs.Description, "tags": tagsStr, "version": version, "now": now, "source": source},
	); err != nil {
		return fmt.Errorf("creating Strategy/StrategyVersion nodes: %w", err)
	}

	// Create USES_SOURCE edges for DomainSource (not LogSource)
	for _, src := range cs.Requires.Sources {
		if err := gc.Update(ctx,
			"MATCH (s:Strategy {name: $name}) MERGE (d:DomainSource {name: $src}) SET d._source = $source, d._updated_at = $now MERGE (s)-[:USES_SOURCE]->(d)",
			map[string]any{"name": name, "src": src, "source": source, "now": now},
		); err != nil {
			return fmt.Errorf("creating USES_SOURCE edge for %s: %w", src, err)
		}
	}

	// Create USES_TOOL edges from discovery MCP hints → MCPTool (not ToolRef)
	if cs.Discovery != nil {
		for _, hint := range cs.Discovery.MCPHints {
			if hint.Tool != "" {
				if err := gc.Update(ctx,
					"MATCH (s:Strategy {name: $name}) MERGE (mt:MCPTool {name: $tool}) MERGE (s)-[:USES_TOOL]->(mt)",
					map[string]any{"name": name, "tool": hint.Tool},
				); err != nil {
					return fmt.Errorf("creating USES_TOOL edge for %s: %w", hint.Tool, err)
				}
			}
		}
	}

	// Create EXPECTS_COLUMN edges for strategy table columns.
	for tableName, tableSpec := range cs.Requires.Tables {
		for colName, colSpec := range tableSpec.Columns {
			if err := gc.Update(ctx,
				"MATCH (s:Strategy {name: $name}) "+
					"MERGE (c:StrategyColumn {name: $col_name, type: $col_type, semantic: $semantic}) "+
					"MERGE (s)-[:EXPECTS_COLUMN {table: $table}]->(c)",
				map[string]any{
					"name": name, "col_name": colName,
					"col_type": colSpec.Type, "semantic": colSpec.Semantic,
					"table": tableName,
				},
			); err != nil {
				return fmt.Errorf("creating EXPECTS_COLUMN edge for %s.%s: %w", tableName, colName, err)
			}
		}
	}

	return nil
}

// strategyStageResponse is the response for strategy_stage, always includes session_id.
type strategyStageResponse struct {
	SessionID string `json:"session_id"`
	Table     string `json:"table"`
	Rows      int    `json:"rows"`
	Columns   int    `json:"columns"`
	Mode      string `json:"mode"` // "parquet" or "chunk"
	Message   string `json:"message"`
}

func makeStrategyStageHandler(sc strategy.Runner, sessions *strategy.StagingSessionStore, runStore strategy.StagingRunStore) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		table, err := request.RequireString("table")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: table"), nil
		}

		columnsStr, err := request.RequireString("columns")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: columns"), nil
		}
		var columns []string
		if err := json.Unmarshal([]byte(columnsStr), &columns); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid columns JSON: %v", err)), nil
		}

		dataStr, err := request.RequireString("data")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: data"), nil
		}
		var data [][]any
		if err := json.Unmarshal([]byte(dataStr), &data); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid data JSON: %v", err)), nil
		}

		// Parse optional types parameter
		var types []string
		typesStr := request.GetString("types", "")
		if typesStr != "" {
			if err := json.Unmarshal([]byte(typesStr), &types); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid types JSON: %v", err)), nil
			}
		}

		appendMode := request.GetString("append", "") == "true"
		sessionID := request.GetString("session_id", "")

		// --- Resolve session context: prefer StagingRunStore, fall back to legacy sessions ---
		var run *strategy.StagingRun
		var session *strategy.StagingSession
		var stageDir string

		if sessionID != "" && runStore != nil {
			// Try run store first (session_id = run_id).
			run, _ = runStore.Get(ctx, sessionID)
		}

		if run != nil {
			// Run-backed staging: directory scoped by run ID.
			stageDir = filepath.Join(sc.StagingDir(), run.ID)
		} else if sessions != nil {
			// Legacy session fallback.
			if sessionID != "" {
				var ok bool
				session, ok = sessions.Get(sessionID)
				if !ok {
					return mcp.NewToolResultError(fmt.Sprintf("session %q not found (expired or invalid)", sessionID)), nil
				}
			} else {
				session = sessions.Create()
			}
			stageDir = sc.StagingDir()
			if session != nil {
				stageDir = filepath.Join(sc.StagingDir(), session.ID)
			}
		} else {
			stageDir = sc.StagingDir()
		}

		if appendMode {
			// Chunk mode: write unique chunk file.
			chunkName := fmt.Sprintf("%s_chunk_%d", table, time.Now().UnixNano())
			path, err := staging.WriteParquet(chunkName, columns, types, data, stageDir)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("strategy stage (chunk) failed: %v", err)), nil
			}

			// Update run store table state with glob pattern.
			if run != nil && runStore != nil {
				globPattern := filepath.Join(stageDir, table+"_chunk_*.parquet")
				now := time.Now()
				ts := &strategy.TableState{
					Name:           table,
					FetchMode:      "mcp",
					Required:       true,
					Status:         strategy.TableStatusStaged,
					ParquetPath:    globPattern,
					RowCount:       int64(len(data)),
					PagesCompleted: 1,
					CompletedAt:    &now,
				}
				// If table already has state, accumulate row count.
				if existing, ok := run.Tables[table]; ok && existing != nil {
					ts.RowCount = existing.RowCount + int64(len(data))
					ts.PagesCompleted = existing.PagesCompleted + 1
				}
				_ = runStore.UpdateTable(ctx, run.ID, table, ts)
			} else if session != nil {
				session.PutChunk(table, path)
			}

			resp := strategyStageResponse{
				Table:   table,
				Rows:    len(data),
				Columns: len(columns),
				Mode:    "chunk",
				Message: fmt.Sprintf("Table %q chunk staged with %d columns and %d rows.", table, len(columns), len(data)),
			}
			if run != nil {
				resp.SessionID = run.ID
			} else if session != nil {
				resp.SessionID = session.ID
			}
			out, _ := json.Marshal(resp)
			return mcp.NewToolResultText(string(out)), nil
		}

		// Standard mode: write single Parquet file.
		path, err := staging.WriteParquet(table, columns, types, data, stageDir)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("strategy stage failed: %v", err)), nil
		}

		// Update run store table state.
		if run != nil && runStore != nil {
			now := time.Now()
			ts := &strategy.TableState{
				Name:        table,
				FetchMode:   "mcp",
				Required:    true,
				Status:      strategy.TableStatusStaged,
				ParquetPath: path,
				RowCount:    int64(len(data)),
				CompletedAt: &now,
			}
			_ = runStore.UpdateTable(ctx, run.ID, table, ts)

			// Check if all tables are now staged.
			checkAndTransitionRunToStaged(ctx, run.ID, runStore)
		} else if session != nil {
			session.Put(table, path)
		}

		resp := strategyStageResponse{
			Table:   table,
			Rows:    len(data),
			Columns: len(columns),
			Mode:    "parquet",
			Message: fmt.Sprintf("Table %q staged with %d columns and %d rows.", table, len(columns), len(data)),
		}
		if run != nil {
			resp.SessionID = run.ID
		} else if session != nil {
			resp.SessionID = session.ID
		}
		out, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(out)), nil
	}
}

// --- strategy_stage_status types and handler (S8) ---

// stageStatusResponse is the response for strategy_stage_status.
type stageStatusResponse struct {
	RunID                string                      `json:"run_id"`
	Strategy             string                      `json:"strategy"`
	Status               string                      `json:"status"`
	RecoveredFromRestart bool                        `json:"recovered_from_restart"`
	ResumeCount          int                         `json:"resume_count"`
	ElapsedSeconds       float64                     `json:"elapsed_seconds"`
	Tables               map[string]*tableStatusInfo `json:"tables"`
}

// tableStatusInfo reports per-table status in the stage_status response.
type tableStatusInfo struct {
	Status             string `json:"status"`
	Partial            bool   `json:"partial,omitempty"`
	RetryCount         int    `json:"retry_count,omitempty"`
	RowsStaged         int64  `json:"rows_staged,omitempty"`
	PagesCompleted     int    `json:"pages_completed,omitempty"`
	LastError          string `json:"last_error,omitempty"`
	WaitingOn          string `json:"waiting_on"`                        // fracta_fetch, agent_action, execution, none
	ResumedFromRestart bool   `json:"resumed_from_restart,omitempty"`
}

func makeStrategyStageStatusHandler(runStore strategy.StagingRunStore) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		runID, err := request.RequireString("session_id")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: session_id"), nil
		}

		run, err := runStore.Get(ctx, runID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to retrieve run %q: %v", runID, err)), nil
		}
		if run == nil {
			return mcp.NewToolResultError(fmt.Sprintf("run %q not found (expired or invalid)", runID)), nil
		}

		elapsed := time.Since(run.CreatedAt).Seconds()
		recoveredFromRestart := run.RecoveredAt != nil

		tables := make(map[string]*tableStatusInfo, len(run.Tables))
		for name, ts := range run.Tables {
			info := &tableStatusInfo{
				Status:             string(ts.Status),
				Partial:            ts.Partial,
				RetryCount:         ts.RetryCount,
				RowsStaged:         ts.RowCount,
				PagesCompleted:     ts.PagesCompleted,
				ResumedFromRestart: ts.ResumedFromRestart,
				WaitingOn:          classifyWaitingOn(run, ts),
			}
			if ts.Error != nil {
				info.LastError = ts.Error.Message
			}
			tables[name] = info
		}

		resp := stageStatusResponse{
			RunID:                run.ID,
			Strategy:             run.StrategyName,
			Status:               string(run.Status),
			RecoveredFromRestart: recoveredFromRestart,
			ResumeCount:          run.ResumeCount,
			ElapsedSeconds:       elapsed,
			Tables:               tables,
		}

		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// classifyWaitingOn determines what a table is currently waiting on.
func classifyWaitingOn(run *strategy.StagingRun, ts *strategy.TableState) string {
	switch ts.Status {
	case strategy.TableStatusFetching:
		return "fracta_fetch"
	case strategy.TableStatusAwaitingAgent:
		return "agent_action"
	case strategy.TableStatusStaged, strategy.TableStatusFailed, strategy.TableStatusSkipped:
		// Table is done. Check if run is executing.
		if run.Status == strategy.RunStatusExecuting {
			return "execution"
		}
		return "none"
	case strategy.TableStatusPending:
		if ts.FetchMode == "fracta_mcp_gateway" {
			return "fracta_fetch"
		}
		return "agent_action"
	default:
		return "none"
	}
}

// --- strategy_resolve types and handler ---

type resolveResponse struct {
	Strategy string              `json:"strategy"`
	Staged   []resolveStaged     `json:"staged"`
	Pending  []resolvePending    `json:"pending"`
	Native   []resolveNative     `json:"native"`
	Errors   []resolveTableError `json:"errors"`
	Warnings []string            `json:"warnings"`
}

type resolveStaged struct {
	Table       string                `json:"table"`
	Backend     string                `json:"backend"`
	FetchMode   string                `json:"fetch_mode"` // "fracta_mcp_gateway" — used by manifest
	RowsStaged  int64                 `json:"rows_staged"`
	ParquetPath string                `json:"parquet_path"`
	Fields      []resolve.FieldMapping `json:"fields"`
	QueryUsed   string                `json:"query_used,omitempty"`
}

type resolvePending struct {
	Table             string                `json:"table"`
	Backend           string                `json:"backend"`
	FetchMode         string                `json:"fetch_mode"`
	MCPTool           string                `json:"mcp_tool,omitempty"`
	MCPServer         string                `json:"mcp_server,omitempty"`
	QueryHint         string                `json:"query_hint,omitempty"`
	Fields            []resolve.FieldMapping `json:"fields"`
	StageInstructions string                `json:"stage_instructions"`
}

type resolveNative struct {
	Table string `json:"table"`
	Note  string `json:"note"`
}

type resolveTableError struct {
	Table string `json:"table"`
	Error string `json:"error"`
}

// resolveResult holds the output of resolveAndStage.
type resolveResult struct {
	Staged     []resolveStaged
	Pending    []resolvePending
	Native     []resolveNative
	Background []resolve.TablePlan // fracta_mcp_gateway tables needing background staging
	Errors     []resolveTableError
	Warnings   []string
}

// resolveAndStage runs the full resolve pipeline: builds a plan from the contract
// and binding, then dispatches each table by fetch mode (fracta_mcp_gateway/mcp/native).
// fracta_mcp_gateway tables are fetched and staged automatically. MCP tables are returned
// as pending for the caller to handle.
func resolveAndStage(
	ctx context.Context,
	sc strategy.Runner,
	resolver *resolve.Resolver,
	effectiveBinding *contract.BindingSpec,
	fetcher *loaders.MCPFetcher,
	cs *contract.ContractSpec,
	params map[string]any,
	tableFilter map[string]bool,
) (*resolveResult, error) {
	plan, err := resolver.Resolve(ctx, cs, effectiveBinding)
	if err != nil {
		return nil, fmt.Errorf("resolution failed: %w", err)
	}

	result := &resolveResult{
		Staged:   []resolveStaged{},
		Pending:  []resolvePending{},
		Native:   []resolveNative{},
		Errors:   []resolveTableError{},
		Warnings: plan.Warnings,
	}

	// Generate a RunID for this resolve call (used for Parquet namespacing).
	runID, err := generateRunID()
	if err != nil {
		return nil, fmt.Errorf("generating run ID: %w", err)
	}

	for _, tp := range plan.Tables {
		if tableFilter != nil && !tableFilter[tp.Table] {
			continue
		}

		fetchMode := tp.FetchMode
		if fetchMode == "" {
			fetchMode = "mcp"
		}

		switch fetchMode {
		case "fracta_mcp_gateway":
			if fetcher == nil {
				result.Errors = append(result.Errors, resolveTableError{
					Table: tp.Table,
					Error: "fracta_mcp_gateway requires MCP client pool (not configured)",
				})
				continue
			}

			// Check if this table needs background staging (large paginated fetch).
			var sb *contract.SourceBinding
			if effectiveBinding != nil {
				if entry, ok := effectiveBinding.SourceBindings[tp.Table]; ok {
					sb = &entry
				}
			}
			if needsBackground(sb) {
				result.Background = append(result.Background, tp)
				continue
			}

			// Inline staging (small tables).
			staged, tableErr := executeMCPFetch(ctx, fetcher, tp, effectiveBinding, params, sc, runID)
			if tableErr != nil {
				result.Errors = append(result.Errors, resolveTableError{
					Table: tp.Table,
					Error: tableErr.Error(),
				})
			} else {
				result.Staged = append(result.Staged, *staged)
			}

		case "mcp":
			queryHint := interpolateQueryHint(tp, effectiveBinding, params)
			result.Pending = append(result.Pending, resolvePending{
				Table:             tp.Table,
				Backend:           tp.Backend,
				FetchMode:         "mcp",
				MCPTool:           tp.MCPTool,
				MCPServer:         tp.MCPServer,
				QueryHint:         queryHint,
				Fields:            tp.Fields,
				StageInstructions: "Fetch results using the MCP tool above, then call strategy_stage with the mapped columns.",
			})

		case "strategy_native", "native":
			result.Native = append(result.Native, resolveNative{
				Table: tp.Table,
				Note:  "Strategy will populate this table at runtime via ctx.graph or ctx.duckdb.",
			})

		default:
			result.Errors = append(result.Errors, resolveTableError{
				Table: tp.Table,
				Error: fmt.Sprintf("unknown fetch_mode %q", fetchMode),
			})
		}
	}

	return result, nil
}

func makeStrategyResolveHandler(
	sc strategy.Runner,
	resolver *resolve.Resolver,
	binding *contract.BindingSpec,
	fetcher *loaders.MCPFetcher,
) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, err := req.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: name"), nil
		}

		info, err := sc.Describe(name)
		if err != nil {
			if msg, ok := sidecarErrorMessage(err, sc, name); ok {
				return mcp.NewToolResultError(msg), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("strategy %q not found: %v", name, err)), nil
		}

		cs := strategyInfoToContractSpec(info)
		if cs == nil || len(cs.Requires.Tables) == 0 {
			return mcp.NewToolResultError(fmt.Sprintf("strategy %q has no data requirements", name)), nil
		}

		var params map[string]any
		paramsStr := req.GetString("params", "")
		if paramsStr != "" {
			if err := json.Unmarshal([]byte(paramsStr), &params); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid params JSON: %v", err)), nil
			}
		}

		// S9: normalize params (apply defaults, validate required, coerce types).
		params, err = normalizeParams(params, cs)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid parameters: %v", err)), nil
		}

		var tableFilter map[string]bool
		tablesStr := req.GetString("tables", "")
		if tablesStr != "" {
			var tableNames []string
			if err := json.Unmarshal([]byte(tablesStr), &tableNames); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid tables JSON: %v", err)), nil
			}
			tableFilter = make(map[string]bool)
			for _, t := range tableNames {
				tableFilter[t] = true
			}
		}

		effectiveBinding := loadEffectiveBinding(sc, info, binding)

		rr, err := resolveAndStage(ctx, sc, resolver, effectiveBinding, fetcher, cs, params, tableFilter)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		resp := resolveResponse{
			Strategy: name,
			Staged:   rr.Staged,
			Pending:  rr.Pending,
			Native:   rr.Native,
			Errors:   rr.Errors,
			Warnings: rr.Warnings,
		}

		data, err := json.Marshal(resp)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshalling response: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// generateRunID produces an 8-character hex string for Parquet namespacing.
func generateRunID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// normalizeParams validates and coerces strategy parameters against the contract spec.
// It applies defaults for missing optional params, errors on missing required params,
// and coerces JSON types (float64→int, string→bool) to match the declared type.
func normalizeParams(params map[string]any, cs *contract.ContractSpec) (map[string]any, error) {
	if cs == nil || len(cs.Params) == 0 {
		return params, nil
	}
	if params == nil {
		params = make(map[string]any)
	}

	for name, spec := range cs.Params {
		val, exists := params[name]

		if !exists || val == nil {
			if spec.Required {
				return nil, fmt.Errorf("required parameter %q is missing", name)
			}
			if spec.Default != nil {
				params[name] = spec.Default
			}
			continue
		}

		switch spec.Type {
		case "int", "integer":
			switch v := val.(type) {
			case float64:
				params[name] = int(v)
			case string:
				n, err := strconv.Atoi(v)
				if err != nil {
					return nil, fmt.Errorf("parameter %q: expected int, got %q", name, v)
				}
				params[name] = n
			}
		case "bool", "boolean":
			switch v := val.(type) {
			case string:
				b, err := strconv.ParseBool(v)
				if err != nil {
					return nil, fmt.Errorf("parameter %q: expected bool, got %q", name, v)
				}
				params[name] = b
			}
		}
	}

	return params, nil
}

// sidecarErrorMessage checks if err is a sidecar transport or restart error and returns
// a user-facing message. Returns ("", false) for non-sidecar errors so the caller can
// fall through to its default error handling (e.g., "strategy not found").
func sidecarErrorMessage(err error, sc strategy.Runner, name string) (string, bool) {
	var transportErr *strategy.SidecarTransportError
	if errors.As(err, &transportErr) {
		return fmt.Sprintf("strategy runner unavailable: %v", err), true
	}
	var restartErr *strategy.SidecarRestartedError
	if errors.As(err, &restartErr) {
		return fmt.Sprintf("strategy runner restarted but still failing: %v", err), true
	}
	return "", false
}

// classifyExecutionError categorizes an execution error into a StructuredError
// with appropriate category (transient/permanent) and retryable flag.
func classifyExecutionError(err error, strategyName string) *strategy.StructuredError {
	var transportErr *strategy.SidecarTransportError
	if errors.As(err, &transportErr) {
		return &strategy.StructuredError{
			Message:        fmt.Sprintf("strategy runner unavailable: %v", err),
			Category:       "transient",
			Retryable:      true,
			RetryAfterSecs: 5,
			Phase:          "execution",
			Detail:         map[string]any{"strategy": strategyName},
		}
	}
	var restartErr *strategy.SidecarRestartedError
	if errors.As(err, &restartErr) {
		return &strategy.StructuredError{
			Message:        fmt.Sprintf("strategy runner restarted: %v", err),
			Category:       "transient",
			Retryable:      true,
			RetryAfterSecs: 10,
			Phase:          "execution",
			Detail:         map[string]any{"strategy": strategyName},
		}
	}
	// Default: permanent execution failure (Python exception, logic error, etc.)
	return &strategy.StructuredError{
		Message:  fmt.Sprintf("strategy execution failed: %v", err),
		Category: "permanent",
		Phase:    "execution",
		Detail:   map[string]any{"strategy": strategyName},
	}
}

// interpolateArgs walks args recursively and applies InterpolateSimple to every string value.
func interpolateArgs(args map[string]any, params map[string]any) map[string]any {
	for k, v := range args {
		args[k] = interpolateValue(v, params)
	}
	return args
}

// interpolateValue applies template interpolation to a value at any nesting depth.
// Strings are interpolated via loaders.InterpolateSimple; maps and slices are
// walked recursively (using reflection to handle typed collections like []string
// or map[string]int, not just []any/map[string]any).
func interpolateValue(v any, params map[string]any) any {
	switch val := v.(type) {
	case string:
		if interpolated, err := loaders.InterpolateSimple(val, params); err == nil {
			return interpolated
		}
		return val
	case map[string]any:
		for k, elem := range val {
			val[k] = interpolateValue(elem, params)
		}
		return val
	default:
		rv := reflect.ValueOf(v)
		if !rv.IsValid() {
			return v
		}
		switch rv.Kind() {
		case reflect.Slice:
			for i := 0; i < rv.Len(); i++ {
				elem := rv.Index(i).Interface()
				interpolated := interpolateValue(elem, params)
				rv.Index(i).Set(reflect.ValueOf(interpolated))
			}
			return rv.Interface()
		case reflect.Map:
			iter := rv.MapRange()
			for iter.Next() {
				k := iter.Key()
				interpolated := interpolateValue(iter.Value().Interface(), params)
				rv.SetMapIndex(k, reflect.ValueOf(interpolated))
			}
			return rv.Interface()
		}
		return v
	}
}

// executeMCPFetch runs an MCPFetcher for a fracta_mcp_gateway-mode table, stages the
// result as Parquet.
func executeMCPFetch(
	ctx context.Context,
	fetcher *loaders.MCPFetcher,
	tp resolve.TablePlan,
	binding *contract.BindingSpec,
	params map[string]any,
	sc strategy.Runner,
	runID string,
) (*resolveStaged, error) {
	// Get binding entry for MCP-specific settings.
	var sb *contract.SourceBinding
	if binding != nil {
		if entry, ok := binding.SourceBindings[tp.Table]; ok {
			sb = &entry
		}
	}

	// Merge mcp_args (static from binding) with strategy params (dynamic).
	// Flat top-level merge: copy mcp_args, override with params where keys match.
	args := make(map[string]any)
	if sb != nil {
		for k, v := range sb.MCPArgs {
			args[k] = v
		}
		for k, v := range params {
			if _, exists := sb.MCPArgs[k]; exists {
				args[k] = v
			}
		}
	}

	// Recursively interpolate {{param}} placeholders in all string values (S8).
	if len(params) > 0 {
		args = interpolateArgs(args, params)
	}

	// Build field mappings with ColumnType.
	fields := make([]loaders.FieldMapping, 0, len(tp.Fields))
	for _, f := range tp.Fields {
		colType := f.ColumnType
		if colType == "" {
			colType = "VARCHAR"
		}
		fields = append(fields, loaders.FieldMapping{
			Source: f.SourceField,
			Column: f.TargetColumn,
			Type:   colType,
		})
	}

	maxRows := loaders.DefaultMaxRows
	if sb != nil && sb.MaxRows > 0 {
		maxRows = sb.MaxRows
	}

	var timeout time.Duration
	if sb != nil && sb.Timeout != "" {
		var err error
		timeout, err = time.ParseDuration(sb.Timeout)
		if err != nil {
			fractalog.Component("strategy").Warn("invalid timeout in binding, using default", "timeout", sb.Timeout, "error", err)
		}
	}

	server := tp.MCPServer
	tool := tp.MCPTool
	itemsPath := ""
	singleItem := false
	if sb != nil {
		if sb.MCPServer != "" {
			server = sb.MCPServer
		}
		if sb.MCPTool != "" {
			tool = sb.MCPTool
		}
		itemsPath = sb.ItemsPath
		singleItem = sb.SingleItem
	}

	fetchOpts := loaders.MCPFetchOpts{
		Server:          server,
		Tool:            tool,
		Args:            args,
		Fields:          fields,
		ItemsPath:       itemsPath,
		SingleItem:      singleItem,
		MaxRows:         maxRows,
		Timeout:         timeout,
		StagingDir:      sc.StagingDir(),
		Table:           tp.Table,
		RunID:           runID,
		ResponseFormat:  tp.ResponseFormat,
		ResponseAdapter: tp.ResponseAdapter,
	}

	var (
		loadResult *loaders.LoadResult
		err        error
	)
	if sb != nil && sb.Pagination != nil {
		loadResult, err = fetcher.FetchPaginated(ctx, fetchOpts, sb.Pagination)
	} else {
		loadResult, err = fetcher.Fetch(ctx, fetchOpts)
	}
	if err != nil {
		return nil, fmt.Errorf("MCP fetch failed: %w", err)
	}

	return &resolveStaged{
		Table:       tp.Table,
		Backend:     tp.Backend,
		FetchMode:   "fracta_mcp_gateway",
		RowsStaged:  loadResult.RowCount,
		ParquetPath: loadResult.ParquetPath,
		Fields:      tp.Fields,
	}, nil
}

// interpolateQueryHint produces an advisory query hint for MCP-mode tables.
func interpolateQueryHint(tp resolve.TablePlan, binding *contract.BindingSpec, params map[string]any) string {
	tmpl := tp.Query
	if binding != nil {
		if sb, ok := binding.SourceBindings[tp.Table]; ok && sb.QueryTemplate != "" {
			tmpl = sb.QueryTemplate
		}
	}
	if tmpl == "" || params == nil {
		return tmpl
	}
	result, err := loaders.InterpolateSimple(tmpl, params)
	if err != nil {
		return tmpl // return uninterpolated template on error
	}
	return result
}

// buildStagingManifest constructs a StagingManifest from the resolve result
// and contract spec. Returns nil if no resolution was performed.
func buildStagingManifest(rr *resolveResult, cs *contract.ContractSpec) strategy.StagingManifest {
	if rr == nil || cs == nil {
		return nil
	}

	manifest := make(strategy.StagingManifest, len(rr.Staged)+len(rr.Pending)+len(rr.Native))

	for _, s := range rr.Staged {
		required := true
		var columns []string
		if ts, ok := cs.Requires.Tables[s.Table]; ok {
			required = !ts.Optional
			for col := range ts.Columns {
				columns = append(columns, col)
			}
		}
		manifest[s.Table] = strategy.StagingManifestEntry{
			Mode:        s.FetchMode, // "fracta_mcp_gateway" — matches runner's mode check
			Required:    required,
			Staged:      true,
			ParquetPath: s.ParquetPath,
			Columns:     columns,
		}
	}

	for _, p := range rr.Pending {
		required := true
		var columns []string
		if ts, ok := cs.Requires.Tables[p.Table]; ok {
			required = !ts.Optional
			for col := range ts.Columns {
				columns = append(columns, col)
			}
		}
		manifest[p.Table] = strategy.StagingManifestEntry{
			Mode:     p.FetchMode,
			Required: required,
			Staged:   false,
			Columns:  columns,
		}
	}

	for _, n := range rr.Native {
		var columns []string
		if ts, ok := cs.Requires.Tables[n.Table]; ok {
			for col := range ts.Columns {
				columns = append(columns, col)
			}
		}
		manifest[n.Table] = strategy.StagingManifestEntry{
			Mode:     "native",
			Required: false,
			Staged:   false,
			Columns:  columns,
		}
	}

	return manifest
}

// --- Background Staging (S4) ---

const (
	bgMaxRetries     = 2
	bgBaseBackoff    = 2 * time.Second
	bgBackoffFactor  = 2
)

// needsBackground determines if a table should be staged in background
// (vs inline). Heuristic: paginated with max_rows > 50000.
func needsBackground(sb *contract.SourceBinding) bool {
	if sb == nil {
		return false
	}
	if sb.Pagination != nil && sb.MaxRows > 50_000 {
		return true
	}
	return false
}

// startBackgroundStaging launches per-table goroutines for fracta_mcp_gateway tables
// that require background staging. Updates the run store as tables progress.
func startBackgroundStaging(
	run *strategy.StagingRun,
	tables []resolve.TablePlan,
	binding *contract.BindingSpec,
	params map[string]any,
	fetcher *loaders.MCPFetcher,
	sc strategy.Runner,
	runStore strategy.StagingRunStore,
	bus events.Bus,
) {
	log := fractalog.Component("strategy")

	for _, tp := range tables {
		if tp.FetchMode != "fracta_mcp_gateway" {
			continue
		}

		var sb *contract.SourceBinding
		if binding != nil {
			if entry, ok := binding.SourceBindings[tp.Table]; ok {
				sb = &entry
			}
		}

		if !needsBackground(sb) {
			continue
		}

		// Serialize fetch plan for restart recovery.
		fetchPlan := serializeFetchPlan(tp, sb, params)
		fetchPlanJSON, _ := json.Marshal(fetchPlan)

		now := time.Now()
		ts := &strategy.TableState{
			Name:      tp.Table,
			FetchMode: "fracta_mcp_gateway",
			Required:  !tp.Optional,
			Status:    strategy.TableStatusFetching,
			FetchPlan: fetchPlanJSON,
			StartedAt: &now,
		}
		if sb != nil && sb.MaxRows > 0 {
			ts.TotalEstimate = sb.MaxRows
		}

		if err := runStore.UpdateTable(context.Background(), run.ID, tp.Table, ts); err != nil {
			log.Warn("failed to update table to fetching", "run_id", run.ID, "table", tp.Table, "error", err)
		}

		log.Info("staging.table.start",
			"run_id", run.ID, "table", tp.Table,
			"strategy", run.StrategyName, "fetch_mode", "fracta_mcp_gateway")
		emitStrategyEvent(bus, "table_staging_start", "unknown", run.ID, run.StrategyName, map[string]string{
			"table": tp.Table, "fetch_mode": "fracta_mcp_gateway",
		})

		// Launch goroutine.
		go stageTable(run.ID, tp, sb, params, fetcher, sc, runStore, bus, run.StrategyName)
	}
}

// stageTable is the per-table background staging goroutine.
// It fetches data, updates the store on progress, retries transient errors.
func stageTable(
	runID string,
	tp resolve.TablePlan,
	sb *contract.SourceBinding,
	params map[string]any,
	fetcher *loaders.MCPFetcher,
	sc strategy.Runner,
	runStore strategy.StagingRunStore,
	bus events.Bus,
	strategyName string,
) {
	log := fractalog.Component("strategy")
	ctx := context.Background()

	var lastErr error
	retries := 0

	for retries <= bgMaxRetries {
		loadResult, err := executeMCPFetch(ctx, fetcher, tp, &contract.BindingSpec{
			SourceBindings: map[string]contract.SourceBinding{tp.Table: *sb},
		}, params, sc, runID)

		if err == nil {
			// Success: mark table as staged.
			now := time.Now()
			ts := &strategy.TableState{
				Name:           tp.Table,
				FetchMode:      "fracta_mcp_gateway",
				Required:       !tp.Optional,
				Status:         strategy.TableStatusStaged,
				ParquetPath:    loadResult.ParquetPath,
				RowCount:       loadResult.RowsStaged,
				PagesCompleted: 0, // non-paginated success
				CompletedAt:    &now,
			}
			if err := runStore.UpdateTable(ctx, runID, tp.Table, ts); err != nil {
				log.Warn("failed to update table to staged", "run_id", runID, "table", tp.Table, "error", err)
			}

			log.Info("staging.table.complete",
				"run_id", runID, "table", tp.Table,
				"rows", loadResult.RowsStaged, "strategy", strategyName)
			emitStrategyEvent(bus, "table_staged", "success", runID, strategyName, map[string]string{
				"table": tp.Table, "rows": strconv.FormatInt(loadResult.RowsStaged, 10),
			})

			// Check if all tables are now staged → transition run to "staged".
			checkAndTransitionRunToStaged(ctx, runID, runStore)
			return
		}

		lastErr = err
		retries++

		if retries > bgMaxRetries {
			break
		}

		// Exponential backoff.
		backoff := bgBaseBackoff * time.Duration(1<<(retries-1)) * time.Duration(bgBackoffFactor) / time.Duration(bgBackoffFactor)
		log.Warn("background staging retry", "run_id", runID, "table", tp.Table, "retry", retries, "backoff", backoff, "error", err)

		// Update retry count in store.
		ts := &strategy.TableState{
			Name:       tp.Table,
			FetchMode:  "fracta_mcp_gateway",
			Required:   !tp.Optional,
			Status:     strategy.TableStatusFetching,
			RetryCount: retries,
			Error: &strategy.StructuredError{
				Message:   err.Error(),
				Category:  "transient",
				Retryable: true,
				Phase:     "staging",
			},
		}
		_ = runStore.UpdateTable(ctx, runID, tp.Table, ts)

		time.Sleep(backoff)
	}

	// All retries exhausted — mark as failed.
	now := time.Now()
	ts := &strategy.TableState{
		Name:        tp.Table,
		FetchMode:   "fracta_mcp_gateway",
		Required:    !tp.Optional,
		Status:      strategy.TableStatusFailed,
		RetryCount:  retries,
		CompletedAt: &now,
		Error: &strategy.StructuredError{
			Message:  fmt.Sprintf("staging failed after %d retries: %v", retries, lastErr),
			Category: "permanent",
			Phase:    "staging",
		},
	}
	if err := runStore.UpdateTable(ctx, runID, tp.Table, ts); err != nil {
		log.Warn("failed to update table to failed", "run_id", runID, "table", tp.Table, "error", err)
	}

	log.Error("staging.table.failed",
		"run_id", runID, "table", tp.Table,
		"retries", retries, "strategy", strategyName, "error", lastErr)
	emitStrategyEvent(bus, "table_staged", "failure", runID, strategyName, map[string]string{
		"table": tp.Table, "retries": strconv.Itoa(retries),
	})

	// If required table failed → transition run to failed with error detail.
	if !tp.Optional {
		_ = runStore.FailRun(ctx, runID, &strategy.StructuredError{
			Message:  fmt.Sprintf("required table %q failed: %v", tp.Table, lastErr),
			Category: "permanent",
			Phase:    "staging",
		})
	}
}

// checkAndTransitionRunToStaged checks if all auto-staging tables are done and
// transitions the run appropriately:
//   - If any table is still fetching/pending → no transition (still in progress)
//   - If any required table failed → transition to failed
//   - If only awaiting_agent tables remain → transition to pending (mixed-mode)
//   - If all tables are in terminal states → transition to staged
func checkAndTransitionRunToStaged(ctx context.Context, runID string, runStore strategy.StagingRunStore) {
	run, err := runStore.Get(ctx, runID)
	if err != nil || run == nil {
		return
	}

	nextStatus := strategy.DeriveRunStatus(run)
	if nextStatus == "" || nextStatus == run.Status {
		return // no transition needed (still in progress or already correct)
	}

	if nextStatus == strategy.RunStatusFailed {
		// Find the first required failed table for the error message.
		for _, ts := range run.Tables {
			if ts.Required && ts.Status == strategy.TableStatusFailed {
				errMsg := "required table failed"
				if ts.Error != nil {
					errMsg = ts.Error.Message
				}
				_ = runStore.FailRun(ctx, runID, &strategy.StructuredError{
					Message:  errMsg,
					Category: "permanent",
					Phase:    "staging",
				})
				return
			}
		}
	}

	_ = runStore.UpdateStatus(ctx, runID, nextStatus)
}

// serializeFetchPlan builds a SerializedFetchPlan from resolve data for restart recovery.
func serializeFetchPlan(tp resolve.TablePlan, sb *contract.SourceBinding, params map[string]any) *strategy.SerializedFetchPlan {
	plan := &strategy.SerializedFetchPlan{
		Server:          tp.MCPServer,
		Tool:            tp.MCPTool,
		ResponseFormat:  tp.ResponseFormat,
		ResponseAdapter: tp.ResponseAdapter,
	}

	// Fields.
	for _, f := range tp.Fields {
		plan.Fields = append(plan.Fields, strategy.FetchField{
			Source: f.SourceField,
			Column: f.TargetColumn,
			Type:   f.ColumnType,
		})
	}

	if sb != nil {
		if sb.MCPServer != "" {
			plan.Server = sb.MCPServer
		}
		if sb.MCPTool != "" {
			plan.Tool = sb.MCPTool
		}
		plan.ItemsPath = sb.ItemsPath
		plan.SingleItem = sb.SingleItem
		plan.MaxRows = sb.MaxRows

		if sb.Timeout != "" {
			if d, err := time.ParseDuration(sb.Timeout); err == nil {
				plan.TimeoutSecs = int(d.Seconds())
			}
		}

		// Merge and interpolate args.
		args := make(map[string]any)
		for k, v := range sb.MCPArgs {
			args[k] = v
		}
		if len(params) > 0 {
			args = interpolateArgs(args, params)
		}
		plan.Args = args

		if sb.Pagination != nil {
			plan.Pagination = &strategy.PaginationConfig{
				Mode:           sb.Pagination.Mode,
				PageSize:       sb.Pagination.PageSize,
				OffsetParam:    sb.Pagination.OffsetParam,
				LimitParam:     sb.Pagination.LimitParam,
				CursorParam:    sb.Pagination.CursorParam,
				NextCursorPath: sb.Pagination.NextCursorPath,
				TotalPath:      sb.Pagination.TotalPath,
			}
		}
	}

	return plan
}

// --- Observability helpers (S10) ---

// emitStrategyEvent emits a strategy lifecycle event to the bus if non-nil.
func emitStrategyEvent(bus events.Bus, action, outcome, runID, strategyName string, attrs map[string]string) {
	if bus == nil {
		return
	}
	e := events.Info("strategy-runner", action)
	e.Category = "strategy"
	e.Resource = "staging_run:" + runID
	e.Outcome = outcome
	if attrs == nil {
		attrs = make(map[string]string)
	}
	attrs["strategy"] = strategyName
	attrs["run_id"] = runID
	e.Attrs = attrs
	bus.Emit(context.Background(), e)
} 
