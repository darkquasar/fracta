---
title: fracta worker
description: Runs one or more worker loops that pull missions from the configured queue and execute them using the host registry.
---

# fracta worker

```
Runs one or more worker loops that pull missions from the configured queue and execute them using the host registry.

Usage:
  fracta worker [flags]

Flags:
      --config string           Path to fracta.yaml config file (required)
  -h, --help                    help for worker
      --id string               Worker ID prefix (default: hostname)
      --workers int             Number of concurrent workers (default: from config or 2)
      --workspace-base string   Base directory for ephemeral workspaces

Global Flags:
      --client-mode string   control-plane client mode: 'auto' (default), 'local' (host-side orchestrator), 'remote' (in-cluster CP API)
      --root string          project root directory (default: current directory)
```
