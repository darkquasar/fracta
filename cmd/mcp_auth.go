package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/oauth"
	"github.com/darkquasar/fracta/internal/secrets"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/spf13/cobra"
)

// Flag globals shared by the new 'fracta config mcp auth ...' wiring
// (cmd/config_mcp_auth.go) and the deprecation alias (cmd/mcp_alias.go).
// The top-level 'fracta mcp <verb>' Cobra wiring that previously lived in
// this file moved to those two files in spec-43.
var (
	mcpLoginDeviceCode bool
	mcpExportFormat    string
	mcpExportOutputDir string
)

func runMCPLogin(cmd *cobra.Command, args []string) error {
	serverName := args[0]

	cfg, err := loadMCPAuthConfig()
	if err != nil {
		return err
	}

	entry, ok := cfg.MCPServers.Servers[serverName]
	if !ok {
		return fmt.Errorf("server %q not found in config", serverName)
	}
	remote, ok := entry.EffectiveRemote()
	if !ok {
		return fmt.Errorf("server %q has no remote config", serverName)
	}
	if remote.Auth == nil || remote.Auth.Type != "oauth" {
		return fmt.Errorf("server %q does not have OAuth auth configured", serverName)
	}

	auth := remote.Auth

	// Device code flow
	if mcpLoginDeviceCode {
		return runDeviceCodeLogin(serverName, auth, remote.URL, cfg)
	}

	redirectURI := auth.EffectiveRedirectURI()
	addr, path, err := oauth.ParseRedirectURI(redirectURI)
	if err != nil {
		return fmt.Errorf("parse redirect_uri: %w", err)
	}

	// Bind callback server before opening browser
	cbServer, err := oauth.NewCallbackServer(addr, path, 120*time.Second)
	if err != nil {
		return fmt.Errorf("start callback server: %w", err)
	}

	// Build OAuth handler from mcp-go
	oauthCfg := transport.OAuthConfig{
		RedirectURI:           redirectURI,
		Scopes:                auth.Scopes,
		PKCEEnabled:           auth.EffectivePKCE(),
		AuthServerMetadataURL: auth.MetadataURL,
		TokenStore:            transport.NewMemoryTokenStore(),
	}

	// Resolve client_id if configured
	if auth.ClientID != nil {
		id, resolveErr := resolveSecret(auth.ClientID)
		if resolveErr != nil {
			return fmt.Errorf("resolve client_id: %w", resolveErr)
		}
		oauthCfg.ClientID = id
	}
	if auth.ClientSecret != nil {
		sec, resolveErr := resolveSecret(auth.ClientSecret)
		if resolveErr != nil {
			return fmt.Errorf("resolve client_secret: %w", resolveErr)
		}
		oauthCfg.ClientSecret = sec
	}

	// Identity resolution: client_registration_file → credential store → dynamic registration
	if oauthCfg.ClientID == "" && auth.ClientRegistrationFile != "" {
		reg, regErr := oauth.LoadClientRegistrationFile(auth.ClientRegistrationFile)
		if regErr != nil {
			return fmt.Errorf("client_registration_file: %w", regErr)
		}
		oauthCfg.ClientID = reg.ClientID
		oauthCfg.ClientSecret = reg.ClientSecret
	}
	store, storeErr := buildCredStore(cfg)
	if storeErr == nil && oauthCfg.ClientID == "" {
		reg, regErr := store.GetClientRegistration(context.Background(), serverName)
		if regErr == nil {
			oauthCfg.ClientID = reg.ClientID
			oauthCfg.ClientSecret = reg.ClientSecret
		}
	}

	// Create the OAuth handler
	handler := transport.NewOAuthHandler(oauthCfg)
	handler.SetBaseURL(remote.URL)

	// Dynamic client registration if no client_id
	originalClientID := oauthCfg.ClientID
	if oauthCfg.ClientID == "" {
		if err := handler.RegisterClient(context.Background(), "fracta"); err != nil {
			return fmt.Errorf("dynamic client registration: %w", err)
		}
	}

	// Persist dynamically registered client
	if storeErr == nil && handler.GetClientID() != "" && originalClientID == "" {
		_ = store.SaveClientRegistration(context.Background(), serverName, &oauth.ClientRegistration{
			ClientID:     handler.GetClientID(),
			ClientSecret: handler.GetClientSecret(),
		})
	}

	// Validate PKCE S256 support before proceeding
	if auth.EffectivePKCE() {
		meta, metaErr := handler.GetServerMetadata(context.Background())
		if metaErr != nil {
			return fmt.Errorf("fetch auth server metadata: %w", metaErr)
		}
		if len(meta.CodeChallengeMethodsSupported) > 0 {
			s256Found := false
			for _, m := range meta.CodeChallengeMethodsSupported {
				if m == "S256" {
					s256Found = true
					break
				}
			}
			if !s256Found {
				return fmt.Errorf("auth server does not support S256 PKCE (advertised: %v); cannot proceed securely", meta.CodeChallengeMethodsSupported)
			}
		}
		// If code_challenge_methods_supported is absent, RFC 7636 §4.2 says
		// the server MAY still accept S256 — proceed optimistically.
	}

	// Generate PKCE + state
	state, err := transport.GenerateState()
	if err != nil {
		return fmt.Errorf("generate state: %w", err)
	}
	codeVerifier, err := transport.GenerateCodeVerifier()
	if err != nil {
		return fmt.Errorf("generate code verifier: %w", err)
	}
	codeChallenge := transport.GenerateCodeChallenge(codeVerifier)

	// Get authorization URL
	authURL, err := handler.GetAuthorizationURL(context.Background(), state, codeChallenge)
	if err != nil {
		return fmt.Errorf("get authorization URL: %w", err)
	}

	// Open browser
	fmt.Printf("Opening browser to authorize with %s...\n", serverName)
	if err := oauth.OpenBrowser(authURL); err != nil {
		fmt.Printf("Could not open browser. Please visit:\n  %s\n", authURL)
	}

	// Wait for callback
	fmt.Println("Waiting for authorization callback...")
	result, err := cbServer.Wait(context.Background())
	if err != nil {
		return err
	}
	if result.Error != "" {
		return fmt.Errorf("authorization failed: %s", result.Error)
	}
	if result.State != state {
		return fmt.Errorf("state mismatch: possible CSRF attack")
	}

	// Exchange code for token via ProcessAuthorizationResponse
	err = handler.ProcessAuthorizationResponse(context.Background(), result.Code, state, codeVerifier)
	if err != nil {
		return fmt.Errorf("exchange code for token: %w", err)
	}

	// Retrieve the saved token from the handler's token store
	token, err := oauthCfg.TokenStore.GetToken(context.Background())
	if err != nil {
		return fmt.Errorf("get token after exchange: %w", err)
	}

	// Persist token to OS keyring
	if storeErr == nil {
		if err := store.SaveToken(context.Background(), serverName, token); err != nil {
			return fmt.Errorf("save token: %w", err)
		}
	}

	fmt.Printf("Successfully authenticated with %s!\n", serverName)
	if token.Scope != "" {
		fmt.Printf("Scopes: %s\n", token.Scope)
	}
	return nil
}

func runMCPLogout(cmd *cobra.Command, args []string) error {
	serverName := args[0]

	cfg, err := loadMCPAuthConfig()
	if err != nil {
		return err
	}

	store, err := buildCredStore(cfg)
	if err != nil {
		return fmt.Errorf("build credential store: %w", err)
	}

	ctx := context.Background()
	_ = store.DeleteToken(ctx, serverName)
	_ = store.DeleteClientRegistration(ctx, serverName)

	fmt.Printf("Removed credentials for %s\n", serverName)
	return nil
}

func runMCPAuthStatus(cmd *cobra.Command, args []string) error {
	cfg, err := loadMCPAuthConfig()
	if err != nil {
		return err
	}

	store, err := buildCredStore(cfg)
	if err != nil {
		return fmt.Errorf("build credential store: %w", err)
	}

	ctx := context.Background()

	// Filter to specific server or show all OAuth servers
	servers := make(map[string]config.MCPServerRemote)
	if len(args) > 0 {
		entry, ok := cfg.MCPServers.Servers[args[0]]
		if !ok {
			return fmt.Errorf("server %q not found", args[0])
		}
		if remote, ok := entry.EffectiveRemote(); ok {
			servers[args[0]] = remote
		}
	} else {
		for name, entry := range cfg.MCPServers.Servers {
			if remote, ok := entry.EffectiveRemote(); ok {
				if remote.Auth != nil && remote.Auth.Type == "oauth" {
					servers[name] = remote
				}
			}
		}
	}

	if len(servers) == 0 {
		fmt.Println("No OAuth-configured MCP servers found.")
		return nil
	}

	fmt.Printf("%-20s %-12s %-30s %s\n", "SERVER", "STATUS", "SCOPES", "EXPIRES")
	fmt.Printf("%-20s %-12s %-30s %s\n", "------", "------", "------", "-------")

	for name := range servers {
		tok, err := store.GetToken(ctx, name)
		if err != nil {
			fmt.Printf("%-20s %-12s %-30s %s\n", name, "no token", "-", "-")
			continue
		}
		status := "valid"
		if tok.IsExpired() {
			status = "expired"
		}
		expiry := "-"
		if !tok.ExpiresAt.IsZero() {
			expiry = tok.ExpiresAt.Format("2006-01-02 15:04")
		}
		scope := tok.Scope
		if scope == "" {
			scope = "-"
		}
		fmt.Printf("%-20s %-12s %-30s %s\n", name, status, scope, expiry)
	}
	return nil
}

func loadMCPAuthConfig() (*config.Config, error) {
	if configFlag != "" {
		return config.LoadConfig(configFlag)
	}
	return loadConfigOrDefault(projectRoot)
}

func buildCredStore(cfg *config.Config) (oauth.OAuthCredentialStore, error) {
	factory := oauth.NewCredentialStoreFactory(cfg.TokenStore)
	return factory.Build()
}

func resolveSecret(sv *config.SecretValue) (string, error) {
	if sv == nil {
		return "", nil
	}
	return secrets.Resolve(sv)
}

func runDeviceCodeLogin(serverName string, auth *config.MCPServerAuth, serverURL string, cfg *config.Config) error {
	var clientID string
	if auth.ClientID != nil {
		id, err := resolveSecret(auth.ClientID)
		if err != nil {
			return fmt.Errorf("resolve client_id: %w", err)
		}
		clientID = id
	}
	if clientID == "" {
		return fmt.Errorf("device code flow requires client_id")
	}

	dcResp, tokenURL, err := oauth.RequestDeviceCode(context.Background(), oauth.DeviceCodeConfig{
		ClientID:    clientID,
		Scopes:      auth.Scopes,
		MetadataURL: auth.MetadataURL,
		ServerURL:   serverURL,
	})
	if err != nil {
		return fmt.Errorf("request device code: %w", err)
	}

	fmt.Printf("To authorize, visit: %s\n", dcResp.VerificationURI)
	fmt.Printf("Enter code: %s\n\n", dcResp.UserCode)
	if dcResp.VerificationURIComplete != "" {
		fmt.Printf("Or open: %s\n\n", dcResp.VerificationURIComplete)
	}
	fmt.Println("Waiting for authorization...")

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(dcResp.ExpiresIn)*time.Second)
	defer cancel()

	token, err := oauth.PollForToken(ctx, tokenURL, clientID, dcResp.DeviceCode, dcResp.Interval)
	if err != nil {
		return err
	}

	store, err := buildCredStore(cfg)
	if err != nil {
		return fmt.Errorf("build credential store: %w", err)
	}
	if err := store.SaveToken(context.Background(), serverName, token); err != nil {
		return fmt.Errorf("save token: %w", err)
	}

	fmt.Printf("Successfully authenticated with %s!\n", serverName)
	return nil
}

func runMCPExport(cmd *cobra.Command, args []string) error {
	serverName := args[0]

	cfg, err := loadMCPAuthConfig()
	if err != nil {
		return err
	}

	store, err := buildCredStore(cfg)
	if err != nil {
		return fmt.Errorf("build credential store: %w", err)
	}

	ctx := context.Background()
	tok, err := store.GetToken(ctx, serverName)
	if err != nil {
		return fmt.Errorf("no token stored for %s: %w", serverName, err)
	}

	reg, _ := store.GetClientRegistration(ctx, serverName)

	switch mcpExportFormat {
	case "env":
		return exportEnv(serverName, tok, reg)
	case "k8s-secret":
		return exportK8sSecret(serverName, tok, reg)
	case "files":
		return exportFiles(serverName, tok, reg)
	default:
		return fmt.Errorf("unknown format %q (supported: env, k8s-secret, files)", mcpExportFormat)
	}
}

func envKey(server, suffix string) string {
	return "FRACTA_MCP_" + strings.ToUpper(strings.ReplaceAll(server, "-", "_")) + "_" + suffix
}

func exportEnv(server string, tok *transport.Token, reg *oauth.ClientRegistration) error {
	fmt.Printf("%s=%s\n", envKey(server, "ACCESS_TOKEN"), tok.AccessToken)
	if tok.RefreshToken != "" {
		fmt.Printf("%s=%s\n", envKey(server, "REFRESH_TOKEN"), tok.RefreshToken)
	}
	if tok.TokenType != "" {
		fmt.Printf("%s=%s\n", envKey(server, "TOKEN_TYPE"), tok.TokenType)
	}
	if reg != nil {
		fmt.Printf("%s=%s\n", envKey(server, "CLIENT_ID"), reg.ClientID)
		if reg.ClientSecret != "" {
			fmt.Printf("%s=%s\n", envKey(server, "CLIENT_SECRET"), reg.ClientSecret)
		}
	}
	return nil
}

func exportK8sSecret(server string, tok *transport.Token, reg *oauth.ClientRegistration) error {
	tokJSON, _ := json.MarshalIndent(tok, "    ", "  ")
	fmt.Printf(`apiVersion: v1
kind: Secret
metadata:
  name: fracta-mcp-%s
type: Opaque
stringData:
  token.json: |
    %s
`, server, string(tokJSON))
	if reg != nil {
		regJSON, _ := json.MarshalIndent(reg, "    ", "  ")
		fmt.Printf("  client-registration.json: |\n    %s\n", string(regJSON))
	}
	return nil
}

func exportFiles(server string, tok *transport.Token, reg *oauth.ClientRegistration) error {
	dir := mcpExportOutputDir
	if dir == "" {
		dir = "."
	}

	tokJSON, _ := json.MarshalIndent(tok, "", "  ")
	tokPath := filepath.Join(dir, server+"-token.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	if err := os.WriteFile(tokPath, append(tokJSON, '\n'), 0o600); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}
	fmt.Printf("Wrote %s\n", tokPath)

	if reg != nil {
		regJSON, _ := json.MarshalIndent(reg, "", "  ")
		regPath := filepath.Join(dir, server+"-client-registration.json")
		if err := os.WriteFile(regPath, append(regJSON, '\n'), 0o600); err != nil {
			return fmt.Errorf("write client registration file: %w", err)
		}
		fmt.Printf("Wrote %s\n", regPath)
	}
	return nil
}
