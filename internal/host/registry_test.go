package host

import (
	"errors"
	"testing"
)

func TestMapRegistry_GetResolvesCorrectHost(t *testing.T) {
	reg := NewMapRegistry("alpha")
	hostA := NoopHost{}
	hostB := NoopHost{}
	reg.Register("alpha", hostA)
	reg.Register("beta", hostB)

	got, err := reg.Get("alpha")
	if err != nil {
		t.Fatalf("Get(alpha): %v", err)
	}
	if got != hostA {
		t.Error("Get(alpha) returned wrong host")
	}

	got, err = reg.Get("beta")
	if err != nil {
		t.Fatalf("Get(beta): %v", err)
	}
	if got != hostB {
		t.Error("Get(beta) returned wrong host")
	}
}

func TestMapRegistry_GetUnknownReturnsError(t *testing.T) {
	reg := NewMapRegistry("claude")
	reg.Register("claude", NoopHost{})

	_, err := reg.Get("codex")
	if err == nil {
		t.Fatal("expected error for unknown host type")
	}
	if !errors.Is(err, ErrUnknownHost) {
		t.Errorf("error = %v, want ErrUnknownHost", err)
	}
}

func TestMapRegistry_Default(t *testing.T) {
	reg := NewMapRegistry("claude")
	h := NoopHost{}
	reg.Register("claude", h)

	name, got := reg.Default()
	if name != "claude" {
		t.Errorf("default name = %q, want %q", name, "claude")
	}
	if got != h {
		t.Error("default host mismatch")
	}
}

func TestMapRegistry_DefaultUnregistered(t *testing.T) {
	reg := NewMapRegistry("missing")

	name, got := reg.Default()
	if name != "missing" {
		t.Errorf("default name = %q, want %q", name, "missing")
	}
	if got != nil {
		t.Error("expected nil host for unregistered default")
	}
}
