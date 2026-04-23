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

// DeviceCodeConfig holds configuration for the device code flow.
type DeviceCodeConfig struct {
	ClientID    string
	Scopes      []string
	MetadataURL string
	ServerURL   string
}

// DeviceCodeResponse holds the initial device authorization response.
type DeviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// RequestDeviceCode initiates the device code flow.
func RequestDeviceCode(ctx context.Context, cfg DeviceCodeConfig) (*DeviceCodeResponse, string, error) {
	tokenURL, deviceAuthURL, err := discoverDeviceEndpoints(ctx, cfg.MetadataURL, cfg.ServerURL)
	if err != nil {
		return nil, "", fmt.Errorf("discover endpoints: %w", err)
	}

	data := url.Values{
		"client_id": {cfg.ClientID},
	}
	if len(cfg.Scopes) > 0 {
		data.Set("scope", strings.Join(cfg.Scopes, " "))
	}

	req, err := http.NewRequestWithContext(ctx, "POST", deviceAuthURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("device authorization request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("device authorization returned %d: %s", resp.StatusCode, string(body))
	}

	var dcResp DeviceCodeResponse
	if err := json.Unmarshal(body, &dcResp); err != nil {
		return nil, "", fmt.Errorf("parse device code response: %w", err)
	}
	return &dcResp, tokenURL, nil
}

// PollForToken polls the token endpoint until the user authorizes or timeout.
func PollForToken(ctx context.Context, tokenURL, clientID, deviceCode string, interval int) (*transport.Token, error) {
	if interval < 5 {
		interval = 5
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			tok, pending, err := pollOnce(ctx, tokenURL, clientID, deviceCode)
			if err != nil {
				return nil, err
			}
			if pending {
				continue
			}
			return tok, nil
		}
	}
}

func pollOnce(ctx context.Context, tokenURL, clientID, deviceCode string) (*transport.Token, bool, error) {
	data := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"client_id":   {clientID},
		"device_code": {deviceCode},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode == http.StatusOK {
		var tok transport.Token
		if err := json.Unmarshal(body, &tok); err != nil {
			return nil, false, fmt.Errorf("parse token: %w", err)
		}
		if tok.ExpiresIn > 0 && tok.ExpiresAt.IsZero() {
			tok.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
		}
		return &tok, false, nil
	}

	// Check for pending/slow_down errors
	var errResp struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &errResp) == nil {
		switch errResp.Error {
		case "authorization_pending", "slow_down":
			return nil, true, nil
		case "expired_token":
			return nil, false, fmt.Errorf("device code expired — please restart the login flow")
		case "access_denied":
			return nil, false, fmt.Errorf("access denied by user")
		}
	}

	return nil, false, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
}

func discoverDeviceEndpoints(ctx context.Context, metadataURL, serverURL string) (tokenURL, deviceAuthURL string, err error) {
	if metadataURL == "" && serverURL != "" {
		u, err := url.Parse(serverURL)
		if err != nil {
			return "", "", err
		}
		metadataURL = fmt.Sprintf("%s://%s/.well-known/oauth-authorization-server", u.Scheme, u.Host)
	}
	if metadataURL == "" {
		return "", "", fmt.Errorf("no metadata URL available for device code flow")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", metadataURL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("metadata returned %d", resp.StatusCode)
	}

	var meta struct {
		TokenEndpoint               string `json:"token_endpoint"`
		DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", "", err
	}
	if meta.TokenEndpoint == "" {
		return "", "", fmt.Errorf("metadata missing token_endpoint")
	}
	if meta.DeviceAuthorizationEndpoint == "" {
		return "", "", fmt.Errorf("metadata missing device_authorization_endpoint")
	}
	return meta.TokenEndpoint, meta.DeviceAuthorizationEndpoint, nil
}
