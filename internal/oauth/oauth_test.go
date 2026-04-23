package oauth

import (
	"context"
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"
)

// TestOAuthAPISurface verifies that mcp-go exposes the OAuth types and
// functions required by the MCP authorization spec (2025-11-25).
func TestOAuthAPISurface(t *testing.T) {
	// OAuthAuthorizationRequiredError must be type-assertable
	var err error = &transport.OAuthAuthorizationRequiredError{}
	var oauthErr *transport.OAuthAuthorizationRequiredError
	if !errors.As(err, &oauthErr) {
		t.Error("OAuthAuthorizationRequiredError not type-assertable via errors.As")
	}

	// OAuthConfig must have ProtectedResourceMetadataURL field (RFC 9728)
	cfg := transport.OAuthConfig{
		ProtectedResourceMetadataURL: "https://example.com/.well-known/oauth-protected-resource",
	}
	if cfg.ProtectedResourceMetadataURL == "" {
		t.Error("ProtectedResourceMetadataURL field missing or empty")
	}

	// PKCEEnabled field exists and defaults correctly
	cfg2 := transport.OAuthConfig{PKCEEnabled: true}
	if !cfg2.PKCEEnabled {
		t.Error("PKCEEnabled should be true")
	}

	// S256 PKCE: GenerateCodeVerifier and GenerateCodeChallenge must exist
	verifier, err := transport.GenerateCodeVerifier()
	if err != nil {
		t.Fatalf("GenerateCodeVerifier: %v", err)
	}
	if len(verifier) < 43 {
		t.Errorf("code verifier too short: %d chars (RFC 7636 requires 43-128)", len(verifier))
	}

	challenge := transport.GenerateCodeChallenge(verifier)
	if challenge == "" {
		t.Error("GenerateCodeChallenge returned empty string")
	}
	if challenge == verifier {
		t.Error("S256 challenge must not equal verifier (plain method not acceptable)")
	}

	// GenerateState must produce sufficient entropy
	state, err := transport.GenerateState()
	if err != nil {
		t.Fatalf("GenerateState: %v", err)
	}
	if len(state) < 16 {
		t.Errorf("state too short: %d chars", len(state))
	}

	// OAuthHandler constructor and key methods must exist
	handler := transport.NewOAuthHandler(transport.OAuthConfig{
		TokenStore:  transport.NewMemoryTokenStore(),
		PKCEEnabled: true,
	})
	if handler == nil {
		t.Fatal("NewOAuthHandler returned nil")
	}

	// SetBaseURL must exist
	handler.SetBaseURL("https://example.com")

	// SetProtectedResourceMetadataURL must exist
	handler.SetProtectedResourceMetadataURL("https://example.com/.well-known/oauth-protected-resource")

	// GetClientID / GetClientSecret must exist (empty before registration)
	_ = handler.GetClientID()
	_ = handler.GetClientSecret()

	// code_challenge_methods_supported: mcp-go exposes this field on
	// AuthServerMetadata but does NOT validate S256 presence before auth.
	// fracta adds its own preflight check in cmd/mcp_auth.go (runMCPLogin)
	// that fetches metadata and rejects servers that advertise methods
	// without S256. Verify the metadata type and field exist here.
	meta := &transport.AuthServerMetadata{
		CodeChallengeMethodsSupported: []string{"S256"},
	}
	if len(meta.CodeChallengeMethodsSupported) != 1 || meta.CodeChallengeMethodsSupported[0] != "S256" {
		t.Error("AuthServerMetadata.CodeChallengeMethodsSupported not accessible")
	}
}

// TestOAuthAPISurface_MemoryTokenStore verifies the memory token store contract.
func TestOAuthAPISurface_MemoryTokenStore(t *testing.T) {
	store := transport.NewMemoryTokenStore()
	if store == nil {
		t.Fatal("NewMemoryTokenStore returned nil")
	}

	// ErrNoToken must be the sentinel
	_, err := store.GetToken(context.Background())
	if !errors.Is(err, transport.ErrNoToken) {
		t.Errorf("expected ErrNoToken, got %v", err)
	}
}
