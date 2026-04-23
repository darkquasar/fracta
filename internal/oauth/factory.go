package oauth

import (
	"fmt"

	"github.com/darkquasar/fracta/internal/config"
)

// CredentialStoreFactory builds an OAuthCredentialStore from config.
type CredentialStoreFactory struct {
	cfg config.TokenStoreConfig
}

func NewCredentialStoreFactory(cfg config.TokenStoreConfig) *CredentialStoreFactory {
	return &CredentialStoreFactory{cfg: cfg}
}

// Build returns the configured credential store.
func (f *CredentialStoreFactory) Build() (OAuthCredentialStore, error) {
	driver := f.cfg.Driver
	if driver == "" {
		driver = "auto"
	}
	switch driver {
	case "auto", "keyring":
		return NewKeyringCredentialStore(), nil
	case "file":
		return nil, fmt.Errorf("local credential file store not implemented; use OAuth token_file for mounted/pre-authorized tokens")
	default:
		return nil, fmt.Errorf("unknown token_store driver: %q (supported: auto, keyring)", driver)
	}
}
