# Fracta MCP

## What It Is

`fracta` is the first-party MCP surface provided by this repo. It is not a backend MCP dependency like Elastic or Vendor. It is the gateway/control-plane tool surface that agents and thin clients connect to.

## Local-Process Mode

Local clients launch fracta directly, normally through `.mcp.json` or Codex config:

```text
bin/fracta serve --config <mode config>
```

The binary is built with:

```bash
make build
```

## In-Cluster Mode

The in-cluster gateway and control plane use:

```text
fracta/agent:latest
```

That image is defined by the repo-root `Dockerfile` because it contains the fracta binary, runtime support, and strategy runner dependencies.

Build and load it into your local Kubernetes cluster with:

```bash
make docker-build
make docker-load
```

`make docker-load` defaults to Docker Desktop Kubernetes. For other local clusters, pass `K8S_IMAGE_LOADER=kind`, `K8S_IMAGE_LOADER=minikube`, or `K8S_IMAGE_LOADER=k3d`. See `deployment/mcp-servers/README.md` for the cluster-specific variables.

Relevant manifests:

- `deployment/k8s-local-cluster/manifests/fracta-gateway.yaml`
- `deployment/k8s-local-cluster/manifests/fracta-controlplane.yaml`

Agent pods also use `fracta/agent:latest` unless overridden by runtime configuration.
