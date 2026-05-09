---
title: fracta mcp
description: MCP server management commands
---

# fracta mcp

```
MCP server management commands

Usage:
  fracta mcp [command]

Available Commands:
  auth-status Show authentication status for MCP servers
  export      Export OAuth credentials in various formats
  login       Authenticate with an OAuth-enabled MCP server
  logout      Remove stored credentials for an MCP server

Flags:
  -h, --help   help for mcp

Global Flags:
      --client-mode string   control-plane client mode: 'auto' (default), 'local' (host-side orchestrator), 'remote' (in-cluster CP API)
      --config string        path to fracta.yaml config file (default: <root>/fracta.yaml)
      --root string          project root directory (default: current directory)

Use "fracta mcp [command] --help" for more information about a command.
```
