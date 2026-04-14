package contract

import (
	"os"
)

// ResolveContract resolves contract content from a raw string.
// Empty string → no contract. If the string is a path to an existing regular file,
// the file contents are returned. Otherwise the raw string is returned as-is (inline text).
func ResolveContract(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}

	info, err := os.Stat(raw)
	if err == nil && info.Mode().IsRegular() {
		data, err := os.ReadFile(raw)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	return raw, nil
}
