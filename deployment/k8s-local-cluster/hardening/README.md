# Strategy Runner Hardening Templates

This directory contains Kubernetes hardening templates for fracta strategy execution.

These manifests are **not applied by the current local K8s deployment** and are **not enforced by default**. They document the controls fracta should apply when the strategy runner is decoupled from the gateway into its own pod, or when strategy execution moves to per-run pods.

## Current State

The local K8s deployment currently runs `strategy-runner` as a sidecar container inside the `fracta-gateway` pod:

```text
fracta-gateway pod
  - fracta-gateway container
  - strategy-runner container
```

That topology limits what these templates can enforce:

- `NetworkPolicy` applies at the pod level, not the container level. Applying a strategy-only egress policy to the current shared gateway pod would also affect the gateway container.
- Pod-level security settings apply to the whole pod. Some settings can be copied to the `strategy-runner` container, but the template is written for a separate strategy pod.
- Resource limits are already set directly on the current `strategy-runner` sidecar in `deployment/k8s-local-cluster/manifests/fracta-gateway.yaml`.

## Future Target

These templates become actionable when fracta runs strategies in a separate pod:

```text
fracta-gateway pod
  - fracta-gateway container

fracta-strategy pod
  - strategy-runner container
```

or when each strategy execution gets its own short-lived pod:

```text
fracta-strategy-run-<id> pod
  - strategy-runner container
```

At that point these templates can be applied as Kustomize overlays, Helm values, or copied into generated pod specs.

## Files

| File | Purpose |
|---|---|
| `SECURITY.md` | Deeper strategy execution security model and threat mitigations. |
| `strategy-network-policy.yaml` | Default-deny strategy pod egress, with explicit DNS and FalkorDB allowances. |
| `strategy-pod-security.yaml` | Restricted pod/container security context: non-root, read-only root filesystem, no privilege escalation, dropped capabilities, seccomp runtime default. |
| `strategy-resource-limits.yaml` | CPU, memory, ephemeral storage, and wall-clock execution bounds. |

## Enabling Later

Before applying these templates directly:

1. Decouple `strategy-runner` from the `fracta-gateway` pod.
2. Give strategy pods stable labels matching the selectors in these templates.
3. Use a CNI that enforces `NetworkPolicy`, such as Calico or Cilium.
4. Label the namespace for Pod Security Admission if relying on Kubernetes restricted profile enforcement.
5. Decide which strategy trust tiers need graph access, network access, and graph write access.
