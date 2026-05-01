package resolve

// FieldMapping maps a source field to a contract column via a shared semantic type.
type FieldMapping struct {
	SourceField  string `json:"source_field"`  // field name in data source / MCP response
	TargetColumn string `json:"target_column"` // column name in the strategy's expected table
	Semantic     string `json:"semantic"`      // shared semantic type that links them
	ColumnType   string `json:"column_type,omitempty"` // from contract: VARCHAR, INT64, FLOAT64, BOOLEAN, TIMESTAMP
}

// TablePlan describes how to populate one staged table.
type TablePlan struct {
	Table           string         `json:"table"`                      // target table name (from contract)
	Source          string         `json:"source"`                     // DomainSource name
	Backend         string         `json:"backend"`                    // "elasticsearch", "mcp", "snowflake"
	FetchMode       string         `json:"fetch_mode,omitempty"`       // "fracta_mcp_gateway", "mcp", "native"
	MCPTool         string         `json:"mcp_tool,omitempty"`         // MCP tool to call (when backend=mcp)
	MCPServer       string         `json:"mcp_server,omitempty"`       // MCP server name (e.g., "elastic", "vendor")
	Query           string         `json:"query,omitempty"`            // query template / index pattern
	Fields          []FieldMapping `json:"fields"`                     // field-to-column mapping
	Optional        bool           `json:"optional,omitempty"`         // whether the strategy works without this table
	ResponseFormat  string         `json:"response_format,omitempty"`  // "json" (default), "csv", "ndjson"
	ResponseAdapter string         `json:"response_adapter,omitempty"` // tool-specific adapter (e.g., "tabular_text")
}

// ResolutionPlan is the output of the resolver. It tells the agent
// exactly how to stage data for a strategy execution.
type ResolutionPlan struct {
	Strategy string      `json:"strategy"` // strategy name
	Tables   []TablePlan `json:"tables"`   // one plan per required table
	Warnings []string    `json:"warnings,omitempty"`
}
