---
title: fracta host-mcp
description: Start an MCP server on stdio that exposes lifecycle tools through the ControlPlaneClient abstraction.
---

```
Start an MCP server on stdio that exposes lifecycle tools (spawn, list,
peek, say, kill, logs) through the ControlPlaneClient abstraction.

This is designed for use as an MCP server in host tool configurations
(e.g. Claude Desktop, IDE plugins). It provides the same lifecycle
semantics as the CLI but via MCP tool calls.

Usage:
  fracta host-mcp [flags]

Flags:
  -h, --help   help for host-mcp

Global Flags:
      --client-mode string   control-plane client mode: 'auto' (default), 'local' (host-side orchestrator), 'remote' (in-cluster CP API)
      --config string        path to fracta.yaml config file (default: <root>/fracta.yaml)
      --root string          project root directory (default: current directory)
```
