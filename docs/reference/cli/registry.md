---
title: fracta registry
description: Manage the MCP server registry
---

```
Manage the MCP server registry

Usage:
  fracta registry [command]

Available Commands:
  add         Register a new MCP server
  list        List registered MCP servers
  remove      Remove a registered MCP server
  status      Show registry status or details for a specific server

Flags:
  -h, --help   help for registry

Global Flags:
      --client-mode string   control-plane client mode: 'auto' (default), 'local' (host-side orchestrator), 'remote' (in-cluster CP API)
      --config string        path to fracta.yaml config file (default: <root>/fracta.yaml)
      --root string          project root directory (default: current directory)

Use "fracta registry [command] --help" for more information about a command.
```
