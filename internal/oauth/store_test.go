package oauth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/zalando/go-keyring"
)

func TestKeyringCredentialStore_TokenRoundTrip(t *testing.T) {
	keyring.MockInit()
	store := NewKeyringCredentialStore()
	ctx := context.Background()

	// No token initially
	_, err := store.GetToken(ctx, "notion")
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("expected ErrNoToken, got %v", err)
	}

	// Save and retrieve
	tok := &transport.Token{
		AccessToken:  "access-123",
		RefreshToken: "refresh-456",
		TokenType:    "Bearer",
	}
	if err := store.SaveToken(ctx, "notion", tok); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.GetToken(ctx, "notion")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AccessToken != "access-123" {
		t.Errorf("access_token = %q", got.AccessToken)
	}
	if got.RefreshToken != "refresh-456" {
		t.Errorf("refresh_token = %q", got.RefreshToken)
	}

	// Delete
	if err := store.DeleteToken(ctx, "notion"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = store.GetToken(ctx, "notion")
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("after delete: expected ErrNoToken, got %v", err)
	}
}

func TestKeyringCredentialStore_ClientRegistrationRoundTrip(t *testing.T) {
	keyring.MockInit()
	store := NewKeyringCredentialStore()
	ctx := context.Background()

	// No registration initially
	_, err := store.GetClientRegistration(ctx, "notion")
	if !errors.Is(err, ErrNoClientRegistration) {
		t.Fatalf("expected ErrNoClientRegistration, got %v", err)
	}

	reg := &ClientRegistration{
		ClientID:     "dyn-client-id",
		ClientSecret: "dyn-secret",
	}
	if err := store.SaveClientRegistration(ctx, "notion", reg); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.GetClientRegistration(ctx, "notion")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ClientID != "dyn-client-id" {
		t.Errorf("client_id = %q", got.ClientID)
	}
	if got.ClientSecret != "dyn-secret" {
		t.Errorf("client_secret = %q", got.ClientSecret)
	}

	// Delete
	if err := store.DeleteClientRegistration(ctx, "notion"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = store.GetClientRegistration(ctx, "notion")
	if !errors.Is(err, ErrNoClientRegistration) {
		t.Fatalf("after delete: expected ErrNoClientRegistration, got %v", err)
	}
}

func TestKeyringCredentialStore_DeleteNotFoundIsSuccess(t *testing.T) {
	keyring.MockInit()
	store := NewKeyringCredentialStore()
	ctx := context.Background()

	// Deleting nonexistent key should not error
	if err := store.DeleteToken(ctx, "nonexistent"); err != nil {
		t.Errorf("delete token: %v", err)
	}
	if err := store.DeleteClientRegistration(ctx, "nonexistent"); err != nil {
		t.Errorf("delete client reg: %v", err)
	}
}

func TestKeyringCredentialStore_ContextCancellation(t *testing.T) {
	keyring.MockInit()
	store := NewKeyringCredentialStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.GetToken(ctx, "notion")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	err = store.SaveToken(ctx, "notion", &transport.Token{AccessToken: "x"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on save, got %v", err)
	}

	err = store.DeleteToken(ctx, "notion")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on delete, got %v", err)
	}
}

func TestKeyringCredentialStore_ContextTimeout(t *testing.T) {
	keyring.MockInit()
	store := NewKeyringCredentialStore()
	// Very short deadline that's already expired
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	_, err := store.GetToken(ctx, "notion")
	if err == nil {
		t.Fatal("expected error for expired context")
	}
}

func TestKeyringCredentialStore_TokenTooLarge(t *testing.T) {
	keyring.MockInit()
	store := NewKeyringCredentialStore()
	ctx := context.Background()

	// Create a token with an oversized access_token
	bigToken := &transport.Token{
		AccessToken: strings.Repeat("x", 3000),
		TokenType:   "Bearer",
	}
	err := store.SaveToken(ctx, "notion", bigToken)
	if !errors.Is(err, ErrTokenTooLarge) {
		t.Errorf("expected ErrTokenTooLarge, got %v", err)
	}
}

func TestKeyringCredentialStore_ClientRegistrationTooLarge(t *testing.T) {
	keyring.MockInit()
	store := NewKeyringCredentialStore()
	ctx := context.Background()

	bigReg := &ClientRegistration{
		ClientID:     strings.Repeat("x", 3000),
		ClientSecret: "secret",
	}
	err := store.SaveClientRegistration(ctx, "server", bigReg)
	if !errors.Is(err, ErrTokenTooLarge) {
		t.Errorf("expected ErrTokenTooLarge, got %v", err)
	}
}

func TestPerServerTokenStore_MapsErrNoToken(t *testing.T) {
	keyring.MockInit()
	store := NewKeyringCredentialStore()
	adapter := &PerServerTokenStore{Store: store, Server: "notion"}
	ctx := context.Background()

	_, err := adapter.GetToken(ctx)
	if !errors.Is(err, transport.ErrNoToken) {
		t.Errorf("expected transport.ErrNoToken, got %v", err)
	}
}

func TestPerServerTokenStore_SaveAndGet(t *testing.T) {
	keyring.MockInit()
	store := NewKeyringCredentialStore()
	adapter := &PerServerTokenStore{Store: store, Server: "notion"}
	ctx := context.Background()

	tok := &transport.Token{AccessToken: "via-adapter", TokenType: "Bearer"}
	if err := adapter.SaveToken(ctx, tok); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := adapter.GetToken(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AccessToken != "via-adapter" {
		t.Errorf("access_token = %q", got.AccessToken)
	}
}

func TestKeyringCredentialStore_MultipleServers(t *testing.T) {
	keyring.MockInit()
	store := NewKeyringCredentialStore()
	ctx := context.Background()

	// Save tokens for two different servers
	tok1 := &transport.Token{AccessToken: "server1-token", TokenType: "Bearer"}
	tok2 := &transport.Token{AccessToken: "server2-token", TokenType: "Bearer"}

	if err := store.SaveToken(ctx, "server1", tok1); err != nil {
		t.Fatalf("save server1: %v", err)
	}
	if err := store.SaveToken(ctx, "server2", tok2); err != nil {
		t.Fatalf("save server2: %v", err)
	}

	got1, _ := store.GetToken(ctx, "server1")
	got2, _ := store.GetToken(ctx, "server2")

	if got1.AccessToken != "server1-token" {
		t.Errorf("server1 token = %q", got1.AccessToken)
	}
	if got2.AccessToken != "server2-token" {
		t.Errorf("server2 token = %q", got2.AccessToken)
	}

	// Delete one doesn't affect the other
	store.DeleteToken(ctx, "server1")
	_, err := store.GetToken(ctx, "server1")
	if !errors.Is(err, ErrNoToken) {
		t.Errorf("server1 should be gone")
	}
	got2, _ = store.GetToken(ctx, "server2")
	if got2.AccessToken != "server2-token" {
		t.Errorf("server2 should still exist")
	}
}

func TestCredentialStoreFactory_Auto(t *testing.T) {
	keyring.MockInit()
	factory := NewCredentialStoreFactory(config.TokenStoreConfig{})
	store, err := factory.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestCredentialStoreFactory_Keyring(t *testing.T) {
	keyring.MockInit()
	factory := NewCredentialStoreFactory(config.TokenStoreConfig{Driver: "keyring"})
	store, err := factory.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestCredentialStoreFactory_FileUnsupported(t *testing.T) {
	factory := NewCredentialStoreFactory(config.TokenStoreConfig{Driver: "file"})
	_, err := factory.Build()
	if err == nil {
		t.Fatal("expected error for file driver")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("error should mention 'not implemented': %v", err)
	}
}

func TestCredentialStoreFactory_Unknown(t *testing.T) {
	factory := NewCredentialStoreFactory(config.TokenStoreConfig{Driver: "magic"})
	_, err := factory.Build()
	if err == nil {
		t.Fatal("expected error for unknown driver")
	}
}
