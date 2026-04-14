package contract

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// PaginationConfig describes how to paginate an MCP tool call.
type PaginationConfig struct {
	Mode           string `yaml:"mode"`             // "offset" or "cursor"
	PageSize       int    `yaml:"page_size"`
	OffsetParam    string `yaml:"offset_param"`     // arg name for offset (offset mode)
	LimitParam     string `yaml:"limit_param"`      // arg name for page size (offset mode)
	CursorParam    string `yaml:"cursor_param"`     // arg name for cursor token (cursor mode)
	NextCursorPath string `yaml:"next_cursor_path"` // dot-path to next cursor in response
	TotalPath      string `yaml:"total_path"`       // dot-path to total count (optional, offset mode)
}

// SourceBinding maps one abstract source/table to a concrete backend.
//
// Fetch mode reference:
//
//	fetch_mode         | Response format                              | Who fetches       | Who parses
//	-------------------+----------------------------------------------+-------------------+----------------------------------
//	fracta_mcp_gateway   | JSON (default), CSV, NDJSON, or named adapter| Go MCPFetcher     | MCPFetcher + format/adapter dispatch
//	mcp                | Any                                          | Agent (manual)    | Agent + strategy_stage
//	native             | N/A                                          | Strategy itself   | Strategy code
//
// For fracta_mcp_gateway: set response_format for generic parsers (csv, ndjson) or
// response_adapter for tool-specific parsing (e.g., "tabular_text"). These are
// mutually exclusive. Default is JSON when neither is set.
type SourceBinding struct {
	Backend         string             `yaml:"backend"`
	ConfigKey       string             `yaml:"config_key"`
	Index           string             `yaml:"index,omitempty"`
	QueryTemplate   string             `yaml:"query_template,omitempty"`
	FetchMode       string             `yaml:"fetch_mode,omitempty"`
	MaxRows         int                `yaml:"max_rows,omitempty"`
	FieldMap        map[string]string  `yaml:"field_map,omitempty"`
	MCPTool         string             `yaml:"mcp_tool,omitempty"`
	MCPServer       string             `yaml:"mcp_server,omitempty"`
	MCPArgs         map[string]any     `yaml:"mcp_args,omitempty"`
	ItemsPath       string             `yaml:"items_path,omitempty"`
	SingleItem      bool               `yaml:"single_item,omitempty"`
	Timeout         string             `yaml:"timeout,omitempty"`
	Pagination      *PaginationConfig  `yaml:"pagination,omitempty"`
	ResponseFormat  string             `yaml:"response_format,omitempty"`  // "json" (default), "csv", "ndjson"
	ResponseAdapter string             `yaml:"response_adapter,omitempty"` // tool-specific: "tabular_text", etc.
}

// FetchModeOrDefault returns the effective fetch mode, defaulting to "mcp".
// Valid modes: mcp, fracta_mcp_gateway, native.
// "mcp_client" is accepted as a backward-compatible alias for "fracta_mcp_gateway".
// "direct" and "api" are no longer supported and return an error via ValidateFetchMode.
func (sb SourceBinding) FetchModeOrDefault() string {
	switch sb.FetchMode {
	case "":
		return "mcp"
	case "mcp_client":
		return "fracta_mcp_gateway"
	case "strategy_native":
		return "native"
	default:
		return sb.FetchMode
	}
}

// ValidateFetchMode returns an error if the fetch mode is no longer supported.
func (sb SourceBinding) ValidateFetchMode() error {
	switch sb.FetchMode {
	case "direct", "api":
		return fmt.Errorf("fetch_mode %q is no longer supported — use 'fracta_mcp_gateway' with an MCP server binding instead", sb.FetchMode)
	default:
		return nil
	}
}

// BindingSpec is the full parsed representation of a binding.yaml file.
type BindingSpec struct {
	SourceBindings map[string]SourceBinding   `yaml:"source_bindings"`
	FieldOverrides map[string]map[string]string `yaml:"field_overrides,omitempty"`
}

// ParseBinding parses a binding.yaml from a YAML byte slice.
func ParseBinding(data []byte) (*BindingSpec, error) {
	var bs BindingSpec
	if err := yaml.Unmarshal(data, &bs); err != nil {
		return nil, fmt.Errorf("parsing binding YAML: %w", err)
	}
	if err := bs.validate(); err != nil {
		return nil, err
	}
	return &bs, nil
}

// validResponseFormats is the set of recognized response_format values.
var validResponseFormats = map[string]bool{
	"":       true, // default (JSON)
	"json":   true,
	"csv":    true,
	"ndjson": true,
}

func (bs *BindingSpec) validate() error {
	for name, sb := range bs.SourceBindings {
		if err := sb.ValidateFetchMode(); err != nil {
			return fmt.Errorf("source binding %q: %w", name, err)
		}
		if sb.ResponseFormat != "" && sb.ResponseAdapter != "" {
			return fmt.Errorf("source binding %q: response_format and response_adapter are mutually exclusive", name)
		}
		if !validResponseFormats[sb.ResponseFormat] {
			return fmt.Errorf("source binding %q: unknown response_format %q (valid: json, csv, ndjson)", name, sb.ResponseFormat)
		}
	}
	return nil
}

// ParseBindingFile reads and parses a binding.yaml from disk.
func ParseBindingFile(path string) (*BindingSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading binding file: %w", err)
	}
	return ParseBinding(data)
}
