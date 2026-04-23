package oauth

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/client/transport"
)

// LoadTokenFile reads a JSON token file and returns the parsed token.
func LoadTokenFile(path string) (*transport.Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read token file %s: %w", path, err)
	}
	var tok transport.Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, fmt.Errorf("parse token file %s: %w", path, err)
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("token file %s: missing access_token", path)
	}
	return &tok, nil
}

// LoadClientRegistrationFile reads a JSON client registration file.
func LoadClientRegistrationFile(path string) (*ClientRegistration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read client registration file %s: %w", path, err)
	}
	var reg ClientRegistration
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parse client registration file %s: %w", path, err)
	}
	if reg.ClientID == "" {
		return nil, fmt.Errorf("client registration file %s: missing client_id", path)
	}
	return &reg, nil
}

// MemoryCredentialStore is an in-memory implementation for pre-seeded tokens.
type MemoryCredentialStore struct {
	tokens map[string]*transport.Token
	regs   map[string]*ClientRegistration
}

func NewMemoryCredentialStore() *MemoryCredentialStore {
	return &MemoryCredentialStore{
		tokens: make(map[string]*transport.Token),
		regs:   make(map[string]*ClientRegistration),
	}
}

func (m *MemoryCredentialStore) SeedToken(server string, tok *transport.Token) {
	m.tokens[server] = tok
}

func (m *MemoryCredentialStore) SeedClientRegistration(server string, reg *ClientRegistration) {
	m.regs[server] = reg
}
