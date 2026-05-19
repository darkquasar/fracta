---
title: Troubleshooting
description: Common issues across all deployment modes
---

## By deployment mode

### Local process

{/* TODO: control plane won't start, port conflicts, runtime CLI not found, sqlite lock errors */}

### Docker Compose

{/* TODO: container fails to start, postgres connection errors, image pull issues, network mode quirks */}

### Kubernetes

The most complete troubleshooting reference for kubernetes mode lives in the [Kubernetes Runbook](/guides/deployment/kubernetes-runbook). The table below summarises the most common issues.

| Symptom | Likely cause | Fix |
|---|---|---|
| Pod stuck in `Pending` | PVC not bound, node selector mismatch | Check PVC binding and node labels |
| Agent Job exits immediately | Image pull failure or missing config | `kubectl describe job/fracta-agent-<task>` |
| Tool calls return 401 | Credential profile not mounted in pod | Check ConfigMap/Secret references |
| Gateway can't reach FalkorDB | Service DNS or port mismatch | Verify `falkordb.fracta.svc:6379` |

{/* TODO: pull the rest of the local-k8s.md table here */}

## By symptom

### Agent stays in `queued` forever

{/* TODO: admission control, concurrency limits, queue worker not running */}

### Agent fails with "no runtime configured"

{/* TODO: missing default_runtime in fracta.yaml; runtime CLI not on PATH */}

### Gateway tool calls return `unauthorized`

{/* TODO: link to credential pipeline; common OAuth/token issues */}

### `fracta init` fails

{/* TODO: not in a git repo, permission issues, existing .fracta directory */}

## FAQ

{/* TODO: 5-10 questions seeded from real issues */}

### Can I run multiple control planes on one machine?

{/* TODO */}

### How do I move agents between deployment modes?

{/* TODO */}

### What happens to a worktree if I git push from outside fracta?

{/* TODO */}
