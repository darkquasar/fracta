# Auth helpers (docker-compose)

Drop executable helper scripts in this directory. They are bind-mounted into
every fracta service container at `/opt/fracta/auth-helpers/` (read-only) and
appear on PATH inside the container.

Reference helpers from `deployment/configs/controlplane.yaml` by bare name:

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

2. Add a profile to `deployment/configs/controlplane.yaml`:

   ```yaml
   auth:
     credentials:
       profiles:
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

3. Point a runtime at the new profile:

   ```yaml
   agents:
     agent_runtimes:
       claude:
         auth_profile: vertex
   ```

4. Restart the controlplane:

   ```sh
   docker compose -f deployment/docker-compose.yml restart controlplane
   ```

No image rebuild. No fracta-Go fork.

## Conventions

- **Executable bit:** All files in this directory MUST be `chmod +x`. The
  scaffold walker emits 0755 automatically; helpers added by hand need
  `chmod +x`.
- **Naming:** `fetch-<provider>-<token-type>` is the recommended pattern
  (e.g. `fetch-vertex-token`, `fetch-bedrock-token`, `fetch-anthropic-key`).
  Names become PATH lookups inside the container.
- **No secrets in scripts:** helpers should *fetch* tokens (call STS, invoke
  `gcloud`, read a mounted secret file, etc.), not embed them. The scaffold
  ships a generic `fetch-token-example` template — edit it (or replace it
  with a renamed copy) before use; it exits non-zero with a clear message
  until you do.

## Switching deployment modes

This project is scaffolded as **docker-compose mode**. Switching to local or
kubernetes requires a destructive re-init (it loses operator edits to
`fracta.yaml`/`deployment/`). One mode per project — see the **Switching
modes** section of the fracta deployment overview docs for separate-repo
and separate-worktree patterns.

## See also

- Auth-helper discovery contract: PATH precedence and per-agent overrides
  are documented in the fracta Kubernetes configuration docs (the contract
  is the same in compose mode).
