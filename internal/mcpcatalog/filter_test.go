package mcpcatalog

import "testing"

func TestParseFilter_Empty(t *testing.T) {
	f, err := ParseFilter("")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !f.IsEmpty() {
		t.Errorf("expected empty filter")
	}
}

func TestParseFilter_SinglePair(t *testing.T) {
	f, err := ParseFilter("status=tested")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if f.IsEmpty() {
		t.Fatalf("filter should not be empty")
	}
	cases := []struct {
		e    *Entry
		want bool
	}{
		{&Entry{Status: "tested"}, true},
		{&Entry{Status: "documented"}, false},
		{&Entry{Status: ""}, false},
	}
	for i, c := range cases {
		if got := f.Match(c.e); got != c.want {
			t.Errorf("case %d: Match(%+v) = %v, want %v", i, c.e, got, c.want)
		}
	}
}

func TestParseFilter_AndCombined(t *testing.T) {
	f, err := ParseFilter("status=tested,category=security")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	cases := []struct {
		e    *Entry
		want bool
	}{
		{&Entry{Status: "tested", Category: "security"}, true},
		{&Entry{Status: "tested", Category: "knowledge"}, false},
		{&Entry{Status: "documented", Category: "security"}, false},
	}
	for i, c := range cases {
		if got := f.Match(c.e); got != c.want {
			t.Errorf("case %d: Match(%+v) = %v, want %v", i, c.e, got, c.want)
		}
	}
}

func TestParseFilter_OrWithinKey(t *testing.T) {
	f, err := ParseFilter("status=tested,documented")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	cases := []struct {
		e    *Entry
		want bool
	}{
		{&Entry{Status: "tested"}, true},
		{&Entry{Status: "documented"}, true},
		{&Entry{Status: "candidate"}, false},
	}
	for i, c := range cases {
		if got := f.Match(c.e); got != c.want {
			t.Errorf("case %d: Match(%+v) = %v, want %v", i, c.e, got, c.want)
		}
	}
}

func TestParseFilter_MixedAndOr(t *testing.T) {
	f, err := ParseFilter("status=tested,documented,category=knowledge")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	cases := []struct {
		e    *Entry
		want bool
	}{
		{&Entry{Status: "tested", Category: "knowledge"}, true},
		{&Entry{Status: "documented", Category: "knowledge"}, true},
		{&Entry{Status: "tested", Category: "security"}, false},
		{&Entry{Status: "candidate", Category: "knowledge"}, false},
	}
	for i, c := range cases {
		if got := f.Match(c.e); got != c.want {
			t.Errorf("case %d: Match(%+v) = %v, want %v", i, c.e, got, c.want)
		}
	}
}

func TestParseFilter_Errors(t *testing.T) {
	bad := []string{
		"=tested",     // empty key
		"status=",     // empty value
		"justavalue",  // no key in scope
		" =x",         // empty key after trim
	}
	for _, expr := range bad {
		if _, err := ParseFilter(expr); err == nil {
			t.Errorf("ParseFilter(%q): expected error, got nil", expr)
		}
	}
}

func TestFilter_MatchNilEntry(t *testing.T) {
	f, _ := ParseFilter("status=tested")
	if f.Match(nil) {
		t.Errorf("Match(nil) should be false")
	}
}

func TestEntryField_Transport(t *testing.T) {
	e := &Entry{Variants: map[string]VariantSpec{"container": {Transport: "streamable-http"}}}
	if got := entryField(e, "transport"); got != "streamable-http" {
		t.Errorf("got %q", got)
	}
	e2 := &Entry{Variants: map[string]VariantSpec{"local": {Transport: "stdio"}}}
	if got := entryField(e2, "transport"); got != "stdio" {
		t.Errorf("got %q", got)
	}
}
