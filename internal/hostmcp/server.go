// Package hostmcp provides a host-facing MCP server that exposes lifecycle
// tools through the ControlPlaneClient abstraction. This is SEPARATE from
// the gateway (agent-facing) and the admin Server (stdio-based).
//
// The host MCP adapter is designed for use cases where the host is an MCP
// client (e.g. Claude Desktop, another LLM tool-calling surface). It exposes
// the same lifecycle verbs as the CLI (spawn, list, peek, say, kill) but
// through MCP tool calls instead of CLI flags.
package hostmcp

import (
	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/mark3labs/mcp-go/server"
)

// Server is the host-facing MCP server for lifecycle operations.
// It routes all tool calls through a ControlPlaneClient.
type Server struct {
	mcp    *server.MCPServer
	client cpapi.ControlPlaneClient
}

// New creates a host-facing MCP server backed by the given client.
func New(client cpapi.ControlPlaneClient) *Server {
	s := &Server{
		mcp: server.NewMCPServer(
			"fracta-host",
			"0.1.0",
			server.WithToolCapabilities(true),
		),
		client: client,
	}
	s.registerTools()
	return s
}

// MCPServer returns the underlying MCPServer for transport wiring.
func (s *Server) MCPServer() *server.MCPServer {
	return s.mcp
}

// Serve starts the host MCP server on stdio transport.
func (s *Server) Serve() error {
	return server.ServeStdio(s.mcp)
}
