package contract

import (
	"testing"
)

func TestParseContract_FullContract(t *testing.T) {
	yaml := `
name: "correlate-ip-across-sources"
version: "1.0.0"
description: >
  Query the universal graph for all log sources with IP fields.
tags: [correlation, ip, multi-source]
params:
  ip:
    type: str
    required: true
    description: "IP address to investigate"
requires:
  graph: true
  sources: [CloudTrail, VPCFlowLogs, IdentitySystemLog]
`
	cs, err := ParseContract([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cs.Name != "correlate-ip-across-sources" {
		t.Errorf("name = %q, want %q", cs.Name, "correlate-ip-across-sources")
	}
	if cs.Version != "1.0.0" {
		t.Errorf("version = %q, want %q", cs.Version, "1.0.0")
	}
	if len(cs.Tags) != 3 {
		t.Errorf("tags count = %d, want 3", len(cs.Tags))
	}
	if cs.Tags[0] != "correlation" {
		t.Errorf("first tag = %q, want %q", cs.Tags[0], "correlation")
	}
	if !cs.Requires.Graph {
		t.Error("requires.graph should be true")
	}
	if len(cs.Requires.Sources) != 3 {
		t.Errorf("sources count = %d, want 3", len(cs.Requires.Sources))
	}
	p, ok := cs.Params["ip"]
	if !ok {
		t.Fatal("expected 'ip' param")
	}
	if !p.Required {
		t.Error("ip param should be required")
	}
}

func TestParseContract_WithTables(t *testing.T) {
	yaml := `
name: "elastic-field-survey"
description: "Survey ES indices."
tags: [enrichment]
requires:
  graph: false
  tables:
    es_indices:
      description: "Index metadata"
      optional: false
      columns:
        index:
          type: VARCHAR
        docs:
          type: VARCHAR
pinned_backend: elasticsearch
`
	cs, err := ParseContract([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cs.PinnedBackend != "elasticsearch" {
		t.Errorf("pinned_backend = %q, want %q", cs.PinnedBackend, "elasticsearch")
	}
	tbl, ok := cs.Requires.Tables["es_indices"]
	if !ok {
		t.Fatal("expected 'es_indices' table")
	}
	if tbl.Optional {
		t.Error("es_indices should not be optional")
	}
	if len(tbl.Columns) != 2 {
		t.Errorf("columns count = %d, want 2", len(tbl.Columns))
	}
	col, ok := tbl.Columns["index"]
	if !ok {
		t.Fatal("expected 'index' column")
	}
	if col.Type != "VARCHAR" {
		t.Errorf("column type = %q, want VARCHAR", col.Type)
	}
}

func TestParseContract_WithDiscovery(t *testing.T) {
	yaml := `
name: "test-strategy"
description: "Test with discovery hints."
tags: [test]
discovery:
  description: "Requires external data."
  mcp_hints:
    - tool: "elasticsearch.search"
      purpose: "Fetch events"
      stage_as: "events"
`
	cs, err := ParseContract([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cs.Discovery == nil {
		t.Fatal("expected discovery spec")
	}
	if len(cs.Discovery.MCPHints) != 1 {
		t.Fatalf("mcp_hints count = %d, want 1", len(cs.Discovery.MCPHints))
	}
	hint := cs.Discovery.MCPHints[0]
	if hint.Tool != "elasticsearch.search" {
		t.Errorf("hint tool = %q, want %q", hint.Tool, "elasticsearch.search")
	}
	if hint.StageAs != "events" {
		t.Errorf("hint stage_as = %q, want %q", hint.StageAs, "events")
	}
}

func TestParseContract_MissingName(t *testing.T) {
	yaml := `
description: "No name."
tags: [test]
`
	_, err := ParseContract([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestParseContract_MissingDescription(t *testing.T) {
	yaml := `
name: "test"
tags: [test]
`
	_, err := ParseContract([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing description")
	}
}

func TestParseContract_MissingTags(t *testing.T) {
	yaml := `
name: "test"
description: "Test."
`
	_, err := ParseContract([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing tags")
	}
}

func TestParseContract_WithVolumeHint(t *testing.T) {
	yaml := `
name: "test-volume-hint"
description: "Test volume_hint on tables."
tags: [test]
requires:
  tables:
    auth_events:
      description: "Auth events"
      volume_hint: large
      columns:
        identity_arn:
          type: VARCHAR
    small_table:
      description: "Small lookup"
      columns:
        key:
          type: VARCHAR
`
	cs, err := ParseContract([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tbl, ok := cs.Requires.Tables["auth_events"]
	if !ok {
		t.Fatal("expected 'auth_events' table")
	}
	if tbl.VolumeHint != "large" {
		t.Errorf("volume_hint = %q, want %q", tbl.VolumeHint, "large")
	}
	small, ok := cs.Requires.Tables["small_table"]
	if !ok {
		t.Fatal("expected 'small_table' table")
	}
	if small.VolumeHint != "" {
		t.Errorf("volume_hint = %q, want empty", small.VolumeHint)
	}
}

func TestParseBinding(t *testing.T) {
	yaml := `
source_bindings:
  auth_events:
    backend: elasticsearch
    config_key: elastic_main
    index: "audit-**"
field_overrides:
  auth_events:
    identity: "userIdentity.arn"
    ip_address: "sourceIPAddress"
`
	bs, err := ParseBinding([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sb, ok := bs.SourceBindings["auth_events"]
	if !ok {
		t.Fatal("expected 'auth_events' binding")
	}
	if sb.Backend != "elasticsearch" {
		t.Errorf("backend = %q, want %q", sb.Backend, "elasticsearch")
	}
	if sb.Index != "audit-**" {
		t.Errorf("index = %q, want %q", sb.Index, "audit-**")
	}
	overrides, ok := bs.FieldOverrides["auth_events"]
	if !ok {
		t.Fatal("expected field overrides for auth_events")
	}
	if overrides["identity"] != "userIdentity.arn" {
		t.Errorf("identity override = %q", overrides["identity"])
	}
}
