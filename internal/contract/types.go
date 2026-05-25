package contract

// ParamSpec describes a single input parameter for a strategy.
type ParamSpec struct {
	Type        string `yaml:"type"`
	Required    bool   `yaml:"required"`
	Default     any    `yaml:"default,omitempty"`
	Description string `yaml:"description"`
}

// ColumnSpec describes a single column within a staging table.
type ColumnSpec struct {
	Type        string `yaml:"type"`
	Semantic    string `yaml:"semantic,omitempty"`
	Description string `yaml:"description,omitempty"`
}

// TableSpec describes a DuckDB table that must be staged before execution.
type TableSpec struct {
	Description string                `yaml:"description"`
	Optional    bool                  `yaml:"optional"`
	VolumeHint  string                `yaml:"volume_hint,omitempty"`
	Columns     map[string]ColumnSpec `yaml:"columns"`
}

// RequiresSpec declares what a strategy needs at runtime.
type RequiresSpec struct {
	Graph bool `yaml:"graph"`
	// MCP indicates the strategy calls ctx.mcp.call_tool() inline. When true,
	// the runner refuses to start if ctx.mcp would be None (gateway access
	// not configured). Closes Bug 10: silent None instead of loud failure.
	MCP     bool                 `yaml:"mcp,omitempty"`
	Sources []string             `yaml:"sources,omitempty"`
	Tables  map[string]TableSpec `yaml:"tables,omitempty"`
}

// MCPHint provides a hint for pre-staging data via an MCP tool.
type MCPHint struct {
	Tool    string `yaml:"tool"`
	Purpose string `yaml:"purpose"`
	StageAs string `yaml:"stage_as"`
}

// DiscoverySpec provides guidance for the orchestrator on data pre-staging.
type DiscoverySpec struct {
	Description string    `yaml:"description"`
	MCPHints    []MCPHint `yaml:"mcp_hints,omitempty"`
}

// ContractSpec is the full parsed representation of a contract.yaml file.
type ContractSpec struct {
	Name          string               `yaml:"name"`
	Version       string               `yaml:"version,omitempty"`
	Description   string               `yaml:"description"`
	Tags          []string             `yaml:"tags"`
	Params        map[string]ParamSpec `yaml:"params,omitempty"`
	Requires      RequiresSpec         `yaml:"requires"`
	PinnedBackend string               `yaml:"pinned_backend,omitempty"`
	Discovery     *DiscoverySpec       `yaml:"discovery,omitempty"`
}
