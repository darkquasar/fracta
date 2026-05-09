---
title: fracta kill
description: Kill an agent, removing its worktree and state
---

# fracta kill

```
Kill an agent, removing its worktree and state

Usage:
  fracta kill <name> [flags]

Flags:
  -h, --help         help for kill
      --keep-files   keep the worktree files after killing the agent

Global Flags:
      --client-mode string   control-plane client mode: 'auto' (default), 'local' (host-side orchestrator), 'remote' (in-cluster CP API)
      --config string        path to fracta.yaml config file (default: <root>/fracta.yaml)
      --root string          project root directory (default: current directory)
```
