package graph

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SeedFromFile loads a .cypher file and executes each non-empty, non-comment
// line as a separate GRAPH.QUERY against the given GraphClient.
// Returns the number of statements executed.
func SeedFromFile(ctx context.Context, client GraphClient, path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	executed := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if err := client.Update(ctx, line, nil); err != nil {
			return executed, fmt.Errorf("statement %d in %s: %w", executed+1, filepath.Base(path), err)
		}
		executed++
	}
	if err := scanner.Err(); err != nil {
		return executed, fmt.Errorf("scan %s: %w", path, err)
	}
	return executed, nil
}

// SeedFromDir loads all *.cypher files from a directory in sorted order.
// Returns the total number of statements executed across all files.
func SeedFromDir(ctx context.Context, client GraphClient, dir string) (int, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".cypher") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("walk %s: %w", dir, err)
	}
	sort.Strings(files)

	total := 0
	for _, f := range files {
		n, err := SeedFromFile(ctx, client, f)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
