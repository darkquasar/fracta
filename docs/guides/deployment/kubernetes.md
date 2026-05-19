---
title: Quickstart, Kubernetes Mode
description: Deploy fracta to a Kubernetes cluster — control plane and gateway as Deployments, agents as Jobs.
---

Fracta deploys to a Kubernetes cluster. The control plane and gateway run as Deployments, agents spawn as K8s Jobs, and the control plane is exposed to the host as a Kubernetes Service. On the host, the thin client connects to that Service — the **golden path** is to run `fracta serve` as an MCP server in your AI CLI's config so your CLI talks MCP to it and it talks HTTP to the in-cluster control plane. The same thin client is also usable from the command line (`fracta spawn`, `fracta list`, …) when you want operator-style access without going through an AI CLI.

How the host reaches the in-cluster Service depends on the cluster: `kubectl port-forward` for a quick dev loop, a `LoadBalancer` service on Docker Desktop, an Ingress for a real cluster. None of those choices change the architecture — they're just transports.

This quickstart covers the golden path. For the complete guide with troubleshooting, observability, and teardown, see the [Kubernetes runbook](/guides/deployment/kubernetes-runbook).

<hr />

## Prerequisites

- **fracta CLI** installed and on PATH (`fracta --help` works). See [installation](/getting-started/installation).
- **Docker** + **kubectl**.
- A local Kubernetes cluster (Docker Desktop K8s, kind, minikube, or k3d) with a current kube-context.
- **A git repository** to scaffold into. `fracta init` runs in your own project root.

Verify:

```bash
kubectl cluster-info
kubectl get nodes
```

<hr />

## 1. Initialize fracta in your project

From the root of any git repository:

```bash
fracta init --scaffold k8s
```

You'll see:

```
Fracta initialized successfully.
  scaffold: k8s
  source:   embedded (fracta vX.Y.Z)
  files:    N written, 0 skipped
```

This drops the k8s scaffold:

```
your-project/
├── fracta.yaml                                       # thin-client config + extra_volumes block
├── .fracta/                                          # gitignored runtime state (logs)
└── deployment/
    ├── k8s/
    │   └── manifests/
    │       ├── namespace.yaml
    │       ├── rbac.yaml
    │       ├── pvc.yaml
    │       ├── workspace-pvc.yaml
    │       ├── postgres.yaml
    │       ├── falkordb.yaml
    │       ├── fracta-controlplane.yaml              # ConfigMap + Deployment + Service
    │       ├── fracta-gateway.yaml                   # ConfigMap + Deployment + Service
    │       ├── auth-helpers-configmap.yaml           # empty stub; populated from auth-helpers/
    │       └── agent-job-template.yaml               # reference, not applied directly
    └── auth-helpers/
        ├── README.md
        └── fetch-token-example                       # 0755 generic helper template; edit before use
```

`fracta.yaml` and everything under `deployment/` are yours to edit.

<hr />

## 2. Set up auth helpers

The scaffolded `deployment/auth-helpers/fetch-token-example` is a deliberately non-functional template that fails loudly until you edit it. Open the file — its header comments include reference snippets for AWS Bedrock STS, Vertex AI via gcloud, mounted Anthropic API keys, and custom HTTP token proxies. Pick the one matching your provider.

For example, for AWS Bedrock STS:

```bash
cat > deployment/auth-helpers/fetch-bedrock-token <<'EOF'
#!/bin/sh
exec aws bedrock get-bearer-token \
  --region "${AWS_REGION:-us-east-1}" \
  --query 'token' --output text
EOF
chmod +x deployment/auth-helpers/fetch-bedrock-token
```

Update `deployment/k8s/manifests/fracta-controlplane.yaml` (the in-cluster controlplane ConfigMap) under `auth.credentials.profiles` with a profile pointing at your helper. The default scaffold ships an `example` profile pointing at `fetch-token-example`; replace it. See the [credential pipeline guide](/guides/authentication/credential-pipeline) for the full profile schema.

The scaffolded `fracta-controlplane.yaml` already declares `runtime.kubernetes.extra_volumes` referencing the `fracta-auth-helpers` ConfigMap, so spawned agent pods auto-mount the helpers at `/opt/fracta/auth-helpers/` — no additional config needed.

<hr />

## 3. Apply the manifests

```bash
kubectl apply -f deployment/k8s/manifests/
```

This brings up the namespace, RBAC, persistent volume claims, postgres, falkordb, the (empty) auth-helpers ConfigMap, the controlplane, and the gateway.

Verify all pods are running:

```bash
kubectl get pods -n fracta
```

You should see five running pods: postgres, falkordb, fracta-controlplane, fracta-gateway, and the strategy-runner sidecar.

<hr />

## 4. Populate the auth-helpers ConfigMap

The `auth-helpers-configmap.yaml` you applied in step 3 is intentionally empty. Populate it from your `deployment/auth-helpers/` directory:

```bash
kubectl create configmap fracta-auth-helpers \
  --from-file=deployment/auth-helpers/ \
  -n fracta \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl rollout restart deployment/fracta-controlplane -n fracta
```

Re-run these two commands every time you add or modify a helper script.

<hr />

## 5. Reach the control plane Service from your host

The control plane is exposed inside the cluster as the `fracta-controlplane` Service on `:9090`. The host needs a route to it. For local dev clusters the simplest option is `kubectl port-forward`:

```bash
kubectl port-forward -n fracta svc/fracta-controlplane 9090:9090
```

This opens `localhost:9090` → `fracta-controlplane` (the scaffolded `fracta.yaml` already points at `http://localhost:9090`). For Docker Desktop with a `LoadBalancer` Service, the Service may publish directly on `localhost:9090` without port-forward. For real clusters, expose via an Ingress and update `control_plane_api.url` in `fracta.yaml` to match.

<hr />

## 6. Wire the thin client into your AI CLI (golden path)

The scaffolded `fracta.yaml` is the host-side thin-client config. Your AI CLI runs `fracta serve` from your project root, which reads `./fracta.yaml`.

**Claude Code** (`.mcp.json` at the project root):

```json
{
  "mcpServers": {
    "fracta": {
      "command": "fracta",
      "args": ["serve"]
    }
  }
}
```

**Codex** (`.codex/config.toml`):

```toml
[mcp_servers.fracta]
command = "fracta"
args = ["serve"]
```

Restart Claude Code (or run `/mcp` to reconnect). From now on your CLI talks MCP to `fracta serve`, and `fracta serve` talks HTTP to the in-cluster control plane via the route from step 5.

If you'd rather drive things from the command line instead of through an AI CLI, you can skip this step and use `fracta spawn`, `fracta list`, etc. directly (see step 7). Both paths target the same control plane.

<hr />

## 7. Spawn your first agent

```bash
fracta spawn \
  --task hello-k8s \
  --contract "Say hello and list your MCP tools"
```

Or via MCP: `fracta_spawn(task="hello-k8s", contract="Say hello and list your MCP tools")`.

Agents run as K8s Jobs (`fracta-agent-<task>`) in the `fracta` namespace. Watch them:

```bash
kubectl get jobs,pods -n fracta -l fracta.agent
fracta list
fracta peek --name hello-k8s
```

<hr />

## Full reference

The complete K8s guide covers images, agent pod lifecycle, multi-runtime MCP configs, observability, troubleshooting, and teardown:

**[Kubernetes runbook](/guides/deployment/kubernetes-runbook)**

For architecture and config reference: [deployment overview](/guides/deployment/overview) (Section 3) and [Kubernetes runtime configuration](/configuration/kubernetes).
