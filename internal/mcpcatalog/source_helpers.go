package mcpcatalog

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// fractaSourceFile is the name of the per-project memo file recording the
// last `fetch` source. Lives at <root>/mcp-servers/.fracta-source.
const fractaSourceFile = ".fracta-source"

// ReadFractaSource returns the recorded source spec from
// <root>/mcp-servers/.fracta-source, or "" if the file is absent.
//
// Empty return is not an error — pre-fetch projects don't have the memo file
// and callers fall back to DefaultFetchSourceSpec via ResolveFetchSource.
func ReadFractaSource(projectRoot string) (string, error) {
	path := filepath.Join(projectRoot, "mcp-servers", fractaSourceFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}
