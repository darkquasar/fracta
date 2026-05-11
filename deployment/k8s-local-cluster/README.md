# Local Kubernetes Deployment

This deployment mode runs fracta in a local Kubernetes cluster such as Docker Desktop Kubernetes, kind, minikube, or k3d.

The `manifests/` directory contains Kubernetes manifests for the full local cluster stack.

## Prerequisites

### Enable Kubernetes Locally

Use Docker Desktop Kubernetes, kind, minikube, or k3d. Docker Desktop is the default Makefile loader.

Verify with:

```bash
kubectl cluster-info
kubectl get nodes
```

### Build the spike image and load into K8s

```bash
make spike-build
```

Local K8s runtimes do not automatically see every image in the host Docker daemon. Locally built images must be loaded into the selected cluster explicitly:

```bash
make spike-load
```

For non-Docker Desktop clusters, pass `K8S_IMAGE_LOADER=kind`, `K8S_IMAGE_LOADER=minikube`, or `K8S_IMAGE_LOADER=k3d`.

The manifest uses `imagePullPolicy: Never` so K8s won't try to pull from a registry.

## Full-Stack Smoke Test

For the current local K8s stack, prefer the Makefile smoke test:

```bash
make k8s-smoke
```

This uses `scripts/k8s-smoke-test.sh` to validate the deployed fracta stack through the control plane API.

## Runtime Launch Configs

Use these as repo-root symlink targets when connecting tools:

```bash
ln -sf deployment/k8s-local-cluster/runtimes/claude/.mcp.json .mcp.json
mkdir -p .codex
ln -sf ../deployment/k8s-local-cluster/runtimes/codex/config.toml .codex/config.toml
```

## Cleanup

Remove all spike resources:

```bash
kubectl delete namespace fracta
kubectl delete pv fracta-spike-pv
```

To also clean up the host path data:

```bash
rm -rf /tmp/fracta-spike
```
