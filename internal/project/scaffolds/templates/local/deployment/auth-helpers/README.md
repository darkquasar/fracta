# Auth helpers (local mode)

Drop executable helper scripts in this directory. In local mode the agent
runs as a host subprocess, so the helpers are invoked directly from the
host's `PATH` when the resolver `command:` field is set in `fracta.yaml`.

For container-based deployment modes (compose, k8s) the helpers in this
directory are also bind-mounted / ConfigMap-mounted into agent containers at
`/opt/fracta/auth-helpers/`, where the image's entrypoint sets:

```
${PWD}/.fracta/auth-helpers : /opt/fracta/auth-helpers : $PATH
```

If you start in local mode and later switch to compose or k8s
(`fracta init --scaffold docker-compose|k8s`), helpers in this directory will
need to either move to that scaffold's `deployment/auth-helpers/` or the
operator can `--source` a shared scaffold tree.

## Conventions

- File names follow `fetch-<provider>-<token-type>` (e.g.
  `fetch-vertex-token`, `fetch-bedrock-token`, `fetch-anthropic-key`).
  Reference them by bare name from `fracta.yaml` resolver `command:` fields —
  PATH does the rest.
- All files in this directory MUST be executable (`chmod +x`). The scaffold
  walker emits 0755 automatically; helpers added by hand need `chmod +x`.
- Keep secrets out of these scripts; they should *fetch* tokens (call STS,
  invoke `gcloud`, read a mounted secret file, etc.), not embed them.

## Switching deployment modes

This project is scaffolded as **local mode**. Switching to docker-compose or
kubernetes requires a destructive re-init (it loses operator edits to
`fracta.yaml`/`deployment/`). One mode per project — see the **Switching
modes** section of the fracta deployment overview docs for separate-repo
and separate-worktree patterns.

## See also

- Auth-helper discovery contract: PATH precedence and per-agent overrides
  are documented in the fracta Kubernetes configuration docs (the contract
  is the same in local mode, even though the agent runs as a host
  subprocess).
