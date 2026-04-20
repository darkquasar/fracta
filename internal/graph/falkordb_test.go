package graph

import "testing"

func TestBuildCypherPrefix(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{
			name:   "nil map",
			params: nil,
			want:   "",
		},
		{
			name:   "empty map",
			params: map[string]any{},
			want:   "",
		},
		{
			name:   "string value",
			params: map[string]any{"name": "CloudTrail"},
			want:   "CYPHER name='CloudTrail' ",
		},
		{
			name:   "string with single quote",
			params: map[string]any{"name": "it's"},
			want:   "CYPHER name='it\\'s' ",
		},
		{
			name:   "int value",
			params: map[string]any{"count": 42},
			want:   "CYPHER count=42 ",
		},
		{
			name:   "int64 value",
			params: map[string]any{"count": int64(99)},
			want:   "CYPHER count=99 ",
		},
		{
			name:   "bool value",
			params: map[string]any{"active": true},
			want:   "CYPHER active=true ",
		},
		{
			name:   "float64 value",
			params: map[string]any{"score": 3.14},
			want:   "CYPHER score=3.14 ",
		},
		{
			name:   "nil value skipped",
			params: map[string]any{"x": nil},
			want:   "CYPHER ",
		},
		{
			name:   "multiple keys sorted",
			params: map[string]any{"b": "two", "a": 1},
			want:   "CYPHER a=1 b='two' ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCypherPrefix(tt.params)
			if got != tt.want {
				t.Errorf("buildCypherPrefix(%v) = %q, want %q", tt.params, got, tt.want)
			}
		})
	}
}
