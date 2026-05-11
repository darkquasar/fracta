package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkquasar/fracta/internal/mcpcatalog"
	"github.com/darkquasar/fracta/internal/project/scaffolds"
)

// tempProject builds a minimal fracta project tree at t.TempDir() and sets the
// package-global projectRoot for the duration of the test. Restores on cleanup.
//
// The optional opts control which fixtures get laid down:
//   - catalogYAML: contents of <root>/mcp-servers/catalog.yaml. Empty → no
//     mcp-servers/ directory (lets tests verify ErrNoCatalog).
//   - entries: map of server-id → server.yaml contents. Each id gets written
//     to <root>/mcp-servers/<id>/server.yaml.
//   - fractaYAML: contents of <root>/fracta.yaml. Empty → not written (lets
//     tests verify pre-init state).
func tempProject(t *testing.T, opts tempProjectOpts) string {
	t.Helper()
	root := t.TempDir()
	if opts.fractaYAML != "" {
		writeFile(t, filepath.Join(root, "fracta.yaml"), opts.fractaYAML)
	}
	if opts.catalogYAML != "" {
		writeFile(t, filepath.Join(root, "mcp-servers", "catalog.yaml"), opts.catalogYAML)
		for id, body := range opts.entries {
			writeFile(t, filepath.Join(root, "mcp-servers", id, "server.yaml"), body)
		}
	}

	prev := projectRoot
	projectRoot = root
	t.Cleanup(func() { projectRoot = prev })
	return root
}

type tempProjectOpts struct {
	catalogYAML string
	entries     map[string]string
	fractaYAML  string
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// minimalCatalog produces a 2-entry catalog.yaml + entries for tests that need
// a working catalog but don't care about specific shapes.
func minimalCatalog() (string, map[string]string) {
	cat := `version: "1"
description: test catalog
servers:
  - id: elastic
    path: elastic/server.yaml
  - id: notion
    path: notion/server.yaml
`
	entries := map[string]string{
		"elastic": `id: elastic
name: Elastic MCP
category: security
status: tested
description: Elasticsearch
upstream: { type: vendor, url: https://www.elastic.co/ }
auth:
  modes: [env_token]
  env_required: [ES_URL, ES_API_KEY]
variants:
  container:
    image: docker.elastic.co/mcp/elasticsearch:latest
    image_owner: external
    transport: streamable-http
    service_url: http://elastic-mcp.fracta.svc:3000/mcp
  local:
    transport: stdio
    command: podman
    args: [run, -i, --rm, -e, ES_URL, -e, ES_API_KEY, docker.elastic.co/mcp/elasticsearch, stdio]
support:
  local_process: supported
  docker_compose: supported
  kubernetes: supported
`,
		"notion": `id: notion
name: Notion
category: knowledge
status: documented
description: Notion KG
upstream: { type: vendor, url: https://www.notion.so/ }
auth:
  modes: [oauth]
variants:
  remote:
    transport: streamable-http
    url: https://mcp.notion.com/mcp
    auth: oauth
    fracta_native: blocked_until_gateway_oauth
  local_proxy:
    transport: stdio
    command: npx
    args: [-y, mcp-remote, https://mcp.notion.com/mcp]
support:
  local_process: supported_via_local_proxy
  docker_compose: not_supported
  kubernetes: blocked_until_gateway_oauth_token_store
`,
	}
	return cat, entries
}

func TestConfigMcpListNoCatalogRemediation(t *testing.T) {
	tempProject(t, tempProjectOpts{
		fractaYAML: "runtime:\n  backend: local\n",
	})
	listOutputFlag = "table"
	listTargetDeploymentFlag = ""
	listFilterFlag = ""
	listNoImageStateFlag = true
	listRemoteFlag = false

	err := runConfigMcpList(configMcpListCmd, nil)
	if err == nil {
		t.Fatalf("expected ErrNoCatalog remediation, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no catalog found at") {
		t.Errorf("error missing 'no catalog' phrase: %q", msg)
	}
	if !strings.Contains(msg, "fracta config mcp fetch") {
		t.Errorf("error missing remediation command: %q", msg)
	}
}

func TestConfigMcpListJSONShape(t *testing.T) {
	cat, entries := minimalCatalog()
	tempProject(t, tempProjectOpts{
		catalogYAML: cat,
		entries:     entries,
		fractaYAML:  "runtime:\n  backend: kubernetes\n",
	})
	listOutputFlag = "json"
	listTargetDeploymentFlag = "all"
	listFilterFlag = ""
	listNoImageStateFlag = true
	listRemoteFlag = false

	out, err := captureStdout(t, func() error {
		return runConfigMcpList(configMcpListCmd, nil)
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var rows []listRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("decode JSON output: %v\nout=%q", err, out)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	// Rows are in SortedIDs() order.
	if rows[0].ID != "elastic" || rows[1].ID != "notion" {
		t.Errorf("row order: got [%s, %s]", rows[0].ID, rows[1].ID)
	}
	if rows[0].AuthModes[0] != "env_token" {
		t.Errorf("elastic auth modes: %v", rows[0].AuthModes)
	}
	if rows[1].AuthModes[0] != "oauth" {
		t.Errorf("notion auth modes: %v", rows[1].AuthModes)
	}
}

func TestConfigMcpListFilterByStatus(t *testing.T) {
	cat, entries := minimalCatalog()
	tempProject(t, tempProjectOpts{
		catalogYAML: cat,
		entries:     entries,
		fractaYAML:  "runtime:\n  backend: kubernetes\n",
	})
	listOutputFlag = "json"
	listTargetDeploymentFlag = "all"
	listFilterFlag = "status=tested"
	listNoImageStateFlag = true
	listRemoteFlag = false

	out, err := captureStdout(t, func() error {
		return runConfigMcpList(configMcpListCmd, nil)
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var rows []listRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("decode JSON: %v\nout=%q", err, out)
	}
	if len(rows) != 1 || rows[0].ID != "elastic" {
		t.Fatalf("expected only elastic; got %+v", rows)
	}
}

func TestResolveTargetDeploymentFilterDefaults(t *testing.T) {
	cases := []struct {
		name    string
		flag    string
		enabled map[scaffolds.Kind]bool
		want    []scaffolds.Kind
		wantErr bool
	}{
		{
			name:    "empty flag, only-k8s scaffold",
			flag:    "",
			enabled: map[scaffolds.Kind]bool{scaffolds.KindK8s: true},
			want:    []scaffolds.Kind{scaffolds.KindK8s},
		},
		{
			name:    "empty flag, no scaffold → all",
			flag:    "",
			enabled: map[scaffolds.Kind]bool{},
			want:    scaffolds.AllKinds(),
		},
		{
			name:    "explicit local",
			flag:    "local",
			enabled: map[scaffolds.Kind]bool{scaffolds.KindK8s: true},
			want:    []scaffolds.Kind{scaffolds.KindLocal},
		},
		{
			name:    "explicit all",
			flag:    "all",
			enabled: map[scaffolds.Kind]bool{scaffolds.KindK8s: true},
			want:    scaffolds.AllKinds(),
		},
		{
			name:    "unknown flag errors",
			flag:    "kubectl",
			enabled: map[scaffolds.Kind]bool{},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := &mcpcatalog.ProjectState{EnabledScaffolds: tc.enabled}
			got, err := resolveTargetDeploymentFilter(tc.flag, state)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len(got)=%d, want %d", len(got), len(tc.want))
			}
			for i, k := range tc.want {
				if got[i] != k {
					t.Errorf("got[%d]=%v, want %v", i, got[i], k)
				}
			}
		})
	}
}

// captureStdout swaps os.Stdout for the duration of fn and returns what was
// written. The cobra.Command writes also use os.Stdout via OutOrStdout().
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan struct{})
	var buf strings.Builder
	go func() {
		bufRaw := make([]byte, 4096)
		for {
			n, err := r.Read(bufRaw)
			if n > 0 {
				buf.Write(bufRaw[:n])
			}
			if err != nil {
				close(done)
				return
			}
		}
	}()

	runErr := fn()
	_ = w.Close()
	<-done
	return buf.String(), runErr
}
