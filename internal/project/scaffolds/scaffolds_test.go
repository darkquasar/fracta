package scaffolds

import "testing"

func TestParseKind_Valid(t *testing.T) {
	cases := []struct {
		in   string
		want Kind
	}{
		{"local", KindLocal},
		{"docker-compose", KindDockerCompose},
		{"k8s", KindK8s},
	}
	for _, c := range cases {
		got, err := ParseKind(c.in)
		if err != nil {
			t.Errorf("ParseKind(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseKind(%q) = %v, want %v", c.in, got, c.want)
		}
		// Round-trip via String().
		if got.String() != c.in {
			t.Errorf("Kind(%v).String() = %q, want %q", got, got.String(), c.in)
		}
	}
}

func TestParseKind_Invalid(t *testing.T) {
	for _, in := range []string{"", "compose", "kube", "K8s", "Local"} {
		if _, err := ParseKind(in); err == nil {
			t.Errorf("ParseKind(%q): expected error, got nil", in)
		}
	}
}

func TestAllKinds(t *testing.T) {
	want := []Kind{KindLocal, KindDockerCompose, KindK8s}
	got := AllKinds()
	if len(got) != len(want) {
		t.Fatalf("AllKinds: len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("AllKinds[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}
