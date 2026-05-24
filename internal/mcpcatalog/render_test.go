package mcpcatalog

import (
	"os"
	"strings"
	"testing"

	"github.com/darkquasar/fracta/internal/project/scaffolds"
	"gopkg.in/yaml.v3"
)

func loadFixture(t *testing.T, id string) *Entry {
	t.Helper()
	cat, err := LoadCatalog(os.DirFS("testdata/catalog"))
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	e, ok := cat.Get(id)
	if !ok {
		t.Fatalf("entry %q not found in fixture catalog", id)
	}
	return e
}

func readGolden(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", path, err)
	}
	return b
}

func TestRenderK8sManifest_ElasticGolden(t *testing.T) {
	e := loadFixture(t, "elastic")
	got, err := e.RenderK8sManifest(RenderOpts{})
	if err != nil {
		t.Fatalf("RenderK8sManifest: %v", err)
	}
	want := readGolden(t, "testdata/golden/elastic/k8s.yaml")
	if string(got) != string(want) {
		writeOnMismatch(t, "elastic_k8s_got.yaml", got)
		t.Errorf("elastic k8s render does not match golden\n--- GOT:\n%s--- WANT:\n%s", got, want)
	}
}

func TestRenderK8sManifest_VendorGolden(t *testing.T) {
	e := loadFixture(t, "vendor")
	got, err := e.RenderK8sManifest(RenderOpts{})
	if err != nil {
		t.Fatalf("RenderK8sManifest: %v", err)
	}
	want := readGolden(t, "testdata/golden/vendor/k8s.yaml")
	if string(got) != string(want) {
		writeOnMismatch(t, "vendor_k8s_got.yaml", got)
		t.Errorf("vendor k8s render does not match golden\n--- GOT:\n%s--- WANT:\n%s", got, want)
	}
}

func TestRenderComposeService_ElasticGolden(t *testing.T) {
	e := loadFixture(t, "elastic")
	got, err := e.RenderComposeService(RenderOpts{})
	if err != nil {
		t.Fatalf("RenderComposeService: %v", err)
	}
	want := readGolden(t, "testdata/golden/elastic/compose.yaml")
	if string(got) != string(want) {
		writeOnMismatch(t, "elastic_compose_got.yaml", got)
		t.Errorf("elastic compose render does not match golden\n--- GOT:\n%s--- WANT:\n%s", got, want)
	}
}

// TestRenderK8sManifest_GHCRFractaGolden covers the previously-broken case:
// fracta-owned image distributed via GHCR (empty docker.dockerfile), with
// service_url declaring a non-default port. Asserts:
//   - imagePullPolicy is IfNotPresent (not Never — the image is published,
//     not locally built; the old logic incorrectly used image_owner==fracta
//     alone as the "Never" trigger).
//   - containerPort and Service.port both come from service_url
//     (8000), not the spec-default 3000.
func TestRenderK8sManifest_GHCRFractaGolden(t *testing.T) {
	e := loadFixture(t, "ghcr-fracta")
	got, err := e.RenderK8sManifest(RenderOpts{})
	if err != nil {
		t.Fatalf("RenderK8sManifest: %v", err)
	}
	want := readGolden(t, "testdata/golden/ghcr-fracta/k8s.yaml")
	if string(got) != string(want) {
		writeOnMismatch(t, "ghcr_fracta_k8s_got.yaml", got)
		t.Errorf("ghcr-fracta k8s render does not match golden\n--- GOT:\n%s--- WANT:\n%s", got, want)
	}
}

// TestPortFromServiceURL covers the parser used by the port-resolution
// cascade in RenderK8sManifest.
func TestPortFromServiceURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"with port", "http://foo.svc:8000/mcp", 8000},
		{"with port high", "http://foo.svc:30000/path", 30000},
		{"no port", "http://foo.svc/mcp", 0},
		{"https no port", "https://example.com/mcp", 0},
		{"malformed", "::not-a-url::", 0},
		{"port-only-no-scheme", "foo.svc:8000", 0}, // url.Parse treats foo.svc as scheme
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := portFromServiceURL(c.in); got != c.want {
				t.Errorf("portFromServiceURL(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

func TestRenderK8sManifest_NotionBlocked(t *testing.T) {
	e := loadFixture(t, "notion")
	_, err := e.RenderK8sManifest(RenderOpts{})
	if err == nil {
		t.Fatalf("expected error rendering notion k8s")
	}
	msg := err.Error()
	if !strings.Contains(msg, "kubernetes") {
		t.Errorf("err missing 'kubernetes': %s", msg)
	}
	if !strings.Contains(msg, "blocked_until_gateway_oauth_token_store") {
		t.Errorf("err missing support-gate text: %s", msg)
	}
}

func TestRenderK8sSecretStub_NoEnv(t *testing.T) {
	// Synthetic entry with no env_required.
	e := &Entry{
		ID: "naked",
		Auth: AuthSpec{
			EnvRequired: nil,
		},
	}
	out, err := e.RenderK8sSecretStub(RenderOpts{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if out != nil {
		t.Errorf("expected nil; got %s", out)
	}
}

func TestRenderK8sSecretStub_Elastic(t *testing.T) {
	e := loadFixture(t, "elastic")
	out, err := e.RenderK8sSecretStub(RenderOpts{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"elastic-mcp-secrets", "url:", "api-key:"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("secret stub missing %q in output:\n%s", want, out)
		}
	}
}

func TestRenderFractaYAMLBlock_K8s(t *testing.T) {
	e := loadFixture(t, "elastic")
	out, err := e.RenderFractaYAMLBlock(scaffolds.KindK8s, RenderOpts{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"url: http://elastic-mcp.fracta.svc:3000/mcp", "transport: streamable-http"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("block missing %q in:\n%s", want, out)
		}
	}
	for _, absent := range []string{"elastic:", "remote:"} {
		if strings.Contains(string(out), absent) {
			t.Errorf("block should NOT contain wrapper key %q (leaf-only rendering):\n%s", absent, out)
		}
	}
}

func TestRenderFractaYAMLBlock_Local(t *testing.T) {
	e := loadFixture(t, "elastic")
	out, err := e.RenderFractaYAMLBlock(scaffolds.KindLocal, RenderOpts{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"command: podman", "args: [run"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("block missing %q in:\n%s", want, out)
		}
	}
	for _, absent := range []string{"elastic:", "local:"} {
		if strings.Contains(string(out), absent) {
			t.Errorf("block should NOT contain wrapper key %q (leaf-only rendering):\n%s", absent, out)
		}
	}
}

func TestRenderFractaYAMLBlock_ModeUnsupported(t *testing.T) {
	e := loadFixture(t, "vendor")
	_, err := e.RenderFractaYAMLBlock(scaffolds.KindLocal, RenderOpts{})
	if err == nil {
		t.Fatalf("expected error: vendor.support.local_process == not_supported")
	}
}

func TestRenderThenUpsert_NoDoubleNesting(t *testing.T) {
	cases := []struct {
		name    string
		mode    scaffolds.Kind
		modeKey string
	}{
		{"k8s", scaffolds.KindK8s, "remote"},
		{"docker-compose", scaffolds.KindDockerCompose, "remote"},
		{"local", scaffolds.KindLocal, "local"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := loadFixture(t, "elastic")
			rendered, err := e.RenderFractaYAMLBlock(tc.mode, RenderOpts{})
			if err != nil {
				t.Fatalf("render: %v", err)
			}

			root := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{
				{Kind: yaml.MappingNode},
			}}
			if err := UpsertMCPServer(root, "elastic", tc.mode, rendered, false); err != nil {
				t.Fatalf("upsert: %v", err)
			}

			out, err := yaml.Marshal(root)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var cfg map[string]interface{}
			if err := yaml.Unmarshal(out, &cfg); err != nil {
				t.Fatalf("unmarshal result: %v", err)
			}

			servers, ok := cfg["mcp_servers"].(map[string]interface{})["servers"].(map[string]interface{})
			if !ok {
				t.Fatalf("missing mcp_servers.servers in:\n%s", out)
			}
			elastic, ok := servers["elastic"].(map[string]interface{})
			if !ok {
				t.Fatalf("missing mcp_servers.servers.elastic in:\n%s", out)
			}
			modeBlock, ok := elastic[tc.modeKey].(map[string]interface{})
			if !ok {
				t.Fatalf("missing mcp_servers.servers.elastic.%s in:\n%s", tc.modeKey, out)
			}
			if _, doubled := modeBlock["elastic"]; doubled {
				t.Errorf("double-nesting detected: mcp_servers.servers.elastic.%s.elastic exists in:\n%s", tc.modeKey, out)
			}
		})
	}
}

func writeOnMismatch(t *testing.T, name string, data []byte) {
	t.Helper()
	tmp := t.TempDir() + "/" + name
	_ = os.WriteFile(tmp, data, 0o644)
	t.Logf("wrote actual render to %s for inspection", tmp)
}

func TestLongestCommonPrefixUnderscore(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"ES_URL", "ES_API_KEY"}, "ES_"},
		{[]string{"VENDOR_MCP_FOO", "VENDOR_MCP_BAR"}, "VENDOR_MCP_"},
		{[]string{"NOTHING_SHARED", "DIFFERENT"}, ""},
		{[]string{"SINGLE_VAR"}, "SINGLE_"},
		{[]string{}, ""},
	}
	for _, c := range cases {
		got := longestCommonPrefixUnderscore(c.in)
		if got != c.want {
			t.Errorf("LCP(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestKebabAfterPrefix(t *testing.T) {
	got := kebabAfterPrefix("ES_API_KEY", "ES_")
	if got != "api-key" {
		t.Errorf("got %q, want api-key", got)
	}
	got = kebabAfterPrefix("ES_URL", "ES_")
	if got != "url" {
		t.Errorf("got %q, want url", got)
	}
}
