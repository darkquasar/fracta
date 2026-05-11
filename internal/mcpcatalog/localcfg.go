// Package mcpcatalog provides local-config mutators for fracta.yaml,
// docker-compose.yml, and .env.example. Mutations preserve comments, key
// ordering, and indentation by round-tripping through gopkg.in/yaml.v3 as a
// *yaml.Node tree. Writes are atomic (temp+fsync+rename); the happy path
// leaves no .bak files. See spec-43 §4 R5, §8.4, §11 R2.
package mcpcatalog

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/project/scaffolds"
)

// modeKey maps a scaffold kind to the per-mode key under
// mcp_servers.servers.<id>. local→"local"; docker-compose and k8s both map to
// "remote" (the runtime distinguishes via URL).
func modeKey(k scaffolds.Kind) (string, error) {
	switch k {
	case scaffolds.KindLocal:
		return "local", nil
	case scaffolds.KindDockerCompose, scaffolds.KindK8s:
		return "remote", nil
	default:
		return "", fmt.Errorf("mcpcatalog: unsupported scaffold kind %v", k)
	}
}

// ReadFractaYAML parses path into a *yaml.Node tree preserving comments and
// key ordering. An empty file is allowed and returns a node with Kind=0 that
// later mutators recognize as "create the document".
func ReadFractaYAML(path string) (*yaml.Node, error) {
	return readYAMLNode(path)
}

// WriteFractaYAMLAtomic writes root to path via temp+fsync+rename. No .bak is
// created on the happy path. Multi-file rollback (R5) is the caller's
// responsibility.
func WriteFractaYAMLAtomic(path string, root *yaml.Node) error {
	return writeYAMLNodeAtomic(path, root)
}

// ReadComposeYAML parses path into a *yaml.Node tree.
func ReadComposeYAML(path string) (*yaml.Node, error) {
	return readYAMLNode(path)
}

// WriteComposeYAMLAtomic writes root to path via temp+fsync+rename.
func WriteComposeYAMLAtomic(path string, root *yaml.Node) error {
	return writeYAMLNodeAtomic(path, root)
}

// readYAMLNode is the shared loader for fracta.yaml and docker-compose.yml.
func readYAMLNode(path string) (*yaml.Node, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var n yaml.Node
	dec := yaml.NewDecoder(f)
	if err := dec.Decode(&n); err != nil {
		if errors.Is(err, io.EOF) {
			// Empty file — return an empty document node so mutators can build it.
			return &yaml.Node{Kind: yaml.DocumentNode}, nil
		}
		return nil, fmt.Errorf("mcpcatalog: decode %s: %w", path, err)
	}
	return &n, nil
}

// writeYAMLNodeAtomic marshals root and writes it via temp+fsync+rename. If
// root is empty/zero, writes an empty file. Tabs/spaces follow yaml.v3's
// default 4-space indentation.
func writeYAMLNodeAtomic(path string, root *yaml.Node) error {
	log := fractalog.Component("mcpcatalog")

	// Clean up any orphan temp from a previous crash before writing.
	cleanupOrphanTempsBestEffort(path)

	var buf bytes.Buffer
	if root != nil && root.Kind != 0 {
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(root); err != nil {
			_ = enc.Close()
			return fmt.Errorf("mcpcatalog: encode %s: %w", path, err)
		}
		if err := enc.Close(); err != nil {
			return fmt.Errorf("mcpcatalog: close encoder for %s: %w", path, err)
		}
	}

	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mcpcatalog: mkdir %s: %w", dir, err)
	}

	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+".tmp.*")
	if err != nil {
		return fmt.Errorf("mcpcatalog: create temp for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	cleaned := false
	defer func() {
		if !cleaned {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("mcpcatalog: write temp %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("mcpcatalog: fsync temp %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("mcpcatalog: close temp %s: %w", tmpName, err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("mcpcatalog: rename %s -> %s: %w", tmpName, path, err)
	}
	cleaned = true
	log.Debug("wrote yaml", "path", path, "bytes", buf.Len())
	return nil
}

// cleanupOrphanTempsBestEffort removes stale temp files left behind by a crash
// between fsync and rename. Pattern: .<base>.tmp.*. Best-effort, errors logged
// at debug level.
func cleanupOrphanTempsBestEffort(path string) {
	log := fractalog.Component("mcpcatalog")
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	base := filepath.Base(path)
	prefix := "." + base + ".tmp."
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), prefix) {
			full := filepath.Join(dir, e.Name())
			if err := os.Remove(full); err != nil {
				log.Debug("cleanup orphan temp failed", "path", full, "err", err)
			}
		}
	}
}

// findOrCreateMapping walks root.Content[0] (the document root mapping), then
// descends through keys. For each key in path, ensures a mapping node exists,
// creating empty maps as needed. Returns the deepest mapping node.
func findOrCreateMapping(root *yaml.Node, path []string) (*yaml.Node, error) {
	if root == nil {
		return nil, errors.New("mcpcatalog: nil root")
	}
	if root.Kind == 0 {
		root.Kind = yaml.DocumentNode
	}
	if len(root.Content) == 0 {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.MappingNode})
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("mcpcatalog: top-level node is %v, want mapping", doc.Kind)
	}
	// Force block style — yaml.v3 inherits the parent's flow style for
	// inserted children, so a `servers: {}` flow map needs to be flipped to
	// block style before we insert sub-keys.
	doc.Style = 0
	cur := doc
	for _, key := range path {
		next := mappingGet(cur, key)
		if next == nil {
			child := &yaml.Node{Kind: yaml.MappingNode}
			mappingSet(cur, key, child)
			cur = child
			continue
		}
		if next.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("mcpcatalog: %s is %v, want mapping", key, next.Kind)
		}
		next.Style = 0
		cur = next
	}
	return cur, nil
}

// mappingGet looks up key in a mapping node. Returns nil if absent.
func mappingGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mappingSet inserts or replaces key→value in a mapping node, preserving the
// position of existing keys.
func mappingSet(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		value,
	)
}

// mappingDelete removes key from a mapping node. Returns true if removed.
func mappingDelete(m *yaml.Node, key string) bool {
	if m == nil || m.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}

// UpsertMCPServer inserts or replaces
// mcp_servers.servers.<id>.<modeKey(mode)> with the contents of rendered.
// rendered is parsed as YAML; its top-level value becomes the per-mode block.
// If the entry already exists and force is false, returns an error.
func UpsertMCPServer(root *yaml.Node, id string, mode scaffolds.Kind, rendered []byte, force bool) error {
	key, err := modeKey(mode)
	if err != nil {
		return err
	}
	if id == "" {
		return errors.New("mcpcatalog: UpsertMCPServer: empty id")
	}

	srv, err := findOrCreateMapping(root, []string{"mcp_servers", "servers", id})
	if err != nil {
		return err
	}
	if existing := mappingGet(srv, key); existing != nil && !force {
		return fmt.Errorf("server %s already configured for target-deployment %s; use --force to overwrite", id, mode)
	}

	value, err := decodeRenderedValue(rendered)
	if err != nil {
		return fmt.Errorf("mcpcatalog: parse rendered block for %s/%s: %w", id, mode, err)
	}
	mappingSet(srv, key, value)
	return nil
}

// decodeRenderedValue parses rendered as YAML and returns the value node
// (unwrapping the document node). Empty input yields a nil-valued scalar.
func decodeRenderedValue(rendered []byte) (*yaml.Node, error) {
	if len(bytes.TrimSpace(rendered)) == 0 {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null"}, nil
	}
	var n yaml.Node
	if err := yaml.Unmarshal(rendered, &n); err != nil {
		return nil, err
	}
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null"}, nil
		}
		return n.Content[0], nil
	}
	return &n, nil
}

// RemoveMCPServer removes mcp_servers.servers.<id>.<modeKey(mode)>. If the
// server has no remaining mode entries after removal, also removes the <id>
// entry. Returns nil if nothing was removed (idempotent).
func RemoveMCPServer(root *yaml.Node, id string, mode scaffolds.Kind) error {
	key, err := modeKey(mode)
	if err != nil {
		return err
	}
	if id == "" {
		return errors.New("mcpcatalog: RemoveMCPServer: empty id")
	}
	if root == nil || root.Kind == 0 || len(root.Content) == 0 {
		return nil
	}
	doc := root.Content[0]
	servers := mappingGet(mappingGet(doc, "mcp_servers"), "servers")
	if servers == nil || servers.Kind != yaml.MappingNode {
		return nil
	}
	srv := mappingGet(servers, id)
	if srv == nil || srv.Kind != yaml.MappingNode {
		return nil
	}
	mappingDelete(srv, key)
	if len(srv.Content) == 0 {
		mappingDelete(servers, id)
	}
	return nil
}

// driftProneRE matches scalar string values that yaml.v3 would re-encode
// differently from their plain unquoted form. Covered shapes:
//   - dates: YYYY-MM-DD optionally followed by a time
//   - floats and version-shaped scalars: digits with a single dot (e.g. "1.4")
//   - YAML 1.1 ambiguous booleans: yes/no/on/off (any case) — yaml.v3 itself
//     no longer treats these as booleans on decode, but operators expect them
//     quoted in config files, and any tool that round-trips through yaml.v2
//     would mangle them. Including them preserves the operator's intent.
var driftProneRE = regexp.MustCompile(
	`^(\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?)?|\d+\.\d+|[Yy][Ee][Ss]|[Nn][Oo]|[Oo][Nn]|[Oo][Ff][Ff])$`,
)

// NormalizeFractaYAML rewrites path so subsequent UpsertMCPServer calls produce
// byte-identical output. Walks the *yaml.Node tree and marks drift-prone plain
// scalars (date-shaped, version-shaped like "1.4", ambiguous booleans) with
// DoubleQuotedStyle so a round-trip is stable. Comments and key order are
// preserved. Returns changed=true if the on-disk file was rewritten.
// Idempotent — second call on a normalized file is a no-op.
func NormalizeFractaYAML(path string) (changed bool, err error) {
	log := fractalog.Component("mcpcatalog")
	original, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	root, err := readYAMLNode(path)
	if err != nil {
		return false, err
	}
	walkAndNormalize(root)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if root != nil && root.Kind != 0 {
		if err := enc.Encode(root); err != nil {
			_ = enc.Close()
			return false, fmt.Errorf("mcpcatalog: encode %s: %w", path, err)
		}
	}
	if err := enc.Close(); err != nil {
		return false, fmt.Errorf("mcpcatalog: close encoder for %s: %w", path, err)
	}
	if bytes.Equal(buf.Bytes(), original) {
		return false, nil
	}
	if err := writeYAMLNodeAtomic(path, root); err != nil {
		return false, err
	}
	log.Debug("normalized fracta.yaml", "path", path)
	return true, nil
}

// walkAndNormalize recurses through the yaml node tree, locking drift-prone
// plain string scalars into double-quoted form. Idempotent: scalars already
// quoted are not retouched.
func walkAndNormalize(n *yaml.Node) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode, yaml.MappingNode:
		for _, c := range n.Content {
			walkAndNormalize(c)
		}
	case yaml.ScalarNode:
		if n.Style != 0 {
			// Already explicitly styled (quoted, literal, folded) — leave it.
			return
		}
		if driftProneRE.MatchString(n.Value) {
			// Force string tag + double-quoted style. yaml.v3 may have
			// classified the scalar as !!timestamp / !!float / !!bool — the
			// operator's intent is "treat this as text"; the explicit
			// double-quote is the durable record of that intent.
			n.Style = yaml.DoubleQuotedStyle
			n.Tag = "!!str"
		}
	}
}

// UpsertComposeService inserts or replaces services.<name> in a
// docker-compose.yml node. serviceBlock is parsed as YAML (a mapping whose
// keys are the service's fields) and inserted as the value under
// services.<name>. If the service exists and force is false, returns an error.
func UpsertComposeService(root *yaml.Node, name string, serviceBlock []byte, force bool) error {
	if name == "" {
		return errors.New("mcpcatalog: UpsertComposeService: empty name")
	}
	services, err := findOrCreateMapping(root, []string{"services"})
	if err != nil {
		return err
	}
	if existing := mappingGet(services, name); existing != nil && !force {
		return fmt.Errorf("compose service %s already exists; use --force to overwrite", name)
	}
	value, err := decodeRenderedValue(serviceBlock)
	if err != nil {
		return fmt.Errorf("mcpcatalog: parse compose block for %s: %w", name, err)
	}
	mappingSet(services, name, value)
	return nil
}

// RemoveComposeService removes services.<name>. Idempotent — returns nil if
// services or <name> is absent.
func RemoveComposeService(root *yaml.Node, name string) error {
	if name == "" {
		return errors.New("mcpcatalog: RemoveComposeService: empty name")
	}
	if root == nil || root.Kind == 0 || len(root.Content) == 0 {
		return nil
	}
	doc := root.Content[0]
	services := mappingGet(doc, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil
	}
	mappingDelete(services, name)
	return nil
}

// envVarKeyRE matches the leading `KEY=` or `# KEY=` portion of an env line.
// Operators sometimes comment out lines (`# ES_URL=...`); we treat those as
// already-present so we don't append duplicates.
var envVarKeyRE = regexp.MustCompile(`^\s*#?\s*([A-Za-z_][A-Za-z0-9_]*)\s*=`)

// AppendEnvExample appends KEY= lines for any vars not already present in the
// file. Idempotent: existing keys (commented or not) are skipped. A header
// comment "# Required by <serverID> MCP server" precedes the block, but only
// if the file does not already contain that header.
//
// File is created if absent. Existing content is preserved verbatim.
func AppendEnvExample(path, serverID string, vars []string) error {
	if serverID == "" {
		return errors.New("mcpcatalog: AppendEnvExample: empty serverID")
	}

	var existing []byte
	if data, err := os.ReadFile(path); err == nil {
		existing = data
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("mcpcatalog: read %s: %w", path, err)
	}

	present := parseEnvKeys(existing)
	header := fmt.Sprintf("# Required by %s MCP server", serverID)
	headerPresent := bytes.Contains(existing, []byte(header))

	var toAdd []string
	for _, v := range vars {
		if v == "" {
			continue
		}
		if _, ok := present[v]; ok {
			continue
		}
		toAdd = append(toAdd, v)
		present[v] = struct{}{}
	}
	if len(toAdd) == 0 {
		return nil
	}

	var buf bytes.Buffer
	buf.Write(existing)
	// Ensure we start on a fresh line.
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		buf.WriteByte('\n')
	}
	// Blank line separator before the new block, unless the file is empty.
	if len(existing) > 0 {
		buf.WriteByte('\n')
	}
	if !headerPresent {
		buf.WriteString(header)
		buf.WriteByte('\n')
	}
	for _, v := range toAdd {
		buf.WriteString(v)
		buf.WriteString("=\n")
	}

	return writeFileAtomic(path, buf.Bytes())
}

// parseEnvKeys returns the set of KEY identifiers appearing on any non-blank
// line of data, including commented-out lines like `# FOO=bar`.
func parseEnvKeys(data []byte) map[string]struct{} {
	keys := make(map[string]struct{})
	if len(data) == 0 {
		return keys
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := envVarKeyRE.FindStringSubmatch(line); m != nil {
			keys[m[1]] = struct{}{}
		}
	}
	return keys
}

// writeFileAtomic writes data to path via temp+fsync+rename. Used by
// AppendEnvExample (the env file is a flat text file, not YAML, so it bypasses
// the yaml.Node path).
func writeFileAtomic(path string, data []byte) error {
	cleanupOrphanTempsBestEffort(path)
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mcpcatalog: mkdir %s: %w", dir, err)
	}
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+".tmp.*")
	if err != nil {
		return fmt.Errorf("mcpcatalog: create temp for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	cleaned := false
	defer func() {
		if !cleaned {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("mcpcatalog: write temp %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("mcpcatalog: fsync temp %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("mcpcatalog: close temp %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("mcpcatalog: rename %s -> %s: %w", tmpName, path, err)
	}
	cleaned = true
	return nil
}
