---
title: Kubernetes Quickstart
description: Run fracta against a local kubernetes cluster
---

# Quickstart: Kubernetes Mode

Fracta deploys to a local Kubernetes cluster. The control plane and gateway run as Deployments, agents spawn as K8s Jobs, and the host is a thin client connected via port-forward.

This quickstart covers the golden path. For the complete guide with troubleshooting, images, observability, and teardown, see [local-k8s.md](/guides/deployment/kubernetes-runbook).

<hr />

## Prerequisites

- **Go 1.25+**, **Docker**, **kubectl**, **make**
- A local Kubernetes cluster (Docker Desktop K8s, kind, minikube, or k3d)
- **Optional**: `op` CLI (1Password) for secrets

Verify:

```bash
kubectl cluster-info
kubectl get nodes
```

<hr />

## 1. Build and deploy

```bash
make build            # Go binary → bin/fracta
make k8s-setup        # Build images, load into cluster, deploy all manifests, create secrets
```

This runs: `docker-build` → `docker-load` → `k8s-deploy` → `k8s-deploy-mcp` → `k8s-deploy-gateway` → `k8s-deploy-controlplane` → `k8s-secrets`.

Verify all pods are running:

```bash
make k8s-status
```

<hr />

## 2. Start port-forwards

```bash
scripts/k8s-port-forward.sh
```

This runs in the foreground and opens `localhost:9090` → fracta-controlplane.

<hr />

## 3. Link your runtime config (in another terminal)

```bash
ln -sf deployment/k8s-local-cluster/runtimes/claude/.mcp.json .mcp.json
```

Restart Claude Code or `/mcp` to reconnect.

<hr />

## 4. Credentials

### LLM runtime credentials

Agent pods self-authenticate via the corporate proxy (same as Docker Compose). The `bedrock` auth profile in the controlplane ConfigMap uses `fetch-bedrock-token` inside the pod.

For short-lived Bedrock tokens that need refreshing:

```bash
make k8s-refresh-auth
```

### MCP server API credentials

Created by `make k8s-secrets` (part of `make k8s-setup`). This reads from 1Password and creates K8s Secrets:
- `elastic-mcp-secrets` — Elasticsearch URL + API key
- `vendor-mcp-secrets` — VendorSecurity console URL + token

Without `op`, create the secrets manually:

```bash
kubectl create secret generic elastic-mcp-secrets -n  fracta \
  --from-literal=ES_URL="https://..." \
  --from-literal=ES_API_KEY="..."
```

<hr />

## 5. Spawn your first agent

```bash
bin/fracta spawn \
  --config deployment/k8s-local-cluster/client/fracta.yaml \
  --task hello-k8s \
  --contract "Say hello and list your MCP tools"
```

Or via MCP: `fracta_spawn(task="hello-k8s", contract="Say hello and list your MCP tools")`

Agents run as K8s Jobs (`fracta-agent-<task>`) in the `fracta` namespace.

<hr />

## 6. End-to-end smoke test

To verify the full stack — pods, control plane, port-forwards, agent spawn, postgres event persistence, K8s event sink — run the smoke test:

```bash
make k8s-smoke
```

This invokes `scripts/k8s-smoke-test.sh`, which is the canonical end-to-end check for K8s mode. It spawns a throwaway agent through the control plane API, waits for the K8s Job to complete, and verifies events landed in postgres and as Kubernetes Events. If you're debugging a specific phase (port-forward setup, agent spawn, event persistence), read the script — each step is labelled and can be lifted out for manual repro.

<hr />

## Full reference

The complete K8s guide covers images, agent pod lifecycle, runtime MCP configs, observability, troubleshooting, and teardown:

**[Local K8s Guide](/guides/deployment/kubernetes-runbook)**

For architecture and config reference: [deployment-modes.md](/guides/deployment/overview) (Section 3).
