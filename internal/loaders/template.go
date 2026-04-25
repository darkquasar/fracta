package loaders

import (
	"fmt"
	"strings"
)

// InterpolateSimple performs simple {{param}} string replacement for MCP advisory hints.
// It replaces all {{param}} placeholders with corresponding values from params.
// Returns an error if any referenced parameters are missing.
func InterpolateSimple(tmpl string, params map[string]any) (string, error) {
	result := tmpl
	for key, val := range params {
		placeholder := "{{" + key + "}}"
		result = strings.ReplaceAll(result, placeholder, formatValue(val))
	}

	// Check for unresolved placeholders
	if idx := strings.Index(result, "{{"); idx != -1 {
		end := strings.Index(result[idx:], "}}")
		if end != -1 {
			missing := result[idx+2 : idx+end]
			return "", fmt.Errorf("unresolved template parameter: %s", missing)
		}
	}
	return result, nil
}

func formatValue(v any) string {
	switch val := v.(type) {
	case string:
		return val
	default:
		return fmt.Sprintf("%v", val)
	}
}
