package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
)

// ClientCredentialsConfig holds configuration for the client credentials grant.
type ClientCredentialsConfig struct {
	ClientID     string
	ClientSecret string
	Scopes       []string
	TokenURL     string // explicit token endpoint; if empty, discovered from metadata
	MetadataURL  string // auth server metadata URL for discovery
	ServerURL    string // MCP server base URL for metadata discovery fallback
}

// FetchClientCredentialsToken obtains a token using the OAuth 2.0 client credentials grant.
func FetchClientCredentialsToken(ctx context.Context, cfg ClientCredentialsConfig) (*transport.Token, error) {
	tokenURL := cfg.TokenURL
	if tokenURL == "" {
		discovered, err := discoverTokenEndpoint(ctx, cfg.MetadataURL, cfg.ServerURL)
		if err != nil {
			return nil, fmt.Errorf("discover token endpoint: %w", err)
		}
		tokenURL = discovered
	}

	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
	}
	if len(cfg.Scopes) > 0 {
		data.Set("scope", strings.Join(cfg.Scopes, " "))
	}

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tok transport.Token
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("token response missing access_token")
	}

	// Set ExpiresAt from ExpiresIn if present
	if tok.ExpiresIn > 0 && tok.ExpiresAt.IsZero() {
		tok.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}

	return &tok, nil
}

// discoverTokenEndpoint finds the token endpoint from auth server metadata.
func discoverTokenEndpoint(ctx context.Context, metadataURL, serverURL string) (string, error) {
	if metadataURL == "" && serverURL != "" {
		u, err := url.Parse(serverURL)
		if err != nil {
			return "", fmt.Errorf("parse server URL: %w", err)
		}
		metadataURL = fmt.Sprintf("%s://%s/.well-known/oauth-authorization-server", u.Scheme, u.Host)
	}
	if metadataURL == "" {
		return "", fmt.Errorf("no metadata_url or server URL to discover token endpoint")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", metadataURL, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("metadata endpoint returned %d", resp.StatusCode)
	}

	var meta struct {
		TokenEndpoint string `json:"token_endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", fmt.Errorf("parse metadata: %w", err)
	}
	if meta.TokenEndpoint == "" {
		return "", fmt.Errorf("metadata missing token_endpoint")
	}
	return meta.TokenEndpoint, nil
}
