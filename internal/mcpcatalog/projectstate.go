package mcpcatalog

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/darkquasar/fracta/internal/project/scaffolds"
	"gopkg.in/yaml.v3"
)

// ProjectState summarises the operator project tree for spec-43:
//   - which scaffold modes are enabled
//   - which servers are configured for which modes
//
// The struct is populated by LoadProjectState. Missing files mean "mode not
// enabled" rather than an error — pre-init projects also produce a valid
// (all-false) ProjectState.
type ProjectState struct {
	// EnabledScaffolds[k] is true when scaffold k is enabled. Detection
	// rules per spec-43 plan §1.2:
	//
	//   local           runtime.backend == "local" AND
	//                   deployment/docker-compose.yml does NOT exist
	//   docker-compose  runtime.backend == "local" AND
	//                   deployment/docker-compose.yml exists
	//   k8s             runtime.backend == "kubernetes"
	EnabledScaffolds map[scaffolds.Kind]bool

	// Configured[id][k] is true when server id is configured for mode k.
	// Detection rules:
	//   local           fracta.yaml mcp_servers.servers.<id>.local exists
	//   docker-compose  deployment/docker-compose.yml services.<id>-mcp exists
	//   k8s             deployment/k8s/manifests/<id>-mcp.yaml exists on disk
	Configured map[string]map[scaffolds.Kind]bool
}

// OnlyEnabled returns the single enabled scaffold kind when exactly one of
// local/docker-compose/k8s is enabled. Used to default --target-deployment.
func (s *ProjectState) OnlyEnabled() (scaffolds.Kind, bool) {
	var found scaffolds.Kind
	count := 0
	for _, k := range scaffolds.AllKinds() {
		if s.EnabledScaffolds[k] {
			found = k
			count++
		}
	}
	if count == 1 {
		return found, true
	}
	return 0, false
}

// LoadProjectState walks fracta.yaml, deployment/docker-compose.yml, and
// deployment/k8s/manifests/ to determine which scaffolds are present and which
// servers are configured for which modes.
//
// Returns an empty (all-false) ProjectState rather than an error when no
// fracta.yaml exists — pre-init projects are a valid state.
func LoadProjectState(projectRoot string) (*ProjectState, error) {
	state := &ProjectState{
		EnabledScaffolds: map[scaffolds.Kind]bool{},
		Configured:       map[string]map[scaffolds.Kind]bool{},
	}

	fractaYAMLPath := filepath.Join(projectRoot, "fracta.yaml")
	raw, err := os.ReadFile(fractaYAMLPath)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return nil, fmt.Errorf("mcpcatalog: read fracta.yaml: %w", err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("mcpcatalog: decode fracta.yaml: %w", err)
	}
	doc := docMappingNode(&root)
	if doc == nil {
		return state, nil
	}

	backend := scalarAt(doc, "runtime", "backend")
	composePath := filepath.Join(projectRoot, "deployment", "docker-compose.yml")
	composeExists := fileExists(composePath)

	switch backend {
	case "kubernetes":
		state.EnabledScaffolds[scaffolds.KindK8s] = true
	case "local":
		if composeExists {
			state.EnabledScaffolds[scaffolds.KindDockerCompose] = true
		} else {
			state.EnabledScaffolds[scaffolds.KindLocal] = true
		}
	}

	if servers := mappingAt(doc, "mcp_servers", "servers"); servers != nil {
		for i := 0; i+1 < len(servers.Content); i += 2 {
			id := servers.Content[i].Value
			body := servers.Content[i+1]
			if body == nil || body.Kind != yaml.MappingNode {
				continue
			}
			for j := 0; j+1 < len(body.Content); j += 2 {
				key := body.Content[j].Value
				switch key {
				case "local":
					ensureConfigured(state, id, scaffolds.KindLocal)
				case "remote":
					// remote can be either compose or k8s; we infer by
					// looking at the URL. If service ends in fracta.svc it's
					// k8s; if it looks like an in-compose svc DNS we map to
					// compose. When ambiguous, we mark both — the operator
					// will resolve via --target-deployment.
					url := scalarAt(body.Content[j+1], "url", "")
					if url == "" {
						// Some fracta.yaml drafts use service_url; fall back.
						url = scalarAt(body.Content[j+1], "service_url", "")
					}
					if isLikelyK8sURL(url) {
						ensureConfigured(state, id, scaffolds.KindK8s)
					} else if url != "" {
						ensureConfigured(state, id, scaffolds.KindDockerCompose)
					}
				}
			}
		}
	}

	// docker-compose detection: parse services and look for <id>-mcp.
	if composeExists {
		composeRaw, err := os.ReadFile(composePath)
		if err == nil {
			var composeRoot yaml.Node
			if err := yaml.Unmarshal(composeRaw, &composeRoot); err == nil {
				cdoc := docMappingNode(&composeRoot)
				if services := mappingAt(cdoc, "services"); services != nil {
					for i := 0; i+1 < len(services.Content); i += 2 {
						svcName := services.Content[i].Value
						if id, ok := stripSuffix(svcName, "-mcp"); ok && id != "" {
							ensureConfigured(state, id, scaffolds.KindDockerCompose)
						}
					}
				}
			}
		}
	}

	// k8s detection: scan deployment/k8s/manifests/ for <id>-mcp.yaml files.
	manifestsDir := filepath.Join(projectRoot, "deployment", "k8s", "manifests")
	entries, err := os.ReadDir(manifestsDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if id, ok := stripSuffix(name, "-mcp.yaml"); ok && id != "" {
				ensureConfigured(state, id, scaffolds.KindK8s)
			}
		}
	}

	return state, nil
}

func ensureConfigured(s *ProjectState, id string, k scaffolds.Kind) {
	if s.Configured[id] == nil {
		s.Configured[id] = map[scaffolds.Kind]bool{}
	}
	s.Configured[id][k] = true
}

// isLikelyK8sURL returns true when the URL hostname includes ".svc" or
// ".svc.cluster.local". Heuristic — operators with custom DNS may need
// --target-deployment.
func isLikelyK8sURL(url string) bool {
	if url == "" {
		return false
	}
	return containsAny(url, ".svc:", ".svc.cluster.local", ".svc/")
}

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if len(n) == 0 {
			continue
		}
		for i := 0; i+len(n) <= len(haystack); i++ {
			if haystack[i:i+len(n)] == n {
				return true
			}
		}
	}
	return false
}

func stripSuffix(s, suffix string) (string, bool) {
	if len(s) < len(suffix) {
		return "", false
	}
	if s[len(s)-len(suffix):] != suffix {
		return "", false
	}
	return s[:len(s)-len(suffix)], true
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// docMappingNode unwraps a yaml.Node DocumentNode to its inner mapping.
func docMappingNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		return n.Content[0]
	}
	if n.Kind == yaml.MappingNode {
		return n
	}
	return nil
}

// mappingAt walks a chain of keys through a mapping and returns the mapping
// node found at the leaf, or nil if any step is missing or non-mapping.
func mappingAt(m *yaml.Node, keys ...string) *yaml.Node {
	cur := m
	for _, k := range keys {
		if cur == nil || cur.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(cur.Content); i += 2 {
			if cur.Content[i].Value == k {
				next = cur.Content[i+1]
				break
			}
		}
		cur = next
	}
	if cur == nil || cur.Kind != yaml.MappingNode {
		return nil
	}
	return cur
}

// scalarAt walks a chain of keys through a mapping and returns the leaf
// scalar value, or "" if any step is missing.
func scalarAt(m *yaml.Node, keys ...string) string {
	cur := m
	for i, k := range keys {
		if cur == nil {
			return ""
		}
		if cur.Kind != yaml.MappingNode {
			return ""
		}
		var next *yaml.Node
		for j := 0; j+1 < len(cur.Content); j += 2 {
			if cur.Content[j].Value == k {
				next = cur.Content[j+1]
				break
			}
		}
		if i == len(keys)-1 {
			if next != nil && next.Kind == yaml.ScalarNode {
				return next.Value
			}
			return ""
		}
		cur = next
	}
	return ""
}
