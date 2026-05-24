package mcpclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/fractalog"
	oauthpkg "github.com/darkquasar/fracta/internal/oauth"
	"github.com/darkquasar/fracta/internal/secrets"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// ConnState represents the lifecycle state of one MCP server connection.
type ConnState int

const (
	ConnIdle       ConnState = iota // not yet connected
	ConnConnecting                  // connect in progress (one goroutine owns this)
	ConnReady                       // connected, initialized, tools cached
	ConnFailed                      // last connect/call failed
)

// serverEntry holds the per-server connection state.
type serverEntry struct {
	mu          sync.Mutex
	state       ConnState
	client      *client.Client
	tools       map[string]mcp.Tool
	connectedAt time.Time
	lastErr     error
	waiters     *sync.Cond
}

func newServerEntry() *serverEntry {
	e := &serverEntry{state: ConnIdle}
	e.waiters = sync.NewCond(&e.mu)
	return e
}

// ServerInfo exposes the full state of a server in the pool.
type ServerInfo struct {
	Name    string
	State   ConnState
	LastErr error
	Config  config.MCPServerEntry
}

// Pool manages per-server MCP client connections with explicit lifecycle.
type Pool struct {
	mu                  sync.Mutex
	servers             map[string]*serverEntry
	config              config.MCPServersConfig
	backend             string
	logger              *slog.Logger
	events              events.Bus
	toolsChangedHandler func(server string)
	newTransportFn      func(config.MCPServerEntry, string) (transport.Interface, error) // nil = default; tests inject here
	credStoreFactory    CredentialStoreFactory
}

// CredentialStoreFactory builds a credential store for OAuth token persistence.
type CredentialStoreFactory interface {
	Build() (OAuthCredentialStore, error)
}

// OAuthCredentialStore is the subset of oauth.OAuthCredentialStore needed by Pool.
type OAuthCredentialStore interface {
	GetToken(ctx context.Context, server string) (*transport.Token, error)
	SaveToken(ctx context.Context, server string, token *transport.Token) error
	GetClientRegistration(ctx context.Context, server string) (*OAuthClientRegistration, error)
}

// OAuthClientRegistration holds dynamically registered OAuth client credentials.
type OAuthClientRegistration struct {
	ClientID     string
	ClientSecret string
}

// PoolOption configures a Pool.
type PoolOption func(*Pool)

// WithCredentialStoreFactory sets the OAuth credential store factory.
func WithCredentialStoreFactory(f CredentialStoreFactory) PoolOption {
	return func(p *Pool) {
		p.credStoreFactory = f
	}
}

// NewPool creates a new MCP client pool.
// backend is retained for call-site compatibility; transport selection is
// determined by each MCP server entry (remote/local/kubernetes alias).
func NewPool(cfg config.MCPServersConfig, backend string, opts ...PoolOption) *Pool {
	p := &Pool{
		servers: make(map[string]*serverEntry),
		config:  cfg,
		backend: backend,
		logger:  fractalog.Component("mcpclient"),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// SetEventBus attaches an event bus for emitting connection and tool events.
func (p *Pool) SetEventBus(bus events.Bus) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = bus
}

// emitEvent emits an event if the bus is configured.
func (p *Pool) emitEvent(ctx context.Context, e events.Event) {
	if p.events != nil {
		p.events.Emit(ctx, e)
	}
}

// CallTool implements ToolCaller. It connects on first use, validates the tool,
// and normalizes the response.
func (p *Pool) CallTool(ctx context.Context, server, tool string, args map[string]any) (*ToolResult, error) {
	entry, err := p.getOrConnect(ctx, server)
	if err != nil {
		return nil, fmt.Errorf("connecting to %q: %w", server, err)
	}

	// Validate tool exists (from cached ListTools)
	entry.mu.Lock()
	_, ok := entry.tools[tool]
	c := entry.client
	entry.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("tool %q not found on server %q", tool, server)
	}

	// Call tool using the client pointer grabbed above. This pointer may become
	// stale if a concurrent goroutine marks this server ConnFailed and a subsequent
	// caller reconnects (closing the old client). In that case, c.CallTool returns
	// a transport error, which is propagated to the caller as a resolution error.
	// This race is declared acceptable: no ref-counting needed, callers tolerate
	// individual failures, and the reconnecting goroutine creates a fresh client.
	callReq := mcp.CallToolRequest{}
	callReq.Params.Name = tool
	callReq.Params.Arguments = args
	result, err := c.CallTool(ctx, callReq)
	if err != nil {
		// Transport error — mark failed for reconnect on next call
		entry.mu.Lock()
		entry.state = ConnFailed
		entry.lastErr = err
		entry.mu.Unlock()
		return nil, fmt.Errorf("calling %q on %q: %w", tool, server, err)
	}

	return normalizeResult(result)
}

// Close closes all managed MCP client connections.
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var firstErr error
	for name, entry := range p.servers {
		entry.mu.Lock()
		if entry.client != nil {
			if err := entry.client.Close(); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("closing %q: %w", name, err)
			}
			entry.client = nil
		}
		entry.state = ConnIdle
		entry.mu.Unlock()
	}
	return firstErr
}

// AddServer registers a server in the pool in idle state. It does NOT connect —
// connection happens lazily on DiscoverTools/CallTool. If the server already
// exists, its config is updated and the entry is left in its current state.
func (p *Pool) AddServer(name string, entry config.MCPServerEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.config.Servers == nil {
		p.config.Servers = make(map[string]config.MCPServerEntry)
	}
	p.config.Servers[name] = entry

	if _, ok := p.servers[name]; !ok {
		p.servers[name] = newServerEntry()
	}

	p.logger.Info("pool: server added", "server", name)
}

// DisconnectServer closes the connection and removes the server from the pool entirely.
func (p *Pool) DisconnectServer(name string) {
	p.mu.Lock()
	entry, ok := p.servers[name]
	if ok {
		delete(p.servers, name)
		delete(p.config.Servers, name)
	}
	p.mu.Unlock()

	if ok {
		entry.mu.Lock()
		if entry.client != nil {
			entry.client.Close()
			entry.client = nil
		}
		entry.state = ConnIdle
		entry.mu.Unlock()
	}

	p.logger.Info("pool: server disconnected", "server", name)
}

// KnownServers returns state info for ALL servers in the pool, regardless of
// connection state. The reconciler uses this to diff desired vs actual.
func (p *Pool) KnownServers() []ServerInfo {
	p.mu.Lock()
	defer p.mu.Unlock()

	result := make([]ServerInfo, 0, len(p.servers))
	for name, entry := range p.servers {
		entry.mu.Lock()
		info := ServerInfo{
			Name:    name,
			State:   entry.state,
			LastErr: entry.lastErr,
		}
		entry.mu.Unlock()

		if cfg, ok := p.config.Servers[name]; ok {
			info.Config = cfg
		}
		result = append(result, info)
	}
	return result
}

// SetToolsChangedHandler registers a callback invoked when a backend server
// sends a tools/list_changed notification. The reconciler uses this to trigger
// targeted reconciliation.
func (p *Pool) SetToolsChangedHandler(fn func(server string)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.toolsChangedHandler = fn
}

// getOrConnect returns a connected serverEntry, connecting on first use or after failure.
func (p *Pool) getOrConnect(ctx context.Context, name string) (*serverEntry, error) {
	serverCfg, ok := p.config.Servers[name]
	if !ok {
		return nil, fmt.Errorf("server %q not configured", name)
	}

	entry := p.getEntry(name)

	entry.mu.Lock()
	for {
		switch entry.state {
		case ConnReady:
			entry.mu.Unlock()
			return entry, nil

		case ConnConnecting:
			// Wait for the connecting goroutine to finish
			entry.waiters.Wait()
			// Loop to recheck state

		case ConnIdle, ConnFailed:
			// We take ownership of connecting
			entry.state = ConnConnecting
			entry.mu.Unlock()

			err := p.connect(ctx, entry, name, serverCfg)

			entry.mu.Lock()
			if err != nil {
				entry.state = ConnFailed
				entry.lastErr = err
			} else {
				entry.state = ConnReady
				entry.lastErr = nil
			}
			entry.waiters.Broadcast()
			entry.mu.Unlock()

			if err != nil {
				outcome := "failure"
				if ctx.Err() != nil {
					outcome = "timeout"
				}
				e := events.Warn("mcpclient", "connect_attempt", err.Error())
				e.Category = "backend"
				e.Resource = "mcp_server:" + name
				e.Outcome = outcome
				p.emitEvent(ctx, e)
				return nil, err
			}
			return entry, nil

		default:
			entry.mu.Unlock()
			return nil, fmt.Errorf("unexpected state %d for server %q", entry.state, name)
		}
	}
}

// getEntry returns or creates a serverEntry for the given server name.
func (p *Pool) getEntry(name string) *serverEntry {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry, ok := p.servers[name]
	if !ok {
		entry = newServerEntry()
		p.servers[name] = entry
	}
	return entry
}

// connect runs the 5-step connection sequence. Caller must NOT hold entry.mu.
// serverName is used to route tools/list_changed notifications to the handler.
func (p *Pool) connect(ctx context.Context, entry *serverEntry, serverName string, cfg config.MCPServerEntry) error {
	// 1. Close previous client if any (prevents subprocess leaks)
	entry.mu.Lock()
	if entry.client != nil {
		entry.client.Close()
		entry.client = nil
	}
	entry.mu.Unlock()

	// 2. Create transport
	var t transport.Interface
	var err error
	if p.newTransportFn != nil {
		t, err = p.newTransportFn(cfg, p.backend)
	} else {
		t, err = p.newTransportWithAuth(cfg, serverName)
	}
	if err != nil {
		return fmt.Errorf("create transport: %w", err)
	}

	// 3. Create client + Start
	c := client.NewClient(t)
	if err := c.Start(ctx); err != nil {
		return fmt.Errorf("start client: %w", err)
	}

	// 4. Initialize (protocol negotiation)
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "fracta", Version: "0.1.0"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		stderrSnippet := captureStderr(c)
		closeErr := c.Close()
		return enrichError("initialize", serverName, err, closeErr, stderrSnippet)
	}

	// 5. Register tools/list_changed notification handler.
	// When a backend server signals its tool list has changed, fire the
	// pool's toolsChangedHandler so the reconciler can do a targeted refresh.
	c.OnNotification(func(n mcp.JSONRPCNotification) {
		if n.Method == mcp.MethodNotificationToolsListChanged {
			p.mu.Lock()
			handler := p.toolsChangedHandler
			p.mu.Unlock()
			if handler != nil {
				handler(serverName)
			}
			p.logger.Info("tools/list_changed notification received", "server", serverName)
			e := events.Info("mcpclient", "tool_refresh")
			e.Category = "backend"
			e.Resource = "mcp_server:" + serverName
			e.Outcome = "success"
			p.emitEvent(context.Background(), e)
		}
	})

	// 6. ListTools (cached for connection lifetime).
	// Reconnection after failure re-runs ListTools.
	tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		stderrSnippet := captureStderr(c)
		closeErr := c.Close()
		return enrichError("list tools", serverName, err, closeErr, stderrSnippet)
	}

	entry.mu.Lock()
	entry.client = c
	entry.tools = indexTools(tools.Tools)
	entry.connectedAt = time.Now()
	entry.mu.Unlock()

	p.logger.Info("MCP client connected", "server", serverName, "tools", len(tools.Tools))
	return nil
}

// captureStderr reads up to 1KB of stderr from the subprocess before Close()
// shuts down the pipe. Returns empty string if stderr is unavailable.
func captureStderr(c *client.Client) string {
	stderr, ok := client.GetStderr(c)
	if !ok || stderr == nil {
		return ""
	}
	buf := make([]byte, 1024)
	n, _ := stderr.Read(buf)
	if n == 0 {
		return ""
	}
	return strings.TrimSpace(string(buf[:n]))
}

// enrichError builds a diagnostic error from subprocess failure information.
// stderrSnippet should be captured BEFORE Close() since Close() shuts the pipe.
// closeErr comes FROM Close() (which calls cmd.Wait() → exec.ExitError).
func enrichError(stage, serverName string, originalErr error, closeErr error, stderrSnippet string) error {
	var exitErr *exec.ExitError
	exitInfo := ""
	if errors.As(closeErr, &exitErr) {
		exitInfo = fmt.Sprintf(" (exit code %d)", exitErr.ExitCode())
	}

	stderrInfo := ""
	if stderrSnippet != "" {
		stderrInfo = fmt.Sprintf(" (stderr: %s)", stderrSnippet)
	}

	return fmt.Errorf("%s %q: %w%s%s", stage, serverName, originalErr, exitInfo, stderrInfo)
}

// newTransport creates the transport specified by the MCP server entry.
// This is a package-level function that delegates to Pool's method when auth is needed.
func newTransport(entry config.MCPServerEntry, _ string) (transport.Interface, error) {
	if entry.Remote != nil && entry.Remote.URL != "" {
		return newRemoteTransportBasic(*entry.Remote, "remote")
	}
	if entry.Local.Command != "" {
		return transport.NewStdio(entry.Local.Command, entry.Local.EnvSlice(), entry.Local.Args...), nil
	}
	if entry.Kubernetes.URL != "" {
		return newRemoteTransportBasic(entry.Kubernetes, "kubernetes")
	}
	return nil, fmt.Errorf("no MCP transport configured (set remote.url or local.command)")
}

// newTransportWithAuth creates transport with auth support via the Pool's credential store.
func (p *Pool) newTransportWithAuth(entry config.MCPServerEntry, serverName string) (transport.Interface, error) {
	if entry.Remote != nil && entry.Remote.URL != "" {
		return p.newRemoteTransport(*entry.Remote, serverName, "remote")
	}
	if entry.Local.Command != "" {
		return transport.NewStdio(entry.Local.Command, entry.Local.EnvSlice(), entry.Local.Args...), nil
	}
	if entry.Kubernetes.URL != "" {
		return p.newRemoteTransport(entry.Kubernetes, serverName, "kubernetes")
	}
	return nil, fmt.Errorf("no MCP transport configured (set remote.url or local.command)")
}

// newRemoteTransportBasic creates a transport with only header-based auth (no auth block).
func newRemoteTransportBasic(remote config.MCPServerRemote, label string) (transport.Interface, error) {
	t := remote.Transport
	if t == "" || t == "streamable_http" || t == "streamable-http" {
		var opts []transport.StreamableHTTPCOption
		if len(remote.Headers) > 0 {
			opts = append(opts, transport.WithHTTPHeaders(remote.Headers))
		}
		return transport.NewStreamableHTTP(remote.URL, opts...)
	}
	if t == "sse" {
		var opts []transport.ClientOption
		if len(remote.Headers) > 0 {
			opts = append(opts, transport.WithHeaders(remote.Headers))
		}
		return transport.NewSSE(remote.URL, opts...)
	}
	return nil, fmt.Errorf("unknown %s transport: %q (must be streamable_http or sse)", label, t)
}

// newRemoteTransport creates a transport with full auth support.
func (p *Pool) newRemoteTransport(remote config.MCPServerRemote, serverName, label string) (transport.Interface, error) {
	if remote.Auth == nil || remote.Auth.Type == "" || remote.Auth.Type == "none" {
		return newRemoteTransportBasic(remote, label)
	}

	headers := make(map[string]string)
	for k, v := range remote.Headers {
		headers[k] = v
	}

	switch remote.Auth.Type {
	case "bearer":
		tok, err := secrets.Resolve(remote.Auth.Token)
		if err != nil {
			return nil, fmt.Errorf("%s auth: resolve bearer token: %w", serverName, err)
		}
		headers["Authorization"] = "Bearer " + tok

	case "header":
		val, err := secrets.Resolve(remote.Auth.HeaderValue)
		if err != nil {
			return nil, fmt.Errorf("%s auth: resolve header value: %w", serverName, err)
		}
		for k := range remote.Headers {
			if strings.EqualFold(k, remote.Auth.HeaderName) {
				return nil, fmt.Errorf("%s auth: header %q collides with existing header in remote.headers", serverName, remote.Auth.HeaderName)
			}
		}
		headers[remote.Auth.HeaderName] = val

	case "basic":
		user, err := secrets.Resolve(remote.Auth.Username)
		if err != nil {
			return nil, fmt.Errorf("%s auth: resolve username: %w", serverName, err)
		}
		pass, err := secrets.Resolve(remote.Auth.Password)
		if err != nil {
			return nil, fmt.Errorf("%s auth: resolve password: %w", serverName, err)
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		headers["Authorization"] = "Basic " + encoded

	case "oauth":
		return p.newOAuthTransport(remote, serverName)

	default:
		return nil, fmt.Errorf("%s: unknown auth type %q", serverName, remote.Auth.Type)
	}

	return newRemoteTransportWithHeaders(remote, headers, label)
}

func newRemoteTransportWithHeaders(remote config.MCPServerRemote, headers map[string]string, label string) (transport.Interface, error) {
	t := remote.Transport
	if t == "" || t == "streamable_http" || t == "streamable-http" {
		var opts []transport.StreamableHTTPCOption
		if len(headers) > 0 {
			opts = append(opts, transport.WithHTTPHeaders(headers))
		}
		return transport.NewStreamableHTTP(remote.URL, opts...)
	}
	if t == "sse" {
		var opts []transport.ClientOption
		if len(headers) > 0 {
			opts = append(opts, transport.WithHeaders(headers))
		}
		return transport.NewSSE(remote.URL, opts...)
	}
	return nil, fmt.Errorf("unknown %s transport: %q (must be streamable_http or sse)", label, t)
}

// newOAuthTransport creates a transport with OAuth configuration.
func (p *Pool) newOAuthTransport(remote config.MCPServerRemote, serverName string) (transport.Interface, error) {
	auth := remote.Auth

	// Resolve credentials upfront (used by both flows)
	var clientID, clientSecret string
	if auth.ClientID != nil {
		id, err := secrets.Resolve(auth.ClientID)
		if err != nil {
			return nil, fmt.Errorf("%s oauth: resolve client_id: %w", serverName, err)
		}
		clientID = id
	}
	if auth.ClientSecret != nil {
		sec, err := secrets.Resolve(auth.ClientSecret)
		if err != nil {
			return nil, fmt.Errorf("%s oauth: resolve client_secret: %w", serverName, err)
		}
		clientSecret = sec
	}

	// Client credentials grant: fetch token and use as bearer (no browser, no PKCE)
	if auth.GrantType == "client_credentials" {
		return p.newClientCredentialsTransport(remote, serverName, clientID, clientSecret)
	}

	oauthCfg := transport.OAuthConfig{
		RedirectURI:           auth.EffectiveRedirectURI(),
		Scopes:                auth.Scopes,
		PKCEEnabled:           auth.EffectivePKCE(),
		AuthServerMetadataURL: auth.MetadataURL,
		ClientID:              clientID,
		ClientSecret:          clientSecret,
	}

	// Identity resolution: client_registration_file → credential store → dynamic registration
	if oauthCfg.ClientID == "" && auth.ClientRegistrationFile != "" {
		reg, err := oauthpkg.LoadClientRegistrationFile(auth.ClientRegistrationFile)
		if err != nil {
			return nil, fmt.Errorf("%s oauth: client_registration_file: %w", serverName, err)
		}
		oauthCfg.ClientID = reg.ClientID
		oauthCfg.ClientSecret = reg.ClientSecret
	}
	if oauthCfg.ClientID == "" && p.credStoreFactory != nil {
		store, err := p.credStoreFactory.Build()
		if err == nil {
			reg, err := store.GetClientRegistration(context.Background(), serverName)
			if err == nil {
				oauthCfg.ClientID = reg.ClientID
				oauthCfg.ClientSecret = reg.ClientSecret
			}
		}
	}

	// Set up token store
	tokenStore, err := p.buildTokenStore(remote, serverName)
	if err != nil {
		return nil, err
	}
	oauthCfg.TokenStore = tokenStore

	t := remote.Transport
	if t == "" || t == "streamable_http" || t == "streamable-http" {
		var opts []transport.StreamableHTTPCOption
		if len(remote.Headers) > 0 {
			opts = append(opts, transport.WithHTTPHeaders(remote.Headers))
		}
		opts = append(opts, transport.WithHTTPOAuth(oauthCfg))
		return transport.NewStreamableHTTP(remote.URL, opts...)
	}
	if t == "sse" {
		var opts []transport.ClientOption
		if len(remote.Headers) > 0 {
			opts = append(opts, transport.WithHeaders(remote.Headers))
		}
		opts = append(opts, transport.WithOAuth(oauthCfg))
		return transport.NewSSE(remote.URL, opts...)
	}
	return nil, fmt.Errorf("unknown %s transport: %q (must be streamable_http or sse)", serverName, t)
}

// newClientCredentialsTransport fetches a token using client credentials grant
// and creates a transport with the bearer token in headers.
func (p *Pool) newClientCredentialsTransport(remote config.MCPServerRemote, serverName, clientID, clientSecret string) (transport.Interface, error) {
	auth := remote.Auth
	tok, err := oauthpkg.FetchClientCredentialsToken(context.Background(), oauthpkg.ClientCredentialsConfig{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       auth.Scopes,
		MetadataURL:  auth.MetadataURL,
		ServerURL:    remote.URL,
	})
	if err != nil {
		return nil, fmt.Errorf("%s oauth client_credentials: %w", serverName, err)
	}

	headers := make(map[string]string)
	for k, v := range remote.Headers {
		headers[k] = v
	}
	headers["Authorization"] = "Bearer " + tok.AccessToken

	p.logger.Info("client_credentials token obtained", "server", serverName, "expires_at", tok.ExpiresAt)
	return newRemoteTransportWithHeaders(remote, headers, serverName)
}

// buildTokenStore returns a token store for the given server, preferring
// pre-authorized tokens over the credential store. Returns an error if an
// explicitly configured source (access_token, token_file) is unresolvable.
func (p *Pool) buildTokenStore(remote config.MCPServerRemote, serverName string) (transport.TokenStore, error) {
	auth := remote.Auth

	// Pre-authorized inline access_token — hard error if configured but unresolvable
	if auth.AccessToken != nil {
		tok, err := secrets.Resolve(auth.AccessToken)
		if err != nil {
			return nil, fmt.Errorf("%s oauth: resolve access_token: %w", serverName, err)
		}
		store := transport.NewMemoryTokenStore()
		token := &transport.Token{AccessToken: tok, TokenType: "Bearer"}
		if auth.RefreshToken != nil {
			rt, err := secrets.Resolve(auth.RefreshToken)
			if err != nil {
				return nil, fmt.Errorf("%s oauth: resolve refresh_token: %w", serverName, err)
			}
			token.RefreshToken = rt
		}
		store.SaveToken(context.Background(), token)
		return store, nil
	}

	// Pre-authorized token file — hard error if configured but unreadable
	if auth.TokenFile != "" {
		tok, err := oauthpkg.LoadTokenFile(auth.TokenFile)
		if err != nil {
			return nil, fmt.Errorf("%s oauth: token_file: %w", serverName, err)
		}
		store := transport.NewMemoryTokenStore()
		store.SaveToken(context.Background(), tok)
		return store, nil
	}

	// Credential store (keyring)
	if p.credStoreFactory != nil {
		credStore, err := p.credStoreFactory.Build()
		if err == nil {
			return &perServerTokenStoreAdapter{store: credStore, server: serverName}, nil
		}
		p.logger.Warn("failed to build credential store", "server", serverName, "error", err)
	}

	return transport.NewMemoryTokenStore(), nil
}

// perServerTokenStoreAdapter adapts OAuthCredentialStore to transport.TokenStore.
type perServerTokenStoreAdapter struct {
	store  OAuthCredentialStore
	server string
}

func (a *perServerTokenStoreAdapter) GetToken(ctx context.Context) (*transport.Token, error) {
	tok, err := a.store.GetToken(ctx, a.server)
	if err != nil {
		return nil, transport.ErrNoToken
	}
	return tok, nil
}

func (a *perServerTokenStoreAdapter) SaveToken(ctx context.Context, token *transport.Token) error {
	return a.store.SaveToken(ctx, a.server, token)
}

// normalizeResult extracts one JSON-bearing text block from CallToolResult.
func normalizeResult(result *mcp.CallToolResult) (*ToolResult, error) {
	if result.IsError {
		var msg string
		for _, c := range result.Content {
			if tc, ok := c.(mcp.TextContent); ok {
				msg = tc.Text
				break
			}
		}
		return &ToolResult{Text: msg, IsError: true}, nil
	}

	// Find the first TextContent that parses as valid JSON.
	for _, c := range result.Content {
		tc, ok := c.(mcp.TextContent)
		if !ok {
			fractalog.Component("mcpclient").Debug("skipping non-text MCP content", "type", fmt.Sprintf("%T", c))
			continue
		}
		if json.Valid([]byte(tc.Text)) {
			return &ToolResult{Text: tc.Text}, nil
		}
	}

	// No valid JSON found — return the first text block with an error hint
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return nil, fmt.Errorf("MCP tool returned non-JSON text: %.200s", tc.Text)
		}
	}
	return nil, fmt.Errorf("MCP tool returned no text content")
}

// indexTools builds a map from tool name to Tool for fast lookup.
func indexTools(tools []mcp.Tool) map[string]mcp.Tool {
	m := make(map[string]mcp.Tool, len(tools))
	for _, t := range tools {
		m[t.Name] = t
	}
	return m
}
