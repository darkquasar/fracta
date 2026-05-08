# Local Kubernetes Deployment

This deployment mode runs fracta in a local Kubernetes cluster such as Docker Desktop Kubernetes, kind, minikube, or k3d.

The `manifests/` directory contains Kubernetes manifests for the full local cluster stack plus legacy spike manifests for manually validating two early assumptions:

1. **Claude CLI runs headless in a container** (spike-job)
2. **Two pods can share a git repo via PVC** (git-test-pods)

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

## Test 1: Claude CLI Spike Job

This test runs a K8s Job that invokes Claude CLI in headless batch mode via AWS Bedrock.

**Auth:** Uses `bedrock-auth-helper` to get a short-lived bearer token (60s TTL) which is passed as a K8s Secret. The Job also sets `CLAUDE_CODE_USE_BEDROCK=1`, `CLAUDE_CODE_SKIP_BEDROCK_AUTH=1`, and `AWS_REGION=ap-southeast-2`.

```bash
# Create namespace
kubectl apply -f deployment/k8s-local-cluster/manifests/namespace.yaml

# Fetch bearer token and create secret (token has 60s TTL — run job immediately after)
kubectl create secret generic fracta-spike-auth \
  --namespace fracta \
  --from-literal=bearer-token="$(bedrock-auth-helper)" \
  --dry-run=client -o yaml | kubectl apply -f -

# Run the spike job
kubectl apply -f deployment/k8s-local-cluster/manifests/spike-job.yaml

# Wait for completion (timeout 120s)
kubectl wait --for=condition=complete job/spike-claude-ping -n fracta --timeout=120s

# Check the logs
kubectl logs job/spike-claude-ping -n fracta
```

**Expected output:** JSON containing the word "pong".

## Test 2: Git-on-PVC (Shared Repository)

This test verifies that two pods sharing a PVC can operate on different git branches and see each other's commits.

```bash
# Create the PV and PVC
kubectl apply -f deployment/k8s-local-cluster/manifests/pvc.yaml

# Apply the git test manifests (ConfigMap + 3 pods)
kubectl apply -f deployment/k8s-local-cluster/manifests/git-test-pods.yaml

# Wait for the init pod to complete
kubectl wait --for=jsonpath='{.status.phase}'=Succeeded pod/git-init -n fracta --timeout=60s

# Wait for agent pods to complete
kubectl wait --for=jsonpath='{.status.phase}'=Succeeded pod/git-agent-a -n fracta --timeout=120s
kubectl wait --for=jsonpath='{.status.phase}'=Succeeded pod/git-agent-b -n fracta --timeout=120s

# Check the logs
kubectl logs git-agent-a -n fracta
kubectl logs git-agent-b -n fracta
```

**Expected output:** Both agents report "PASS" — each can see the other's branch and commits.

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
