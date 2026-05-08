# Strategy Execution Security Model

## Defense-in-Depth Architecture

Fracta uses a layered security model for strategy execution. No single layer is sufficient alone; they compose to provide meaningful isolation even if one layer is bypassed.

```
Layer 1: Air-gapped data access
  Strategy has no credentials for external data sources (Elastic, Splunk, S1).
  Data enters only through pre-staged DuckDB tables written by the Go orchestrator.

Layer 2: K8s NetworkPolicy (k8s-local-cluster/hardening/strategy-network-policy.yaml)
  Default-deny egress. Strategy pods can only reach FalkorDB (6379) and DNS (53).
  Blocks data exfiltration and lateral movement even if code achieves network access.

Layer 3: Pod Security Standards (k8s-local-cluster/hardening/strategy-pod-security.yaml)
  Restricted profile: read-only filesystem, non-root, drop ALL capabilities,
  seccomp RuntimeDefault. Prevents supply chain attacks, privilege escalation,
  and dangerous syscall usage.

Layer 4: Resource limits (k8s-local-cluster/hardening/strategy-resource-limits.yaml)
  CPU, memory, ephemeral-storage bounds, plus activeDeadlineSeconds timeout.
  Prevents resource exhaustion from runaway or malicious strategies.

Layer 5: Read-only graph client (internal/graph/readonly.go)
  Strategies get a read-only graph client by default. Write access requires
  the strategy contract to declare graph_write: true. Prevents graph poisoning
  by untrusted or semi-trusted strategies.

Layer 6: Scoped API keys (agent-side, not strategy-side)
  The MCP agent (not the strategy) holds data source credentials. These should
  be read-only and index-scoped to limit blast radius if the agent is compromised.
```

## Trust Tiers

| Tier | Graph Access | Network | Resource Limits | Use Case |
|------|-------------|---------|-----------------|----------|
| Trusted | Read + Write | Scoped egress | Normal (2Gi, 1 CPU) | Human-written seed strategies |
| Semi-trusted | Read-only | Tighter egress | Tighter (1Gi, 500m) | LLM-generated, human-reviewed |
| Untrusted | None | No egress | Strictest (512Mi, 250m) | LLM-generated, unreviewed |

New strategies default to untrusted until promoted.

## Threat Mitigations

| Threat | Mitigation |
|--------|-----------|
| Credential theft | Air-gapped: no data-source credentials in strategy process |
| Data exfiltration | NetworkPolicy: default-deny egress |
| Lateral movement | NetworkPolicy: only FalkorDB and DNS allowed |
| Graph poisoning | Read-only graph client by default |
| Supply chain attack | Read-only root filesystem; no pip install possible |
| Privilege escalation | Drop ALL capabilities; allowPrivilegeEscalation: false |
| Resource exhaustion | K8s resource limits + activeDeadlineSeconds |
| Container escape | seccomp RuntimeDefault; future: gVisor |

## Local K8s Template Status

The Kubernetes hardening manifests live under `deployment/k8s-local-cluster/hardening/`.
They are templates only and are not applied by the current local deployment.
The local K8s gateway currently runs `strategy-runner` as a sidecar inside the
`fracta-gateway` pod, while `NetworkPolicy` and most pod-level controls are most
useful once strategy execution is decoupled into its own pod or per-run pods.

## Requirements

- **CNI**: NetworkPolicy requires Calico, Cilium, or another policy-capable CNI.
  Docker Desktop's default CNI does not enforce NetworkPolicy.
- **Pod Security Admission**: Enable the `PodSecurity` admission controller in
  the cluster and label the namespace with `pod-security.kubernetes.io/enforce: restricted`.

## Future Hardening

- **gVisor/Kata Containers**: User-space kernel for syscall-level isolation.
  Relevant when processing attacker-controlled data or running fully autonomous strategies.
- **Per-execution pods**: Fork a new pod per strategy execution instead of
  reusing a long-lived sidecar, preventing cross-execution state leakage.
- **Graph audit log**: Provenance tracking on all graph writes for detection
  and rollback of poisoning.
