package agentpolicy

import (
	"strings"
	"testing"

	"github.com/darkquasar/fracta/internal/host"
)

func TestOptsFromConfig(t *testing.T) {
	cfg := host.WorkspaceConfig{
		GraphAddr:   "localhost:6379",
		StrategyDir: "/opt/strategies",
		ObjectiveID: "obj-1",
		MissionID:   42,
	}
	opts := OptsFromConfig(cfg)

	if !opts.HasGraph {
		t.Error("expected HasGraph=true")
	}
	if !opts.HasStrategy {
		t.Error("expected HasStrategy=true")
	}
	if opts.ObjectiveID != "obj-1" {
		t.Errorf("ObjectiveID = %q, want obj-1", opts.ObjectiveID)
	}
	if opts.MissionID != 42 {
		t.Errorf("MissionID = %d, want 42", opts.MissionID)
	}
}

func TestOptsFromConfig_Empty(t *testing.T) {
	opts := OptsFromConfig(host.WorkspaceConfig{})
	if opts.HasGraph || opts.HasStrategy || opts.ObjectiveID != "" || opts.MissionID != 0 {
		t.Errorf("expected all zero values, got %+v", opts)
	}
}

func TestResolveInstructionSections_All(t *testing.T) {
	opts := InstructionOpts{
		HasGraph:    true,
		HasStrategy: true,
		ObjectiveID: "obj-1",
	}
	sections := ResolveInstructionSections(opts)

	if len(sections) != 3 {
		t.Fatalf("expected 3 sections, got %d: %v", len(sections), sections)
	}
	// Order matters: objective, graph, strategy
	if sections[0] != "objective" {
		t.Errorf("sections[0] = %q, want objective", sections[0])
	}
	if sections[1] != "graph" {
		t.Errorf("sections[1] = %q, want graph", sections[1])
	}
	if sections[2] != "strategy" {
		t.Errorf("sections[2] = %q, want strategy", sections[2])
	}
}

func TestResolveInstructionSections_GraphOnly(t *testing.T) {
	sections := ResolveInstructionSections(InstructionOpts{HasGraph: true})
	if len(sections) != 1 || sections[0] != "graph" {
		t.Errorf("expected [graph], got %v", sections)
	}
}

func TestResolveInstructionSections_StrategyOnly(t *testing.T) {
	sections := ResolveInstructionSections(InstructionOpts{HasStrategy: true})
	if len(sections) != 1 || sections[0] != "strategy" {
		t.Errorf("expected [strategy], got %v", sections)
	}
}

func TestResolveInstructionSections_None(t *testing.T) {
	sections := ResolveInstructionSections(InstructionOpts{})
	if len(sections) != 0 {
		t.Errorf("expected empty, got %v", sections)
	}
}

func TestGraphInstructions_Content(t *testing.T) {
	content := GraphInstructions()
	if !strings.Contains(content, "Knowledge Graph Protocol") {
		t.Error("missing Knowledge Graph Protocol header")
	}
	if !strings.Contains(content, "graph_checkpoint") {
		t.Error("missing graph_checkpoint reference")
	}
	if !strings.Contains(content, "graph_schema") {
		t.Error("missing graph_schema reference")
	}
}

func TestStrategyInstructions_Content(t *testing.T) {
	content := StrategyInstructions()
	if !strings.Contains(content, "Strategy Engine") {
		t.Error("missing Strategy Engine header")
	}
	if !strings.Contains(content, "strategy_match") {
		t.Error("missing strategy_match reference")
	}
	if !strings.Contains(content, "strategy_run") {
		t.Error("missing strategy_run reference")
	}
}

func TestObjectiveInstructions_Content(t *testing.T) {
	content := ObjectiveInstructions(InstructionOpts{
		ObjectiveID:          "obj-42",
		ObjectiveDescription: "Hunt lateral movement",
		MissionID:            7,
	})

	if !strings.Contains(content, "obj-42") {
		t.Error("missing objective ID")
	}
	if !strings.Contains(content, "Hunt lateral movement") {
		t.Error("missing objective description")
	}
	if !strings.Contains(content, "fracta_propose_mission") {
		t.Error("missing fracta_propose_mission reference")
	}
}

func TestObjectiveInstructions_DefaultDescription(t *testing.T) {
	content := ObjectiveInstructions(InstructionOpts{
		ObjectiveID: "obj-1",
	})
	if !strings.Contains(content, "description not available") {
		t.Error("expected default description placeholder")
	}
}

func TestRenderInstructionSections_Full(t *testing.T) {
	opts := InstructionOpts{
		HasGraph:             true,
		HasStrategy:          true,
		ObjectiveID:          "obj-1",
		ObjectiveDescription: "Test objective",
		MissionID:            1,
	}
	content := RenderInstructionSections(opts)

	if !strings.Contains(content, "Knowledge Graph Protocol") {
		t.Error("missing graph section")
	}
	if !strings.Contains(content, "Strategy Engine") {
		t.Error("missing strategy section")
	}
	if !strings.Contains(content, "obj-1") {
		t.Error("missing objective section")
	}
}

func TestRenderInstructionSections_Empty(t *testing.T) {
	content := RenderInstructionSections(InstructionOpts{})
	if content != "" {
		t.Errorf("expected empty content, got %q", content)
	}
}
