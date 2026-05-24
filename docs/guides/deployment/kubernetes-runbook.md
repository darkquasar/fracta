---
title: Local K8s Mode
description: Operations reference — run fracta against kind (recommended), Docker Desktop, minikube, or k3d for development and testing.
---

Run fracta with a local Kubernetes cluster for development and testing. **kind is the recommended default** — it's reproducible, cluster-agnostic, and matches what CI uses. Docker Desktop, minikube, and k3d also work; the Makefile's image-loading helpers handle each. Agents spawn as K8s Jobs, the gateway proxies MCP tools, and state lives in Postgres.

## Prerequisites

- A local Kubernetes cluster. **kind is the recommended default** (`kind create cluster --name fracta`). Docker Desktop Kubernetes, minikube, and k3d are also supported for image loading.
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
  └─ fracta serve         (reads ./fracta.yaml from your project root)
       └─ thin client (HTTP) ──▶ fracta-controlplane Service :9090 in-cluster
            (transport: kubectl port-forward / LoadBalancer / Ingress, depending on cluster)

K8s Cluster (fracta namespace)
  ├─ fracta-controlplane Deployment  ← lifecycle authority, workers, K8s agent spawner
  ├─ fracta-gateway      Deployment  ← HTTP MCP endpoint for agent pods
  ├─ postgres            StatefulSet ← shared state (agents, events, missions)
  ├─ falkordb            StatefulSet ← knowledge graph
  ├─ fracta-agent-*      Jobs        ← ephemeral batch agent pods (Claude/Codex/OpenCode)
  └─ fracta-stream-*     Pods        ← persistent stream agent pods (Codex/OpenCode)
```

Agent pods connect to the gateway via HTTP MCP. The gateway proxies fracta's own agent/graph/strategy tools, plus any MCP backend services you add to `deployment/k8s/manifests/`.

## Quick Start

### 1. Initialize fracta in your project

From the root of any git repository:

```bash
fracta init --scaffold k8s
```

This drops `fracta.yaml` and `deployment/k8s/manifests/` (namespace, RBAC, postgres, falkordb, controlplane, gateway, auth-helpers ConfigMap stub, agent job template).

### 2. Apply the manifests

```bash
kubectl apply -f deployment/k8s/manifests/
```

### 3. Verify pods are running

```bash
kubectl get pods -n fracta
```

All pods should show `1/1 Running`:
- `postgres-0`
- `falkordb-0`
- `fracta-controlplane-*`
- `fracta-gateway-*`

### 4. Reach the control plane Service from your host

The control plane is the `fracta-controlplane` Service on port 9090 inside the cluster. For local dev clusters:

```bash
kubectl port-forward -n fracta svc/fracta-controlplane 9090:9090
```

Runs in the foreground. Opens `localhost:9090` → fracta-controlplane Service.

Port-forward is the canonical path on kind: kind's LoadBalancer Services stay `<pending>` because there's no cloud provisioner. For Docker Desktop, a `LoadBalancer` Service may publish directly without port-forward; for non-dev clusters, expose via an Ingress. In all cases, update `control_plane_api.url` in `fracta.yaml` to match. The rest of the flow is identical.

### 5. Connect via MCP (golden path)

The scaffolded `fracta.yaml` is the host-side thin-client config. Configure your AI CLI to run `fracta serve` from your project root:

```json
// .mcp.json
{ "mcpServers": { "fracta": { "command": "fracta", "args": ["serve"] } } }
```

Once the host can reach the control plane Service:

```bash
# In Claude Code, reconnect MCP:
/mcp
```

Or run directly:
```bash
fracta serve
```

## Configuration

### Two config files

| File | Purpose | Used by |
|------|---------|---------|
| `fracta.yaml` (project root) | Host-side thin-client config pointing at control plane API | `fracta serve` on your machine |
| `deployment/k8s/manifests/fracta-controlplane.yaml` | In-cluster controlplane config (ConfigMap) | Controlplane pod |
| `deployment/k8s/manifests/fracta-gateway.yaml` | In-cluster gateway config (ConfigMap) | Gateway pod |

### Key differences

| Setting | Host-side (`fracta.yaml`) | In-cluster (controlplane / gateway ConfigMaps) |
|---------|---------------------------|-------------------------------|
| Control plane API | `http://localhost:9090` | Pod-local service access |
| State/queue | Not configured in thin client | Postgres in cluster |
| FalkorDB | Not configured in thin client | `falkordb.fracta.svc:6379` |
| Runtime backend | `kubernetes` (with `extra_volumes`) | `kubernetes` |

### MCP backend transports

Each MCP backend in the gateway config needs an explicit transport. For example, if you've added an Elasticsearch MCP container as a Service in the cluster:

```yaml
mcp_servers:
  servers:
    my-elastic:
      remote:
        url: http://my-elastic-mcp.fracta.svc:3000/mcp
        transport: streamable-http
    my-other-backend:
      remote:
        url: http://my-other.fracta.svc:3000/sse
        transport: sse
```

Supported transports: `streamable-http` (default if omitted), `sse`.

### Secrets

You'll need at minimum a postgres secret. Create it manually:

```bash
kubectl create secret generic postgres-secrets -n fracta \
  --from-literal=password="$(openssl rand -base64 24)"
```

For agent auth credentials, populate the `fracta-auth-helpers` ConfigMap from `deployment/auth-helpers/` — see the [auth helpers section of the K8s configuration docs](/configuration/kubernetes#mounting-operator-supplied-auth-helpers).

For MCP backend services you add (Elasticsearch, internal services, etc.), create their secrets the same way and reference them via `secretKeyRef` in the corresponding Deployment manifest.

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

The scaffolded manifests reference `ghcr.io/darkquasar/fracta:latest` (the published fracta image) with `imagePullPolicy: IfNotPresent`. For local clusters that can pull from public registries, no extra setup is needed. For air-gapped clusters or fracta-development workflows where you've built a local image, load the image into your cluster runtime and edit the `image:` and `imagePullPolicy:` fields in `deployment/k8s/manifests/fracta-controlplane.yaml` and `fracta-gateway.yaml` accordingly:

```bash
# Examples — pick the one matching your cluster runtime.
kind load docker-image ghcr.io/darkquasar/fracta:dev --name <cluster-name>
minikube image load ghcr.io/darkquasar/fracta:dev --profile <profile>
k3d image import  ghcr.io/darkquasar/fracta:dev --cluster <cluster-name>
```

Then update `image:` and set `imagePullPolicy: Never` on the relevant Deployments.

After image changes, restart the Deployments:

```bash
kubectl rollout restart deployment/fracta-controlplane -n fracta
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
| `ErrImagePull` on locally-built pods | Image not loaded into the local cluster runtime | Load the image via your cluster's loader (`kind load docker-image`, `minikube image load`, `k3d image import`) and set `imagePullPolicy: Never` on the relevant manifests |
| `ErrImagePull` on a public-image pod | Cluster cannot reach the registry | Check local cluster network and registry access; for air-gapped clusters, mirror the image |
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
kubectl delete -f deployment/k8s/manifests/
kubectl delete namespace fracta
```

Deletes the `fracta` namespace and persistent volumes.
