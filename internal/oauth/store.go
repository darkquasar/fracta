package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/zalando/go-keyring"
)

const (
	serviceName = "fracta.oauth"
	// keyringTimeout is the default timeout for keyring operations.
	// Follows GitHub CLI's pattern of capping keyring calls.
	keyringTimeout = 3 * time.Second
)

var ErrNoToken = errors.New("no token available")
var ErrNoClientRegistration = errors.New("no client registration available")
var ErrTokenTooLarge = errors.New("token payload exceeds OS keyring size limit; use token_file instead")

// ClientRegistration holds dynamically registered OAuth client credentials.
type ClientRegistration struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
}

// OAuthCredentialStore persists OAuth tokens and client registrations per server.
type OAuthCredentialStore interface {
	GetToken(ctx context.Context, server string) (*transport.Token, error)
	SaveToken(ctx context.Context, server string, token *transport.Token) error
	DeleteToken(ctx context.Context, server string) error
	GetClientRegistration(ctx context.Context, server string) (*ClientRegistration, error)
	SaveClientRegistration(ctx context.Context, server string, reg *ClientRegistration) error
	DeleteClientRegistration(ctx context.Context, server string) error
}

// KeyringCredentialStore uses the OS keyring (via zalando/go-keyring) for token persistence.
// macOS: Keychain via /usr/bin/security (no CGO)
// Linux: Secret Service over D-Bus
// Windows: Credential Manager
type KeyringCredentialStore struct{}

func NewKeyringCredentialStore() *KeyringCredentialStore {
	return &KeyringCredentialStore{}
}

func tokenUser(server string) string  { return server + ":token" }
func clientUser(server string) string { return server + ":client" }

func (s *KeyringCredentialStore) GetToken(ctx context.Context, server string) (*transport.Token, error) {
	val, err := keyringGet(ctx, serviceName, tokenUser(server))
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, ErrNoToken
		}
		return nil, fmt.Errorf("keyring get token: %w", err)
	}
	var tok transport.Token
	if err := json.Unmarshal([]byte(val), &tok); err != nil {
		return nil, fmt.Errorf("unmarshal token: %w", err)
	}
	return &tok, nil
}

func (s *KeyringCredentialStore) SaveToken(ctx context.Context, server string, token *transport.Token) error {
	data, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}
	if len(data) > 2500 {
		return ErrTokenTooLarge
	}
	return keyringSet(ctx, serviceName, tokenUser(server), string(data))
}

func (s *KeyringCredentialStore) DeleteToken(ctx context.Context, server string) error {
	err := keyringDelete(ctx, serviceName, tokenUser(server))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

func (s *KeyringCredentialStore) GetClientRegistration(ctx context.Context, server string) (*ClientRegistration, error) {
	val, err := keyringGet(ctx, serviceName, clientUser(server))
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, ErrNoClientRegistration
		}
		return nil, fmt.Errorf("keyring get client reg: %w", err)
	}
	var reg ClientRegistration
	if err := json.Unmarshal([]byte(val), &reg); err != nil {
		return nil, fmt.Errorf("unmarshal client reg: %w", err)
	}
	return &reg, nil
}

func (s *KeyringCredentialStore) SaveClientRegistration(ctx context.Context, server string, reg *ClientRegistration) error {
	data, err := json.Marshal(reg)
	if err != nil {
		return fmt.Errorf("marshal client reg: %w", err)
	}
	if len(data) > 2500 {
		return ErrTokenTooLarge
	}
	return keyringSet(ctx, serviceName, clientUser(server), string(data))
}

func (s *KeyringCredentialStore) DeleteClientRegistration(ctx context.Context, server string) error {
	err := keyringDelete(ctx, serviceName, clientUser(server))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Context-aware wrappers for zalando/go-keyring (which has no context support).
// Uses a goroutine + select pattern with timeout.

func keyringGet(ctx context.Context, service, user string) (string, error) {
	type result struct {
		val string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := keyring.Get(service, user)
		ch <- result{v, err}
	}()
	deadline := keyringTimeout
	if d, ok := ctx.Deadline(); ok {
		if remaining := time.Until(d); remaining < deadline {
			deadline = remaining
		}
	}
	select {
	case r := <-ch:
		return r.val, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(deadline):
		return "", fmt.Errorf("keyring get timed out after %s", deadline)
	}
}

func keyringSet(ctx context.Context, service, user, password string) error {
	ch := make(chan error, 1)
	go func() {
		ch <- keyring.Set(service, user, password)
	}()
	deadline := keyringTimeout
	if d, ok := ctx.Deadline(); ok {
		if remaining := time.Until(d); remaining < deadline {
			deadline = remaining
		}
	}
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(deadline):
		return fmt.Errorf("keyring set timed out after %s", deadline)
	}
}

func keyringDelete(ctx context.Context, service, user string) error {
	ch := make(chan error, 1)
	go func() {
		ch <- keyring.Delete(service, user)
	}()
	deadline := keyringTimeout
	if d, ok := ctx.Deadline(); ok {
		if remaining := time.Until(d); remaining < deadline {
			deadline = remaining
		}
	}
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(deadline):
		return fmt.Errorf("keyring delete timed out after %s", deadline)
	}
}

// PerServerTokenStore adapts OAuthCredentialStore to mcp-go's transport.TokenStore interface
// for a specific server name.
type PerServerTokenStore struct {
	Store  OAuthCredentialStore
	Server string
}

func (p *PerServerTokenStore) GetToken(ctx context.Context) (*transport.Token, error) {
	tok, err := p.Store.GetToken(ctx, p.Server)
	if errors.Is(err, ErrNoToken) {
		return nil, transport.ErrNoToken
	}
	return tok, err
}

func (p *PerServerTokenStore) SaveToken(ctx context.Context, token *transport.Token) error {
	return p.Store.SaveToken(ctx, p.Server, token)
}
