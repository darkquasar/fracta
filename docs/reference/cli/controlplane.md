---
title: fracta controlplane
description: Start, stop, or check status of the local fracta control plane daemon.
---

# fracta controlplane

```
Start, stop, or check status of the local fracta control plane daemon.

Usage:
  fracta controlplane [command]

Available Commands:
  start       Start the control plane daemon
  status      Report control plane daemon status
  stop        Stop the control plane daemon

Flags:
  -h, --help   help for controlplane

Global Flags:
      --client-mode string   control-plane client mode: 'auto' (default), 'local' (host-side orchestrator), 'remote' (in-cluster CP API)
      --config string        path to fracta.yaml config file (default: <root>/fracta.yaml)
      --root string          project root directory (default: current directory)

Use "fracta controlplane [command] --help" for more information about a command.
```
