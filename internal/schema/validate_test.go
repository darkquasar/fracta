package schema

import "testing"

func TestValidateNode(t *testing.T) {
	reg := loadMergedSchema(t)

	// Valid DomainSource node.
	err := reg.ValidateNode("DomainSource", map[string]any{
		"name":        "CloudTrail",
		"description": "AWS CloudTrail logs",
	})
	if err != nil {
		t.Errorf("valid DomainSource: %v", err)
	}

	// Missing required property.
	err = reg.ValidateNode("DomainSource", map[string]any{
		"description": "No name provided",
	})
	if err == nil {
		t.Error("missing required 'name' should fail")
	}

	// Unknown label.
	err = reg.ValidateNode("FakeLabel", map[string]any{})
	if err == nil {
		t.Error("unknown label should fail")
	}

	// Valid MCPTool node.
	err = reg.ValidateNode("MCPTool", map[string]any{
		"name":       "elastic.search",
		"tool":       "search",
		"mcp_server": "elastic",
	})
	if err != nil {
		t.Errorf("valid MCPTool: %v", err)
	}

	// Valid DataStore node.
	err = reg.ValidateNode("DataStore", map[string]any{
		"uri":  "elastic://elastic/audit-**",
		"type": "elasticsearch",
	})
	if err != nil {
		t.Errorf("valid DataStore: %v", err)
	}

	// Valid FieldType with semantic reference.
	err = reg.ValidateNode("FieldType", map[string]any{
		"name":      "sourceIPAddress",
		"data_type": "string",
		"semantic":  "ip_address",
	})
	if err != nil {
		t.Errorf("valid FieldType with semantic: %v", err)
	}

	// Invalid semantic reference.
	err = reg.ValidateNode("FieldType", map[string]any{
		"name":      "sourceIPAddress",
		"data_type": "string",
		"semantic":  "not_a_real_semantic",
	})
	if err == nil {
		t.Error("invalid semantic reference should fail")
	}
}

func TestValidateEdge(t *testing.T) {
	reg := loadMergedSchema(t)

	// Valid HAS_FIELD edge.
	err := reg.ValidateEdge("HAS_FIELD", "DomainSource", "FieldType", nil)
	if err != nil {
		t.Errorf("valid HAS_FIELD: %v", err)
	}

	// Invalid from label.
	err = reg.ValidateEdge("HAS_FIELD", "IP", "FieldType", nil)
	if err == nil {
		t.Error("HAS_FIELD from IP should fail")
	}

	// Unknown edge type.
	err = reg.ValidateEdge("FAKE_EDGE", "DomainSource", "FieldType", nil)
	if err == nil {
		t.Error("unknown edge type should fail")
	}

	// PROVIDES: MCPServer -> MCPTool
	err = reg.ValidateEdge("PROVIDES", "MCPServer", "MCPTool", nil)
	if err != nil {
		t.Errorf("valid PROVIDES: %v", err)
	}

	// QUERYABLE_VIA: DataStore -> MCPServer
	err = reg.ValidateEdge("QUERYABLE_VIA", "DataStore", "MCPServer", nil)
	if err != nil {
		t.Errorf("valid QUERYABLE_VIA: %v", err)
	}

	// STORED_IN: DomainSource -> DataStore
	err = reg.ValidateEdge("STORED_IN", "DomainSource", "DataStore", nil)
	if err != nil {
		t.Errorf("valid STORED_IN: %v", err)
	}

	// RETURNS_FIELD: MCPTool -> MCPField
	err = reg.ValidateEdge("RETURNS_FIELD", "MCPTool", "MCPField", nil)
	if err != nil {
		t.Errorf("valid RETURNS_FIELD: %v", err)
	}

	// USES_TOOL: Strategy -> MCPTool
	err = reg.ValidateEdge("USES_TOOL", "Strategy", "MCPTool", nil)
	if err != nil {
		t.Errorf("valid USES_TOOL: %v", err)
	}

	// EXPECTS_COLUMN with required table property.
	err = reg.ValidateEdge("EXPECTS_COLUMN", "Strategy", "StrategyColumn", map[string]any{
		"table": "auth_events",
	})
	if err != nil {
		t.Errorf("valid EXPECTS_COLUMN: %v", err)
	}

	// EXPECTS_COLUMN missing required table property.
	err = reg.ValidateEdge("EXPECTS_COLUMN", "Strategy", "StrategyColumn", map[string]any{})
	if err == nil {
		t.Error("EXPECTS_COLUMN without table should fail")
	}
}
