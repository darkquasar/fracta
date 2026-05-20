---
title: fracta serve
description: Start fracta as an MCP server
---

```
Start fracta as an MCP server

Usage:
  fracta serve [flags]

Flags:
      --agent-mode                    run in agent mode (restricted tools for spawned agents)
      --agent-task string             Agent task name (agent-mode only, set by worker)
      --binding string                Path to binding.yaml for data source resolution.
      --control-plane-api-only        run as control-plane API server only (lifecycle HTTP API + workers, no MCP stdio server)
      --gateway-mode                  run as centralized gateway (agent+graph+strategy+proxy tools, no admin)
      --graph-addr string             FalkorDB address (e.g. localhost:6379). Enables graph tools when set.
  -h, --help                          help for serve
      --listen string                 HTTP listen address (e.g. ':8080'); only used with --transport http
      --mission-id int                Mission ID for agent context (agent-mode only, set by worker)
      --objective-id string           Objective ID for agent context (agent-mode only, set by worker)
      --schema-dir string             Path to graph-schema/ directory. Loads schema and seeds graph at startup when set.
      --strategy-dir string           Path to strategy directory. Enables strategy tools when set.
      --strategy-socket-mode string   Strategy runner socket mode: 'external' (connect to sidecar socket) or '' (spawn subprocess)
      --transport string              transport protocol: 'stdio' (default) or 'http'

Global Flags:
      --client-mode string   control-plane client mode: 'auto' (default), 'local' (host-side orchestrator), 'remote' (in-cluster CP API)
      --config string        path to fracta.yaml config file (default: <root>/fracta.yaml)
      --root string          project root directory (default: current directory)
```
