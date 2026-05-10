---
title: Kubernetes Quickstart
description: Run fracta against a local kubernetes cluster
---

# Quickstart: Kubernetes Mode

Fracta deploys to a Kubernetes cluster. The control plane and gateway run as Deployments, agents spawn as K8s Jobs, and the control plane is exposed to the host as a Kubernetes Service. On the host, the thin client connects to that Service — the **golden path** is to run `fracta serve` as an MCP server in your AI CLI's config so your CLI talks MCP to it and it talks HTTP to the in-cluster control plane. The same thin client is also usable from the command line (`fracta spawn`, `fracta list`, …) when you want operator-style access without going through an AI CLI.

How the host reaches the in-cluster Service depends on the cluster: `kubectl port-forward` for a quick dev loop, a `LoadBalancer` service on Docker Desktop, an Ingress for a real cluster. None of those choices change the architecture — they're just transports.

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

## 2. Reach the control plane Service from your host

The control plane is exposed inside the cluster as the `fracta-controlplane` Service on `:9090`. The host needs a route to it. For local dev clusters the simplest option is `kubectl port-forward`; for Docker Desktop a `LoadBalancer` service may already publish it on `localhost:9090`; for real clusters use an Ingress.

For local dev, the bundled helper script port-forwards in the foreground:

```bash
scripts/k8s-port-forward.sh
```

This opens `localhost:9090` → `fracta-controlplane`. Your thin client (next step) talks to that local endpoint.

<hr />

## 3. Wire the thin client into your AI CLI as MCP (golden path)

Symlink the bundled `.mcp.json` so your AI CLI launches `fracta serve` as an MCP server:

```bash
ln -sf deployment/k8s-local-cluster/runtimes/claude/.mcp.json .mcp.json
```

Restart Claude Code (or run `/mcp` to reconnect). From now on your CLI talks MCP to `fracta serve`, and `fracta serve` talks HTTP to the in-cluster control plane via the route from step 2. This is the recommended setup — your AI CLI gains the `fracta_*` tools (spawn, list, peek, say, kill, send, inbox) and uses them like any other MCP tool.

If you'd rather drive things from the command line instead of through an AI CLI, you can skip this step and use `bin/fracta spawn`, `list`, etc. directly (see step 5). Both paths target the same control plane.

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
