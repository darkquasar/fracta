package loaders

import (
	"testing"
)

func TestParseCSVResponse(t *testing.T) {
	csv := "id,name,score\na1,alice,42\na2,bob,7\n"
	items, err := parseCSVResponse(csv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0]["id"] != "a1" || items[0]["name"] != "alice" || items[0]["score"] != "42" {
		t.Errorf("row 0 = %v", items[0])
	}
	if items[1]["id"] != "a2" || items[1]["name"] != "bob" {
		t.Errorf("row 1 = %v", items[1])
	}
}

func TestParseCSVResponse_Empty(t *testing.T) {
	items, err := parseCSVResponse("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if items != nil {
		t.Errorf("expected nil, got %v", items)
	}
}

func TestParseCSVResponse_HeaderOnly(t *testing.T) {
	items, err := parseCSVResponse("id,name\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

func TestParseNDJSONResponse(t *testing.T) {
	ndjson := `{"id":"a1","severity":"high"}
{"id":"a2","severity":"low"}
`
	items, err := parseNDJSONResponse(ndjson)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0]["id"] != "a1" {
		t.Errorf("row 0 id = %v", items[0]["id"])
	}
	if items[1]["severity"] != "low" {
		t.Errorf("row 1 severity = %v", items[1]["severity"])
	}
}

func TestParseNDJSONResponse_WithBlankLines(t *testing.T) {
	ndjson := `{"id":"a1"}

{"id":"a2"}

`
	items, err := parseNDJSONResponse(ndjson)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestParseNDJSONResponse_InvalidLine(t *testing.T) {
	ndjson := `{"id":"a1"}
not json
`
	_, err := parseNDJSONResponse(ndjson)
	if err == nil {
		t.Fatal("expected error for invalid JSON line")
	}
}

func TestParseTabularTextResponse(t *testing.T) {
	text := `Query executed successfully.
Columns: id | hostname | severity
---
abc123 | host-1 | High
def456 | host-2 | Low
`
	items, err := parseTabularTextResponse(text, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0]["id"] != "abc123" || items[0]["hostname"] != "host-1" || items[0]["severity"] != "High" {
		t.Errorf("row 0 = %v", items[0])
	}
	if items[1]["id"] != "def456" || items[1]["hostname"] != "host-2" {
		t.Errorf("row 1 = %v", items[1])
	}
}

func TestParseTabularTextResponse_NoHeaders(t *testing.T) {
	text := `Some random text
---
data here
`
	_, err := parseTabularTextResponse(text, nil)
	if err == nil {
		t.Fatal("expected error when no column headers found")
	}
}

func TestParseTabularTextResponse_NoSeparator(t *testing.T) {
	text := `Columns: id | name
data here
`
	_, err := parseTabularTextResponse(text, nil)
	if err == nil {
		t.Fatal("expected error when no separator found")
	}
}

func TestParseTabularTextResponse_FormatB(t *testing.T) {
	text := `Query executed successfully.
Column Names: id, hostname, severity
Row 0: ['abc123', 'host-1', 'High']
Row 1: ['def456', 'host-2', 'Low']
`
	items, err := parseTabularTextResponse(text, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0]["id"] != "abc123" || items[0]["hostname"] != "host-1" || items[0]["severity"] != "High" {
		t.Errorf("row 0 = %v", items[0])
	}
	if items[1]["id"] != "def456" || items[1]["hostname"] != "host-2" || items[1]["severity"] != "Low" {
		t.Errorf("row 1 = %v", items[1])
	}
}

func TestParseTabularTextResponse_FormatB_DoubleQuotes(t *testing.T) {
	text := `Column Names: name, value
Row 0: ["alice", "42"]
`
	items, err := parseTabularTextResponse(text, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0]["name"] != "alice" || items[0]["value"] != "42" {
		t.Errorf("row 0 = %v", items[0])
	}
}

func TestParseTabularTextResponse_FormatB_QuotedCommas(t *testing.T) {
	text := `Column Names: name, severity, notes
Row 0: ['Doe, Jane', 'High', 'C2, exfil observed']
Row 1: ['Smith, Bob', 'Low', 'clean']
`
	items, err := parseTabularTextResponse(text, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0]["name"] != "Doe, Jane" {
		t.Errorf("row 0 name = %q, want %q", items[0]["name"], "Doe, Jane")
	}
	if items[0]["notes"] != "C2, exfil observed" {
		t.Errorf("row 0 notes = %q, want %q", items[0]["notes"], "C2, exfil observed")
	}
	if items[1]["name"] != "Smith, Bob" {
		t.Errorf("row 1 name = %q, want %q", items[1]["name"], "Smith, Bob")
	}
}

func TestParseTabularTextResponse_FormatB_EscapedQuotes(t *testing.T) {
	text := "Column Names: name, severity\nRow 0: ['O\\'Brien', 'High']\nRow 1: [\"He said \\\"hi\\\"\", 'Low']\n"
	items, err := parseTabularTextResponse(text, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0]["name"] != "O'Brien" {
		t.Errorf("row 0 name = %q, want %q", items[0]["name"], "O'Brien")
	}
	if items[0]["severity"] != "High" {
		t.Errorf("row 0 severity = %q, want %q", items[0]["severity"], "High")
	}
	if items[1]["name"] != `He said "hi"` {
		t.Errorf("row 1 name = %q, want %q", items[1]["name"], `He said "hi"`)
	}
}

func TestParseTabularTextResponse_FormatB_NoColumnNames(t *testing.T) {
	text := `Row 0: ['a', 'b']
`
	_, err := parseTabularTextResponse(text, nil)
	if err == nil {
		t.Fatal("expected error when no Column Names header found")
	}
}

func TestGetResponseAdapter_Registered(t *testing.T) {
	adapter, ok := GetResponseAdapter("tabular_text")
	if !ok {
		t.Fatal("tabular_text adapter not found")
	}
	if adapter == nil {
		t.Fatal("tabular_text adapter is nil")
	}
}

func TestGetResponseAdapter_Unknown(t *testing.T) {
	_, ok := GetResponseAdapter("nonexistent")
	if ok {
		t.Fatal("expected unknown adapter to not be found")
	}
}
