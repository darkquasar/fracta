package mcpcatalog

import (
	"os"
	"testing"

	"github.com/darkquasar/fracta/internal/project/scaffolds"
	"gopkg.in/yaml.v3"
)

func decodeFixture(t *testing.T, path string) *Entry {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var e Entry
	if err := yaml.Unmarshal(raw, &e); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return &e
}

func TestEntry_SupportsMode(t *testing.T) {
	elastic := decodeFixture(t, "testdata/catalog/elastic/server.yaml")
	notion := decodeFixture(t, "testdata/catalog/notion/server.yaml")
	vendor := decodeFixture(t, "testdata/catalog/vendor/server.yaml")

	cases := []struct {
		name string
		e    *Entry
		k    scaffolds.Kind
		want bool
	}{
		{"elastic/local", elastic, scaffolds.KindLocal, true},
		{"elastic/compose", elastic, scaffolds.KindDockerCompose, true},
		{"elastic/k8s", elastic, scaffolds.KindK8s, true},
		{"notion/local", notion, scaffolds.KindLocal, true},
		{"notion/compose-blocked", notion, scaffolds.KindDockerCompose, false},
		{"notion/k8s-blocked", notion, scaffolds.KindK8s, false},
		{"vendor/local-not-supported", vendor, scaffolds.KindLocal, false},
		{"vendor/compose", vendor, scaffolds.KindDockerCompose, true},
		{"vendor/k8s", vendor, scaffolds.KindK8s, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.e.SupportsMode(c.k); got != c.want {
				t.Errorf("SupportsMode(%v) = %v, want %v (note=%q)", c.k, got, c.want, c.e.SupportNote(c.k))
			}
		})
	}
}

func TestEntry_SupportNote(t *testing.T) {
	notion := decodeFixture(t, "testdata/catalog/notion/server.yaml")
	if got := notion.SupportNote(scaffolds.KindK8s); got != "blocked_until_gateway_oauth_token_store" {
		t.Errorf("notion k8s support note = %q, want blocked_until_gateway_oauth_token_store", got)
	}
}

func TestEntry_PreferredVariant(t *testing.T) {
	elastic := decodeFixture(t, "testdata/catalog/elastic/server.yaml")
	notion := decodeFixture(t, "testdata/catalog/notion/server.yaml")
	vendor := decodeFixture(t, "testdata/catalog/vendor/server.yaml")

	if v, ok := elastic.PreferredVariant(scaffolds.KindLocal); !ok || v != "local" {
		t.Errorf("elastic local variant = (%q,%v), want (\"local\", true)", v, ok)
	}
	if v, ok := elastic.PreferredVariant(scaffolds.KindK8s); !ok || v != "container" {
		t.Errorf("elastic k8s variant = (%q,%v), want (\"container\", true)", v, ok)
	}
	if v, ok := notion.PreferredVariant(scaffolds.KindLocal); !ok || v != "local_proxy" {
		t.Errorf("notion local variant = (%q,%v), want (\"local_proxy\", true)", v, ok)
	}
	if _, ok := vendor.PreferredVariant(scaffolds.KindLocal); ok {
		t.Errorf("vendor has no local variant; PreferredVariant should be false")
	}
}

func TestEntry_RequiresImageBuild(t *testing.T) {
	elastic := decodeFixture(t, "testdata/catalog/elastic/server.yaml")
	vendor := decodeFixture(t, "testdata/catalog/vendor/server.yaml")
	if elastic.RequiresImageBuild() {
		t.Errorf("elastic should NOT require build (external image)")
	}
	if !vendor.RequiresImageBuild() {
		t.Errorf("vendor SHOULD require build (image_owner=fracta + dockerfile set)")
	}
}

func TestEntry_ImageRef(t *testing.T) {
	elastic := decodeFixture(t, "testdata/catalog/elastic/server.yaml")
	notion := decodeFixture(t, "testdata/catalog/notion/server.yaml")
	if got := elastic.ImageRef(); got != "docker.elastic.co/mcp/elasticsearch:latest" {
		t.Errorf("elastic ImageRef = %q", got)
	}
	if got := notion.ImageRef(); got != "" {
		t.Errorf("notion (remote-only) ImageRef = %q, want empty", got)
	}
}
