package secrets

import (
	"fmt"
	"os"
	"strings"

	"github.com/darkquasar/fracta/internal/config"
)

// Resolve returns the secret value from whichever source is configured.
func Resolve(sv *config.SecretValue) (string, error) {
	if sv == nil {
		return "", fmt.Errorf("secret value is nil")
	}
	if sv.Value != "" {
		return sv.Value, nil
	}
	if sv.Env != "" {
		v := os.Getenv(sv.Env)
		if v == "" {
			return "", fmt.Errorf("environment variable %q is empty or unset", sv.Env)
		}
		return v, nil
	}
	if sv.File != "" {
		data, err := os.ReadFile(sv.File)
		if err != nil {
			return "", fmt.Errorf("reading secret file %q: %w", sv.File, err)
		}
		return strings.TrimRight(string(data), "\n\r"), nil
	}
	return "", fmt.Errorf("secret value has no source configured")
}
