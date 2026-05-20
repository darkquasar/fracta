---
title: fracta watch
description: Connect to the control plane and stream live events for the given agent.
---

```
Connect to the control plane and stream live events for the given agent.
Events are displayed as they arrive. Use --since to replay from a specific event ID.
Press Ctrl-C to stop.

Usage:
  fracta watch <name> [flags]

Flags:
  -h, --help           help for watch
      --since string   Replay events from this event ID

Global Flags:
      --client-mode string   control-plane client mode: 'auto' (default), 'local' (host-side orchestrator), 'remote' (in-cluster CP API)
      --config string        path to fracta.yaml config file (default: <root>/fracta.yaml)
      --root string          project root directory (default: current directory)
```
