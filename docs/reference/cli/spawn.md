---
title: fracta spawn
description: Spawn a new agent with a dedicated worktree
---

```
Spawn a new agent with a dedicated worktree

Usage:
  fracta spawn [flags]

Flags:
      --base string       base branch (default: from config)
      --contract string   contract text or path to contract file (optional)
      --dry-run           resolve the full spawn chain without creating an agent
      --format string     output format for dry-run: yaml or json (default "yaml")
  -h, --help              help for spawn
      --mode string       agent mode: batch (default) or stream (MCP-only)
      --model string      model to use (overrides config and tier)
      --runtime string    runtime implementation (default: registry default)
      --task string       task name (required for spawn, optional for dry-run)
      --tier string       model tier: heavy, medium, or light (mapped via config model_tiers)

Global Flags:
      --client-mode string   control-plane client mode: 'auto' (default), 'local' (host-side orchestrator), 'remote' (in-cluster CP API)
      --config string        path to fracta.yaml config file (default: <root>/fracta.yaml)
      --root string          project root directory (default: current directory)
```
