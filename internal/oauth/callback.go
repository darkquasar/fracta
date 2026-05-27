package oauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/darkquasar/fracta/internal/fractalog"
)

// CallbackResult holds the result from an OAuth callback.
type CallbackResult struct {
	Code  string
	State string
	Error string
}

// CallbackServer listens for a single OAuth redirect callback.
type CallbackServer struct {
	listener net.Listener
	result   chan CallbackResult
	path     string
	timeout  time.Duration
}

// NewCallbackServer binds to the given address synchronously.
// Returns an error immediately if the port is busy.
func NewCallbackServer(addr, path string, timeout time.Duration) (*CallbackServer, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("bind callback server to %s: %w", addr, err)
	}
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	return &CallbackServer{
		listener: ln,
		result:   make(chan CallbackResult, 1),
		path:     path,
		timeout:  timeout,
	}, nil
}

// Addr returns the listener address (useful when binding to :0).
func (s *CallbackServer) Addr() string {
	return s.listener.Addr().String()
}

// Wait starts serving and blocks until a callback is received, the server
// fails, or the timeout expires.
func (s *CallbackServer) Wait(ctx context.Context) (CallbackResult, error) {
	log := fractalog.Component("oauth")
	mux := http.NewServeMux()
	mux.HandleFunc(s.path, s.handleCallback)
	srv := &http.Server{Handler: mux}

	// Run Serve in a goroutine and surface non-graceful errors via a channel
	// so callers see the real failure instead of a misleading timeout.
	serveErr := make(chan error, 1)
	go func() {
		err := srv.Serve(s.listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	defer func() {
		if err := srv.Shutdown(context.Background()); err != nil {
			log.Warn("callback server shutdown failed", "err", err)
		}
	}()

	select {
	case result := <-s.result:
		return result, nil
	case err := <-serveErr:
		if err != nil {
			return CallbackResult{}, fmt.Errorf("callback server: %w", err)
		}
		// Channel closed without an error — server stopped cleanly without a
		// callback. Treat as timeout (effectively the same outcome).
		return CallbackResult{}, fmt.Errorf("OAuth callback server stopped without callback")
	case <-ctx.Done():
		return CallbackResult{}, fmt.Errorf("OAuth callback timeout after %s", s.timeout)
	}
}

func (s *CallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	result := CallbackResult{
		Code:  q.Get("code"),
		State: q.Get("state"),
		Error: q.Get("error"),
	}

	log := fractalog.Component("oauth")
	if result.Error != "" {
		if _, err := fmt.Fprintf(w, "<html><body><h2>Authentication failed</h2><p>%s</p><p>You can close this window.</p></body></html>",
			result.Error); err != nil {
			log.Warn("write error response failed", "err", err)
		}
	} else {
		if _, err := fmt.Fprint(w, "<html><body><h2>Authentication successful!</h2><p>You can close this window and return to the terminal.</p></body></html>"); err != nil {
			log.Warn("write success response failed", "err", err)
		}
	}

	s.result <- result
}

// ParseRedirectURI extracts address and path from a redirect_uri.
func ParseRedirectURI(redirectURI string) (addr, path string, err error) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return "", "", fmt.Errorf("parse redirect_uri: %w", err)
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "9876"
	}
	p := u.Path
	if p == "" {
		p = "/callback"
	}
	return net.JoinHostPort(host, port), p, nil
}
