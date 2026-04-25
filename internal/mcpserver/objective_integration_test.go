package mcpserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/darkquasar/fracta/internal/objective"
	"github.com/darkquasar/fracta/internal/proposal"
	"github.com/darkquasar/fracta/internal/state/sqlitestore"
	"github.com/mark3labs/mcp-go/mcp"
)

// TestObjectiveToolsE2E exercises the full agent-facing MCP tool contract:
// fracta_propose_mission → fracta_report_finding → fracta_resolve_objective.
// This is T36 — the handler-level test that T35 (admission integration) does not cover.
func TestObjectiveToolsE2E(t *testing.T) {
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "obj-e2e.db")
	store, err := sqlitestore.New(dbPath)
	if err != nil {
		t.Fatalf("sqlitestore.New: %v", err)
	}
	defer store.Close()

	objStore := objective.NewSQLiteStore(store.DB())
	propStore := proposal.NewSQLiteStore(store.DB())

	// Create an objective for the test.
	obj := &objective.Objective{
		ID:          "e2e-obj",
		Description: "E2E handler test",
		MaxMissions: 10,
		MaxDepth:    3,
		MaxRuntime:  3600000000000, // 1 hour
	}
	if err := objStore.Create(ctx, obj); err != nil {
		t.Fatalf("create objective: %v", err)
	}

	// Build handlers with static resolver (simulates agent-mode stdio context).
	resolver := &StaticResolver{AgentTask: "test-agent", ObjectiveID: "e2e-obj", MissionID: 42}
	handlePropose := makeProposeMissionHandler(resolver, propStore)
	handleFinding := makeReportFindingHandler(resolver, objStore)
	handleResolve := makeResolveObjectiveHandler(resolver, objStore)

	// --- Test fracta_propose_mission ---
	proposeResult, err := handlePropose(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{
				"task":       "investigate host-Y",
				"contract":   "Check host-Y for lateral movement",
				"dedupe_key": "investigate:host=host-Y:reason=lateral",
				"rationale":  "Found suspicious RDP connection from host-Y",
				"evidence":   `{"alert_id": "ALERT-456"}`,
				"priority":   float64(2),
			},
		},
	})
	if err != nil {
		t.Fatalf("handleProposeMission error: %v", err)
	}
	if isErrorResult(proposeResult) {
		t.Fatalf("handleProposeMission returned error: %s", extractText(proposeResult))
	}

	// Verify proposal was persisted with correct context.
	var propResponse map[string]interface{}
	json.Unmarshal([]byte(extractText(proposeResult)), &propResponse)
	if propResponse["objective_id"] != "e2e-obj" {
		t.Errorf("proposal objective_id = %v, want e2e-obj", propResponse["objective_id"])
	}
	if propResponse["status"] != "pending" {
		t.Errorf("proposal status = %v, want pending", propResponse["status"])
	}

	// Verify the proposal in the store has control-plane-derived fields.
	proposals, _ := propStore.PendingProposals(ctx)
	if len(proposals) != 1 {
		t.Fatalf("pending proposals = %d, want 1", len(proposals))
	}
	p := proposals[0]
	if p.ObjectiveID != "e2e-obj" {
		t.Errorf("stored objective_id = %q, want e2e-obj", p.ObjectiveID)
	}
	if p.ParentMission != 42 {
		t.Errorf("stored parent_mission = %d, want 42 (derived from agent context)", p.ParentMission)
	}
	if p.ProposedBy != "test-agent" {
		t.Errorf("stored proposed_by = %q, want test-agent (derived from agent context)", p.ProposedBy)
	}
	if p.DedupeKey != "investigate:host=host-Y:reason=lateral" {
		t.Errorf("stored dedupe_key = %q", p.DedupeKey)
	}

	// --- Test fracta_report_finding ---
	findingResult, err := handleFinding(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{
				"summary":       "Found C2 beacon on host-Y port 443",
				"graph_node_id": "event-789",
			},
		},
	})
	if err != nil {
		t.Fatalf("handleReportFinding error: %v", err)
	}
	if isErrorResult(findingResult) {
		t.Fatalf("handleReportFinding returned error: %s", extractText(findingResult))
	}

	// Verify finding count incremented.
	objAfterFinding, _ := objStore.Get(ctx, "e2e-obj")
	if objAfterFinding.FindingCount != 1 {
		t.Errorf("finding_count = %d, want 1", objAfterFinding.FindingCount)
	}

	// --- Test fracta_resolve_objective ---
	resolveResult, err := handleResolve(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{
				"status":  "answered",
				"outcome": "APT29 confirmed: C2 beacon on host-Y, lateral via RDP",
			},
		},
	})
	if err != nil {
		t.Fatalf("handleResolveObjective error: %v", err)
	}
	if isErrorResult(resolveResult) {
		t.Fatalf("handleResolveObjective returned error: %s", extractText(resolveResult))
	}

	// Verify objective resolved.
	objFinal, _ := objStore.Get(ctx, "e2e-obj")
	if objFinal.Status != objective.StatusAnswered {
		t.Errorf("final status = %q, want answered", objFinal.Status)
	}
	if objFinal.Outcome != "APT29 confirmed: C2 beacon on host-Y, lateral via RDP" {
		t.Errorf("outcome = %q", objFinal.Outcome)
	}
}

// TestProposeMission_ContextDerived verifies that objective_id and parent_mission
// are NOT taken from the request — they are derived from the agent's construction-time context.
func TestProposeMission_ContextDerived(t *testing.T) {
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "ctx-test.db")
	store, err := sqlitestore.New(dbPath)
	if err != nil {
		t.Fatalf("sqlitestore.New: %v", err)
	}
	defer store.Close()

	objStore := objective.NewSQLiteStore(store.DB())
	propStore := proposal.NewSQLiteStore(store.DB())

	obj := &objective.Objective{ID: "real-obj", Description: "Real", MaxRuntime: 3600000000000}
	objStore.Create(ctx, obj)

	// Static resolver: objective=real-obj, mission=99, task=agent-A
	resolver := &StaticResolver{AgentTask: "agent-A", ObjectiveID: "real-obj", MissionID: 99}
	handlePropose := makeProposeMissionHandler(resolver, propStore)

	// Even if the request tried to supply different context (which it can't
	// because the tool doesn't accept those params), the stored proposal
	// must use the resolver-derived values.
	handlePropose(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{
				"task":       "child-task",
				"dedupe_key": "test:key",
				"rationale":  "testing context derivation",
			},
		},
	})

	proposals, _ := propStore.PendingProposals(ctx)
	if len(proposals) != 1 {
		t.Fatalf("pending = %d, want 1", len(proposals))
	}
	if proposals[0].ObjectiveID != "real-obj" {
		t.Errorf("objective_id = %q, want real-obj (derived)", proposals[0].ObjectiveID)
	}
	if proposals[0].ParentMission != 99 {
		t.Errorf("parent_mission = %d, want 99 (derived)", proposals[0].ParentMission)
	}
	if proposals[0].ProposedBy != "agent-A" {
		t.Errorf("proposed_by = %q, want agent-A (derived)", proposals[0].ProposedBy)
	}
}

// TestResolveObjective_InvalidTransition verifies that resolving a non-open
// objective returns a clear error.
func TestResolveObjective_InvalidTransition(t *testing.T) {
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "invalid-tx.db")
	store, err := sqlitestore.New(dbPath)
	if err != nil {
		t.Fatalf("sqlitestore.New: %v", err)
	}
	defer store.Close()

	objStore := objective.NewSQLiteStore(store.DB())

	// Create an already-answered objective.
	obj := &objective.Objective{
		ID: "done-obj", Description: "Already done",
		Status: objective.StatusAnswered, MaxRuntime: 3600000000000,
	}
	objStore.Create(ctx, obj)

	resolver := &StaticResolver{AgentTask: "agent-B", ObjectiveID: "done-obj", MissionID: 1}
	handleResolve := makeResolveObjectiveHandler(resolver, objStore)

	result, _ := handleResolve(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{
				"status":  "disproven",
				"outcome": "Actually wrong",
			},
		},
	})

	if !isErrorResult(result) {
		t.Fatal("expected error for invalid transition (answered → disproven)")
	}
}

// --- helpers ---

func extractText(r *mcp.CallToolResult) string {
	if r == nil || len(r.Content) == 0 {
		return ""
	}
	for _, c := range r.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}
