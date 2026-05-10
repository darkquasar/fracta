# Auth helpers (Kubernetes)

Drop executable helper scripts in this directory. They are packaged into the
`fracta-auth-helpers` ConfigMap and mounted into every agent pod (and the
controlplane pod) at `/opt/fracta/auth-helpers/` (read-only, mode 0755). They
appear on PATH inside the container.

Reference helpers from the controlplane config (the `fracta-controlplane-config`
ConfigMap in `deployment/k8s/manifests/fracta-controlplane.yaml`) by bare name:

```yaml
runtime_auth_resolvers:
  example_helper:
    type: command
    command: fetch-token-example   # PATH-resolved via /opt/fracta/auth-helpers/
    ttl_ms: 60000
```

## Workflow for adding a new auth provider (e.g. Vertex)

1. Drop the helper script here:

   ```sh
   cat > deployment/auth-helpers/fetch-vertex-token <<'EOF'
   #!/bin/sh
   gcloud auth print-access-token --impersonate-service-account=...
   EOF
   chmod +x deployment/auth-helpers/fetch-vertex-token
   ```

2. Repackage the ConfigMap:

   ```sh
   kubectl create configmap fracta-auth-helpers \
     --from-file=deployment/auth-helpers/ \
     -n fracta \
     --dry-run=client -o yaml | kubectl apply -f -
   ```

3. Add a profile to `deployment/k8s/manifests/fracta-controlplane.yaml` under
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

4. Point a runtime at the new profile (same ConfigMap):

   ```yaml
   agents:
     agent_runtimes:
       claude:
         auth_profile: vertex
   ```

5. Re-apply the ConfigMap and restart the controlplane:

   ```sh
   kubectl apply -f deployment/k8s/manifests/fracta-controlplane.yaml
   kubectl rollout restart deployment/fracta-controlplane -n fracta
   ```

No image rebuild. No fracta-Go fork.

## Conventions

- **Executable bit:** All files MUST be `chmod +x`. The scaffold walker emits
  0755 automatically; helpers added by hand need `chmod +x`. The ConfigMap
  uses `defaultMode: 0755` to preserve the bit through the volume mount.
- **Naming:** ConfigMap keys must be alphanumeric + `-`, `_`, `.`. Recommended
  pattern: `fetch-<provider>-<token-type>` (e.g. `fetch-vertex-token`,
  `fetch-bedrock-token`, `fetch-anthropic-key`).
- **No secrets in scripts:** helpers should *fetch* tokens (call STS, invoke
  `gcloud`, read a mounted secret file, etc.), not embed them. The scaffold
  ships a generic `fetch-token-example` template — edit it (or replace it
  with a renamed copy) before use; it exits non-zero with a clear message
  until you do.

## Switching deployment modes

This project is scaffolded as **kubernetes mode**. Switching to local or
docker-compose requires a destructive re-init (it loses operator edits to
`fracta.yaml`/`deployment/`). One mode per project — see the **Switching
modes** section of the fracta deployment overview docs for separate-repo
and separate-worktree patterns.

## See also

- Auth-helper discovery contract: PATH precedence and per-agent overrides
  are documented in the fracta Kubernetes configuration docs.
- `extra_volumes` / `extra_volume_mounts` reference: see the same docs page
  for the full corev1 surface and validation rules.
- For the host-side resolver `command:` pipeline, see the credential
  pipeline docs.
