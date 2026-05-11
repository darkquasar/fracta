package mcpcatalog

import (
	"fmt"
	"strings"
)

// Filter is a parsed entry filter. Each key (status, category, ...) AND-combines
// with the others; comma-separated values under the same key OR-combine.
//
// Examples:
//
//	"status=tested"                          → only tested entries
//	"status=tested,category=knowledge"       → tested AND category=knowledge
//	"status=tested,documented"               → tested OR documented (single key)
//	"status=tested,documented,category=knowledge"
//	    → (tested OR documented) AND category=knowledge
type Filter struct {
	// rules: key → list of accepted values (OR within a list)
	rules map[string][]string
}

// IsEmpty reports whether the filter would accept every entry.
func (f Filter) IsEmpty() bool { return len(f.rules) == 0 }

// ParseFilter parses an expression like "status=tested,category=knowledge"
// into a Filter. Whitespace around keys and values is trimmed. Comma-separated
// values that come *before* the next `key=` token are treated as additional
// OR-values for the previous key.
func ParseFilter(expr string) (Filter, error) {
	f := Filter{rules: map[string][]string{}}
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return f, nil
	}
	parts := strings.Split(expr, ",")
	var currentKey string
	for _, raw := range parts {
		part := strings.TrimSpace(raw)
		if part == "" {
			continue
		}
		if i := strings.IndexByte(part, '='); i >= 0 {
			k := strings.TrimSpace(part[:i])
			v := strings.TrimSpace(part[i+1:])
			if k == "" {
				return Filter{}, fmt.Errorf("mcpcatalog: filter expression %q: empty key in %q", expr, raw)
			}
			if v == "" {
				return Filter{}, fmt.Errorf("mcpcatalog: filter expression %q: empty value for key %q", expr, k)
			}
			f.rules[k] = append(f.rules[k], v)
			currentKey = k
			continue
		}
		// No `=` — treat as an OR-value for the previous key.
		if currentKey == "" {
			return Filter{}, fmt.Errorf("mcpcatalog: filter expression %q: value %q has no key", expr, raw)
		}
		f.rules[currentKey] = append(f.rules[currentKey], part)
	}
	return f, nil
}

// Match reports whether the entry satisfies every key in the filter (each key
// matching at least one of its values).
func (f Filter) Match(e *Entry) bool {
	if e == nil {
		return false
	}
	for key, values := range f.rules {
		if !matchKey(e, key, values) {
			return false
		}
	}
	return true
}

func matchKey(e *Entry, key string, values []string) bool {
	field := entryField(e, key)
	for _, v := range values {
		if field == v {
			return true
		}
	}
	return false
}

// entryField returns the entry's filterable value for the given key.
// Supported keys: status, category, id, name, transport (container variant
// preferred, then local). Unknown keys return "" and therefore never match.
func entryField(e *Entry, key string) string {
	switch key {
	case "status":
		return e.Status
	case "category":
		return e.Category
	case "id":
		return e.ID
	case "name":
		return e.Name
	case "transport":
		if v, ok := e.Variants["container"]; ok && v.Transport != "" {
			return v.Transport
		}
		if v, ok := e.Variants["local"]; ok && v.Transport != "" {
			return v.Transport
		}
		return ""
	}
	return ""
}
