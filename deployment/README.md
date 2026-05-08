# Deployment

This directory is the canonical home for fracta deployment configuration.

## Modes

| Directory | Purpose |
|---|---|
| `local-process/` | Single-machine development mode. The control plane daemon, gateway, agents, and local MCP backends run as host processes. |
| `docker-compose/` | Multi-container local stack. The control plane, gateway, Postgres, FalkorDB, and strategy runner run under Docker Compose. |
| `k8s-local-cluster/` | Local Kubernetes stack for Docker Desktop Kubernetes, kind, minikube, or k3d. Agents run as Kubernetes Jobs or stream pods. |
| `mcp-servers/` | Per-MCP-server image and launcher notes used by local-process and in-cluster modes. |

Runtime launch configs live inside each mode under `runtimes/`. For example, Claude local-process mode uses `deployment/local-process/runtimes/claude/.mcp.json`, while Codex local-process mode uses `deployment/local-process/runtimes/codex/config.toml`.

## Common Commands

```bash
# Local process mode
bin/fracta serve --config deployment/local-process/fracta.yaml

# Docker Compose mode
make compose-up
bin/fracta serve --config deployment/docker-compose/client/fracta.yaml

# Local Kubernetes mode
make k8s-setup
bin/fracta serve --config deployment/k8s-local-cluster/client/fracta.yaml
```
