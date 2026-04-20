package graph

import (
	"testing"
)

func TestParseResult(t *testing.T) {
	// Simulate FalkorDB's [headers, rows, stats] response.
	res := []interface{}{
		[]interface{}{"name", "count"},
		[]interface{}{
			[]interface{}{"CloudTrail", int64(42)},
			[]interface{}{"VPCFlowLogs", int64(7)},
		},
		[]interface{}{"Cached execution: 0", "Query internal execution time: 0.5 ms"},
	}

	records, err := parseResult(res)
	if err != nil {
		t.Fatalf("parseResult: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	if records[0]["name"] != "CloudTrail" {
		t.Errorf("record[0].name = %v, want CloudTrail", records[0]["name"])
	}
	if records[0]["count"] != int64(42) {
		t.Errorf("record[0].count = %v, want 42", records[0]["count"])
	}
	if records[1]["name"] != "VPCFlowLogs" {
		t.Errorf("record[1].name = %v, want VPCFlowLogs", records[1]["name"])
	}
}

func TestParseResultEmpty(t *testing.T) {
	// No rows returned.
	res := []interface{}{
		[]interface{}{"col"},
		[]interface{}{},
		[]interface{}{"Query internal execution time: 0.1 ms"},
	}

	records, err := parseResult(res)
	if err != nil {
		t.Fatalf("parseResult: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

func TestParseResultNil(t *testing.T) {
	records, err := parseResult(nil)
	if err != nil {
		t.Fatalf("parseResult: %v", err)
	}
	if records != nil {
		t.Errorf("expected nil records, got %v", records)
	}
}
