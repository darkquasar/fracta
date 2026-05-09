---
title: Kubernetes Runbook
description: Operations reference for the kubernetes deployment mode
---

# Local K8s Mode

Run  fracta with a local Kubernetes cluster for development and testing. The default Makefile path targets Docker Desktop Kubernetes, but repo-built image loading can also target kind, minikube, or k3d. Agents spawn as K8s Jobs, the gateway proxies MCP tools, and state lives in Postgres.

## Prerequisites

- A local Kubernetes cluster. Docker Desktop Kubernetes is the default path; kind, minikube, and k3d are also supported for image loading.
- `kubectl`, `make`, `op` (1Password CLI) on PATH
- `psql` for event queries (optional)

Verify:
```bash
kubectl cluster-info
kubectl get nodes
```

## Architecture

```
Host (your machine)
  └─ fracta serve --config deployment/k8s-local-cluster/client/fracta.yaml
       └─ thin client → fracta-controlplane (:9090 via port-forward or LoadBalancer)

K8s Cluster (fracta namespace)
  ├─ fracta-controlplane Deployment  ← lifecycle authority, workers, K8s agent spawner
  ├─ fracta-gateway     Deployment  ← HTTP MCP endpoint for agent pods
  ├─ postgres          StatefulSet ← shared state (agents, events, missions)
  ├─ falkordb          StatefulSet ← knowledge graph
  ├─ elastic-mcp       Deployment  ← Elasticsearch MCP backend
  ├─ vendor-mcp        Deployment  ← Vendor/VendorSecurity MCP backend
  ├─ fracta-agent-*      Jobs        ← ephemeral batch agent pods (Claude/Codex/OpenCode)
  └─ fracta-stream-*     Pods        ← persistent stream agent pods (Codex/OpenCode)
```

Agent pods connect to the gateway via HTTP MCP. The gateway proxies tools from elastic-mcp (5 tools) and vendor-mcp (22 tools), plus fracta's own agent/graph/strategy tools.

## Quick Start

### 1. Full setup (first time)

```bash
make k8s-setup
```

This runs: `docker-build` → `docker-load` → `vendor-mcp-build` → `vendor-mcp-load` → `k8s-deploy` → `k8s-deploy-mcp` → `k8s-deploy-gateway` → `k8s-deploy-controlplane` → `k8s-secrets`.

### 2. Verify pods are running

```bash
make k8s-status
```

All pods should show `1/1 Running`:
- `postgres-0`
- `falkordb-0`
- `elastic-mcp-*`
- `vendor-mcp-*`
- `fracta-gateway-*`

### 3. Start port-forwards

```bash
scripts/k8s-port-forward.sh
```

Runs in the foreground. Opens:
- `localhost:9090` → fracta-controlplane
- optional service ports for direct troubleshooting, depending on the script

### 4. Connect via MCP

`.mcp.json` points to `fracta serve --config deployment/k8s-local-cluster/client/fracta.yaml`. With port-forwards running:

```bash
# In Claude Code, reconnect MCP:
/mcp
```

Or run directly:
```bash
bin/fracta serve --config deployment/k8s-local-cluster/client/fracta.yaml
```

## Configuration

### Two config files

| File | Purpose | Used by |
|------|---------|---------|
| `deployment/k8s-local-cluster/client/fracta.yaml` | Host-side thin-client config pointing at control plane API | `fracta serve` on your machine |
| `deployment/k8s-local-cluster/manifests/fracta-gateway.yaml` | In-cluster config (ConfigMap with in-cluster DNS) | Gateway pod |

### Key differences

| Setting | Host-side (`deployment/k8s-local-cluster/client/fracta.yaml`) | In-cluster (gateway ConfigMap) |
|---------|----------------------------|-------------------------------|
| Control plane API | `http://localhost:9090` | Pod-local service access |
| State/queue | Not configured in thin client | Postgres in cluster |
| FalkorDB | Not configured in thin client | `falkordb.fracta.svc:6379` |
| Runtime backend | Not configured in thin client | `kubernetes` |

### MCP backend transports

Each MCP backend in the gateway config needs an explicit transport:

```yaml
mcp_servers:
  servers:
    elastic:
      remote:
        url: http://elastic-mcp.fracta.svc:3000/mcp
        transport: streamable-http    # Elasticsearch MCP uses Streamable HTTP
    vendor:
      remote:
        url: http://vendor-mcp.fracta.svc:3000/sse
        transport: sse                # Generic MCP uses SSE
```

Supported transports: `streamable-http` (default if omitted), `sse`.

### Secrets

Created by `make k8s-secrets` using 1Password:
- `postgres-secrets` — password for postgres
- `elastic-mcp-secrets` — Elasticsearch URL + API key
- `vendor-mcp-secrets` — VendorSecurity console URL + token

Bedrock auth token (short-lived):
```bash
make k8s-refresh-auth
```

## How Agent Pods Work

When you spawn an agent (via `fracta_spawn` or `fracta spawn`),  fracta prepares a per-agent workspace and the K8s runtime runs it:

1. The in-cluster control plane worker resolves the selected runtime (`claude`, `codex`, or `opencode`) and writes runtime-specific workspace files into the configured staging directory.
2. The K8s backend packages those files into a ConfigMap and creates either a batch Job (`fracta-agent-<task>`) or, for stream mode, a persistent Pod (`fracta-stream-<task>`).
3. A `workspace-init` init container copies the ConfigMap files into the runtime workdir, normally `/workspace/agents/<task>`.
4. An optional auth Secret provides the host-seeded bearer token for the agent runtime.
5. The main container runs `entrypoint.sh`, starts the strategy sidecar, and execs the selected runtime command.
6. The runtime reads its workspace config and connects to the  fracta gateway via the agent-scoped HTTP MCP endpoint.
7. The agent discovers fracta, elastic, and vendor tools through the gateway. On completion,  fracta records output and events.

The runtime-specific files currently injected into K8s agent workspaces are:

| Runtime | Workspace files |
|---------|-----------------|
| Claude | `.mcp.json`, `.claude/settings.json`, `.fracta/user-settings.json`, `CLAUDE.md` |
| Codex | `.codex/config.toml`, `AGENTS.md` |
| OpenCode | `opencode.json`, `AGENTS.md` |

### Runtime MCP Config Formats

Claude uses `.mcp.json`:

```json
{
  "mcpServers": {
    "fracta": {
      "type": "http",
      "url": "http://fracta-gateway.fracta.svc:8080/agents/<task>/mcp"
    }
  }
}
```

The `"type": "http"` field is required — Claude CLI uses it to select HTTP MCP transport.

Codex uses `.codex/config.toml`:

```toml
[mcp_servers.fracta]
url = "http://fracta-gateway.fracta.svc:8080/agents/<task>/mcp"
bearer_token_env_var = "FRACTA_GATEWAY_TOKEN"
```

OpenCode uses `opencode.json`:

```json
{
  "mcp": {
    "fracta": {
      "type": "remote",
      "url": "http://fracta-gateway.fracta.svc:8080/agents/<task>/mcp",
      "headers": {
        "Authorization": "Bearer {env:FRACTA_GATEWAY_TOKEN}"
      }
    }
  },
  "permission": {
    "task": "deny"
  }
}
```

The exact permission payload differs by runtime. See `docs/runtime-configuration.md` for the full multi-runtime configuration details.

## Images

Per-MCP image and launcher notes live under `deployment/mcp-servers/`.

| Image | Source | Build command | Cluster handling |
|-------|--------|---------------|------------------|
| `fracta/agent:latest` | `Dockerfile` | `make docker-build` | Local image, loaded with `make docker-load` |
| `fracta/vendor-mcp:latest` | `deployment/mcp-servers/vendor/Dockerfile` | `make vendor-mcp-build` | Local image, loaded with `make vendor-mcp-load` |
| `docker.elastic.co/mcp/elasticsearch:latest` | Public registry, no local Dockerfile | None | Cluster pulls with `imagePullPolicy: IfNotPresent` |

Repo-built local images use `imagePullPolicy: Never` and must be loaded into the local cluster runtime:

```bash
make docker-load        # fracta/agent
make vendor-mcp-load    # fracta/vendor-mcp
```

The Makefile does not prompt with a menu. Pick the loader explicitly when you are not using Docker Desktop:

```bash
K8S_IMAGE_LOADER=kind KIND_CLUSTER=<name> make docker-load vendor-mcp-load
K8S_IMAGE_LOADER=minikube MINIKUBE_PROFILE=<profile> make docker-load vendor-mcp-load
K8S_IMAGE_LOADER=k3d K3D_CLUSTER=<name> make docker-load vendor-mcp-load
```

Supported `K8S_IMAGE_LOADER` values are `docker-desktop`, `kind`, `minikube`, and `k3d`.

After code changes, rebuild + reload + restart:
```bash
make build docker-build docker-load
kubectl rollout restart deployment/fracta-gateway -n fracta
```

## Observability

### Events in Postgres

```bash
kubectl exec -n  fracta postgres-0 -- psql -U  fracta -d  fracta -c \
  "SELECT component, action, outcome, task, detail FROM agent_events ORDER BY timestamp DESC LIMIT 20;"
```

### Kubernetes Events

```bash
kubectl get events -n  fracta --sort-by='.lastTimestamp' | grep fracta
```

### Gateway logs

```bash
kubectl logs deployment/fracta-gateway -n  fracta --tail=20
```

### Agent pod logs (while pod exists)

```bash
# Batch agents
kubectl logs job/fracta-agent-<task> -n fracta

# Stream agents
kubectl logs pod/fracta-stream-<task> -n fracta
```

### Sidecar logs (inside agent pod)

```bash
kubectl exec <pod> -n  fracta -- cat /var/log/fracta-strategy.log
```

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `ErrImagePull` on repo-built pods | Image not loaded into the local cluster runtime | `make docker-load` / `make vendor-mcp-load`, with `K8S_IMAGE_LOADER=...` if not using Docker Desktop |
| `ErrImagePull` on `elastic-mcp` | Cluster cannot pull `docker.elastic.co/mcp/elasticsearch:latest` | Check local cluster network and registry access |
| `no MCP transport configured` | MCP server entry is missing both `remote.url` and `local.command` | Add a `remote` entry to the gateway ConfigMap |
| MCP tools not discovered in agent | Runtime workspace config missing gateway endpoint or auth fields | Check generated `.mcp.json`, `.codex/config.toml`, or `opencode.json` |
| OpenCode MCP tool call is auto-rejected | Concrete OpenCode MCP permission key missing, for example `fracta_list` | Ensure `opencode.json` expands fracta tool permissions |
| `unexpected status code: 404` on MCP backend | Wrong SSE/HTTP endpoint path | Check transport + URL path (`/sse` vs `/mcp`) |
| `timeout waiting for endpoint` | SSE endpoint returns EOF | Backend may need `/sse` path explicitly |
| Agent output parse error | Sidecar stdout contamination | Check `entrypoint.sh` redirects to log file |
| `config_skew` event | Host and worker config differ | Expected if configs diverge; informational |
| Agents table empty after completion | Reaper cleans terminal queued agents | By design; check `missions` table instead |
| `Authorization header is missing` | `CLAUDE_CODE_SIMPLE` in credential profile env blocks `apiKeyHelper` loading | Remove `CLAUDE_CODE_SIMPLE` from credential profile env (enforced by `forbid_env` assertion) |
| `Authentication failed: API Key is valid` | Missing `AWS_REGION` in credential profile env | Add `AWS_REGION: "ap-southeast-2"` to `auth.credentials.profiles.bedrock.env` (enforced by `require_env` assertion) |
| `model identifier is invalid` | Stale/wrong Bedrock model ID | Use `global.anthropic.claude-sonnet-4-6` (not `us.anthropic.*` or old dated IDs) |
| `configmaps is forbidden` (in-cluster mode) | Controlplane pod using `default` service account | Set `serviceAccountName: fracta-agent` on controlplane deployment |
| `bedrock-auth-helper: executable file not found` (in-cluster mode) | `host_fallback` credential source (scope: `host_edge`) runs `bedrock-auth-helper` which doesn't exist in-cluster | In-cluster config should not include `host_fallback` source; the credential planner annotates it as `unavailable`. Pods self-auth via corporate proxy source |

## Teardown

```bash
make k8s-teardown
```

Deletes the `fracta` namespace and persistent volumes.
