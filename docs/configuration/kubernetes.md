---
title: Kubernetes runtime configuration
description: The runtime.kubernetes block in fracta.yaml — agent pods, operator-supplied auth helpers, and customization.
---

This page documents the `runtime.kubernetes` block in `fracta.yaml`. The
quickest way to get a working tree is `fracta init --scaffold k8s`, which
materializes a starter set of manifests and a `deployment/` directory under
your project root. See the [Kubernetes deployment guide](../guides/deployment/kubernetes.md)
for the full setup walkthrough.

## Mounting operator-supplied auth helpers

The agent image is auth-agnostic: it ships an empty `/opt/fracta/auth-helpers/`
directory on PATH and nothing else auth-specific. Operators bring their own
token-fetching scripts (Bedrock STS, gcloud, secret-mounted files, custom
proxies) and mount them into that directory at runtime via a ConfigMap volume.

Resolver `command:` references in the controlplane config are resolved on
PATH inside the container, so you reference helpers by bare name regardless
of where the file lives on the host.

### End-to-end example

1. Edit (or replace) the starter helper at `deployment/auth-helpers/fetch-token-example`
   so it prints a token for your auth provider on stdout. Examples for
   Bedrock STS, gcloud Vertex tokens, mounted Anthropic API keys, and
   custom HTTP proxies are in the file's header comments. Rename the file
   to whatever your `command:` references will use (e.g.
   `fetch-bedrock-token`, `fetch-vertex-token`).

2. Package `deployment/auth-helpers/` into a ConfigMap:

   ```sh
   kubectl create configmap fracta-auth-helpers \
     --from-file=deployment/auth-helpers/ \
     -n fracta \
     --dry-run=client -o yaml | kubectl apply -f -
   ```

3. Reference the ConfigMap from `fracta.yaml` (the scaffold ships this
   block already; shown here for reference):

   ```yaml
   runtime:
     backend: kubernetes
     kubernetes:
       namespace: fracta
       image: ghcr.io/darkquasar/fracta:latest
       # ...existing fields...
       extra_volumes:
         - name: auth-helpers
           configMap:
             name: fracta-auth-helpers
             defaultMode: 0755
       extra_volume_mounts:
         - name: auth-helpers
           mountPath: /opt/fracta/auth-helpers
           readOnly: true
   ```

4. Restart the controlplane after re-applying the ConfigMap so spawned
   agents pick up the new helpers:

   ```sh
   kubectl rollout restart deployment/fracta-controlplane -n fracta
   ```

Every spawned agent pod (Job-backed or stream-pod) now has the helpers on
PATH at `/opt/fracta/auth-helpers/<name>`. **No image rebuild required.**
Workspace-local helpers in `${PWD}/.fracta/auth-helpers/` shadow image-baked
ones for per-agent overrides.

### Adding a new auth provider

Once the ConfigMap workflow is wired up, adding a new provider is a matter
of dropping a script and updating one config block. For example, to add
Vertex AI alongside an existing setup:

```sh
cat > deployment/auth-helpers/fetch-vertex-token <<'EOF'
#!/bin/sh
exec gcloud auth print-access-token \
  --impersonate-service-account="${VERTEX_SA:?VERTEX_SA must be set}"
EOF
chmod +x deployment/auth-helpers/fetch-vertex-token

# Repackage the ConfigMap (same command as before)
kubectl create configmap fracta-auth-helpers \
  --from-file=deployment/auth-helpers/ \
  -n fracta --dry-run=client -o yaml | kubectl apply -f -
```

Then add a profile to the controlplane ConfigMap (in
`deployment/k8s/manifests/fracta-controlplane.yaml`) under
`auth.credentials.profiles`:

```yaml
vertex:
  runtime_auth_resolvers:
    vertex_helper:
      type: command
      command: fetch-vertex-token
      ttl_ms: 60000
  env:
    CLAUDE_CODE_USE_VERTEX: "1"
    ANTHROPIC_VERTEX_PROJECT_ID: "my-project"
  default_binding:
    type: claude_api_key_helper
    runtime_auth_resolver: vertex_helper
```

Point a runtime at the new profile (same ConfigMap):

```yaml
agents:
  agent_runtimes:
    claude:
      auth_profile: vertex
```

Re-apply the manifest and restart the controlplane:

```sh
kubectl apply -f deployment/k8s/manifests/fracta-controlplane.yaml
kubectl rollout restart deployment/fracta-controlplane -n fracta
```

**No image rebuild. No fork.**

### Diagnostic log line

Every agent pod logs the following at startup:

```
fracta: user-settings.json present=true; auth-helpers=[fetch-bedrock-token,fetch-vertex-token]
```

- `present=false` means the controlplane didn't project a credentials
  file — the agent will fail any auth-bearing call.
- `auth-helpers=[none]` means no helpers are visible on PATH; check the
  ConfigMap mount.

## Field reference

`extra_volumes` accepts a list of full `corev1.Volume` objects — anything
you can express in a Kubernetes Pod spec works here: ConfigMap, Secret,
hostPath, emptyDir, projected, CSI, etc.

`extra_volume_mounts` accepts a list of `corev1.VolumeMount` objects. Each
mount's `name` MUST reference one of:

- An entry in `extra_volumes` declared in the same config.
- A built-in volume name the runtime always defines: `workspace`,
  `agent-config`, `auth-secret`.

Mismatches surface as a config-load error naming the offending mount and
listing the valid names. Empty/missing fields produce no diagnostics and no
behavior change.

### Path resolution inside the container

The agent image entrypoint sets:

```sh
export PATH="${PWD}/.fracta/auth-helpers:/opt/fracta/auth-helpers:${PATH}"
```

Workspace-local helpers shadow image-baked helpers of the same name, which
permits per-agent overrides without rebuilding the image. Resolver
`command:` references should use bare names (e.g. `fetch-bedrock-token`),
not absolute paths — the bare name is found via PATH and the precedence
above means workspace-local overrides "just work".

## Application

The runtime appends `extra_volumes` to every pod spec's `volumes` and
`extra_volume_mounts` to the main agent container's `volumeMounts`. The
workspace-init init container does **not** receive any extras — auth
helpers have no init-time use case, and keeping the init container's mount
set minimal preserves a clean, opt-in injection path.

This applies uniformly to:

- `Spawn()` (Job-backed agents — the default lifecycle).
- `SpawnStreamPod()` (long-lived stream pods used by Codex / OpenCode).
