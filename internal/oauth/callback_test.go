package oauth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestCallbackServer_Success(t *testing.T) {
	srv, err := NewCallbackServer("127.0.0.1:0", "/callback", 5*time.Second)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		resp, err := http.Get(fmt.Sprintf("http://%s/callback?code=abc&state=xyz", srv.Addr()))
		if err != nil {
			t.Errorf("request failed: %v", err)
			return
		}
		resp.Body.Close()
	}()

	result, err := srv.Wait(context.Background())
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if result.Code != "abc" {
		t.Errorf("code = %q, want %q", result.Code, "abc")
	}
	if result.State != "xyz" {
		t.Errorf("state = %q, want %q", result.State, "xyz")
	}
}

func TestCallbackServer_Error(t *testing.T) {
	srv, err := NewCallbackServer("127.0.0.1:0", "/callback", 5*time.Second)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		resp, err := http.Get(fmt.Sprintf("http://%s/callback?error=access_denied", srv.Addr()))
		if err != nil {
			t.Errorf("request failed: %v", err)
			return
		}
		resp.Body.Close()
	}()

	result, err := srv.Wait(context.Background())
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if result.Error != "access_denied" {
		t.Errorf("error = %q, want %q", result.Error, "access_denied")
	}
}

func TestCallbackServer_Timeout(t *testing.T) {
	srv, err := NewCallbackServer("127.0.0.1:0", "/callback", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	_, err = srv.Wait(context.Background())
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestCallbackServer_BusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	defer ln.Close()

	_, err = NewCallbackServer(ln.Addr().String(), "/callback", 5*time.Second)
	if err == nil {
		t.Fatal("expected error for busy port")
	}
}

func TestParseRedirectURI(t *testing.T) {
	tests := []struct {
		uri      string
		wantAddr string
		wantPath string
	}{
		{"http://localhost:9876/callback", "localhost:9876", "/callback"},
		{"http://127.0.0.1:8080/oauth/redirect", "127.0.0.1:8080", "/oauth/redirect"},
		{"http://localhost/callback", "localhost:9876", "/callback"},
	}
	for _, tt := range tests {
		addr, path, err := ParseRedirectURI(tt.uri)
		if err != nil {
			t.Errorf("ParseRedirectURI(%q): %v", tt.uri, err)
			continue
		}
		if addr != tt.wantAddr {
			t.Errorf("ParseRedirectURI(%q) addr = %q, want %q", tt.uri, addr, tt.wantAddr)
		}
		if path != tt.wantPath {
			t.Errorf("ParseRedirectURI(%q) path = %q, want %q", tt.uri, path, tt.wantPath)
		}
	}
}
