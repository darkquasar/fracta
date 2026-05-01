package agentpolicy

import (
	"embed"

	"github.com/darkquasar/fracta/internal/assets"
	"github.com/darkquasar/fracta/internal/host"
)

//go:embed instructions/*
var instructionFS embed.FS

// sharedInstructions loads the host-neutral instruction content (graph protocol,
// strategy engine, objective context) that all hosts share.
var sharedInstructions = assets.New(instructionFS, "instructions")

// InstructionOpts controls which optional sections are included in agent
// instructions. This is platform knowledge — any host needs to know whether
// graph, strategy, and objective tools are available.
type InstructionOpts struct {
	HasGraph    bool   // graph tools available (GraphAddr set)
	HasStrategy bool   // strategy tools available (StrategyDir set)
	HasGateway  bool   // gateway MCP tools available (GatewayURL set)
	ObjectiveID string // objective this agent serves
	MissionID   int64  // mission this agent is executing

	// ObjectiveDescription is the human-readable description of the objective.
	// Empty means not fetched; templates use generic placeholder text.
	ObjectiveDescription string
	MissionDepth         int
}

// OptsFromConfig derives InstructionOpts from a WorkspaceConfig.
func OptsFromConfig(cfg host.WorkspaceConfig) InstructionOpts {
	return InstructionOpts{
		HasGraph:    cfg.GraphAddr != "",
		HasStrategy: cfg.StrategyDir != "",
		HasGateway:  cfg.GatewayURL != "",
		ObjectiveID: cfg.ObjectiveID,
		MissionID:   cfg.MissionID,
	}
}

// ResolveInstructionSections returns which optional instruction sections should
// be included based on the opts. Returns section names: "objective", "graph",
// "strategy". The host adapter uses these to compose its final instructions.
func ResolveInstructionSections(opts InstructionOpts) []string {
	var sections []string
	if opts.HasGateway {
		sections = append(sections, "gateway")
	}
	if opts.ObjectiveID != "" {
		sections = append(sections, "objective")
	}
	if opts.HasGraph {
		sections = append(sections, "graph")
	}
	if opts.HasStrategy {
		sections = append(sections, "strategy")
	}
	return sections
}

// GraphInstructions returns the knowledge graph protocol section content.
// This is host-neutral documentation about fracta's graph tools.
func GraphInstructions() string {
	return sharedInstructions.MustLoad("graph-protocol.md")
}

// StrategyInstructions returns the strategy engine awareness section content.
// This is host-neutral documentation about fracta's strategy tools.
func StrategyInstructions() string {
	return sharedInstructions.MustLoad("strategy-engine.md")
}

// ObjectiveInstructions returns the rendered objective context section.
func ObjectiveInstructions(opts InstructionOpts) string {
	desc := opts.ObjectiveDescription
	if desc == "" {
		desc = "(description not available — check with chessmaster)"
	}
	return sharedInstructions.MustRender("objective-context.md.tmpl", map[string]any{
		"ObjectiveID":          opts.ObjectiveID,
		"ObjectiveDescription": desc,
		"MissionID":            opts.MissionID,
	})
}

// RenderInstructionSections renders the content for the given section names.
// Sections are appended in order. Unknown section names are silently skipped.
// GatewayInstructions returns the MCP gateway awareness section content.
func GatewayInstructions() string {
	return sharedInstructions.MustLoad("gateway-tools.md")
}

func RenderInstructionSections(opts InstructionOpts) string {
	var result string
	for _, section := range ResolveInstructionSections(opts) {
		switch section {
		case "gateway":
			result += GatewayInstructions()
		case "objective":
			result += ObjectiveInstructions(opts)
		case "graph":
			result += GraphInstructions()
		case "strategy":
			result += StrategyInstructions()
		}
	}
	return result
}
