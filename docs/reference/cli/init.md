---
title: fracta init
description: Materialize a deployment scaffold (local, docker-compose, or k8s) into the current git repository.
---

Materializes one of three deployment scaffolds (`local`, `docker-compose`,
`k8s`) into the current git repository. The first invocation drops a
complete deployment tree in your project root; re-running with the same
scaffold is idempotent. Switching scaffolds in an existing project is
refused — see [Switching modes](#switching-modes) below.

```
Initialize fracta in the current git repository by materializing a scaffold.

Examples:
  fracta init --scaffold local
  fracta init --scaffold docker-compose
  fracta init --scaffold k8s
  fracta init --scaffold k8s --source github:acme/fracta-scaffolds@v3.2.0
  fracta init --scaffold k8s --source ./local/templates
  fracta init --scaffold k8s --source https://example.com/scaffold-k8s.tar.gz \
    --source-checksum sha256:abc123...
  fracta init --scaffold local --force --yes

The first invocation drops a complete deployment tree into the operator's
repository. Re-running with --force overwrites existing files; without
--force, existing files are preserved.

Usage:
  fracta init [flags]

Flags:
      --scaffold string          Scaffold to lay down: local | docker-compose | k8s
      --source string            Override scaffold source (path, github:owner/repo@ref, or https://...)
      --source-checksum string   Optional sha256:<hex> for https:// sources
      --force                    Overwrite existing files
      --yes                      Skip confirmation prompt for --force
  -h, --help                     help for init
```

## Scaffolds

| `--scaffold` value | Use when | Produces |
|---|---|---|
| `local` | Single-machine development; agents run as host subprocesses | `fracta.yaml` (`runtime.backend: local`), `.fracta/state.db`, `deployment/auth-helpers/` |
| `docker-compose` | Local containerized stack: controlplane, gateway, postgres, falkordb, strategy-runner | `fracta.yaml` (thin client), `deployment/docker-compose.yml`, `deployment/configs/`, `deployment/auth-helpers/` |
| `k8s` | Kubernetes deployment; agents as Jobs or stream pods | `fracta.yaml` (`runtime.backend: kubernetes` with `extra_volumes`), `deployment/k8s/manifests/`, `deployment/auth-helpers/` |

The `local` scaffold is the lightest; docker-compose and k8s scaffold a full
service topology. See the per-mode guides:

- [Local process](../../guides/deployment/local-process.md)
- [Docker Compose](../../guides/deployment/docker-compose.md)
- [Kubernetes](../../guides/deployment/kubernetes.md)

## `--source` resolution

| `--source` value | Resolves to |
|---|---|
| (unset) | The scaffold tree baked into your `fracta` binary (offline-by-default) |
| `./relative/path` or `/abs/path` | Local filesystem; expects `<path>/<kind>/` to exist |
| `github:owner/repo@ref` | Codeload tarball from `https://codeload.github.com/owner/repo/tar.gz/<ref>`; expects `<repo>/<kind>/` |
| `https://...tar.gz` | Raw HTTPS tarball; expects `<kind>/` at archive root |

Tarball downloads are capped at 50 MB. HTTPS sources accept an optional
`--source-checksum sha256:<hex>` for integrity verification — without it,
init prints a warning and proceeds.

GitHub refs that don't look like commit SHAs (anything not matching
`[0-9a-f]{7,}`) emit a reproducibility warning to stderr — pin to commit
SHAs or release tags for CI/CD.

## What gets written

The walker materializes every file in the chosen scaffold tree, **skipping
existing files** by default (so re-running `fracta init` doesn't clobber
operator edits). Files under any `auth-helpers/` directory are always
written with mode `0755`; everything else preserves the source-reported mode
or defaults to `0644`.

`fracta init` also:

- Verifies the directory is a git repo (errors otherwise).
- Checks scaffold-specific dependencies via `prereq.EnsureDepsFor(kind)`:
  `local` needs `git`; `docker-compose` needs `git` + `docker` + the
  `docker compose` plugin; `k8s` needs `git` + `kubectl` (warns if no
  current kube-context, doesn't fail).
- Initializes `.fracta/state.db` (SQLite) for `local` scaffolds only.
  Compose and k8s use postgres-backed state in their deployed services.
- Appends `.fracta/` and `.worktrees/` to `.gitignore` if not already
  present.

## Switching modes

A project that's already been scaffolded as one mode cannot be re-initialized
as another. Spec-42 ships **single-mode-per-project**: the `fracta.yaml`,
`deployment/` tree, and embedded controlplane configs are mode-coupled, so a
mixed scaffold would produce a tree where the manifests, configs, and
top-level config disagree about what's running.

If you try to switch:

```
$ fracta init --scaffold k8s
Error: this project is already scaffolded as local; cannot re-init as k8s
without losing customizations.
```

Resolution paths:

- **Switch destructively**: `rm -rf deployment/ fracta.yaml .fracta/ && fracta init --scaffold k8s`. Loses any operator edits.
- **Re-run idempotently**: `fracta init --scaffold local` against an existing local project skips existing files (`0 written, N skipped`) and refreshes anything missing.
- **Multi-mode requirements**: see the [Switching modes](../../guides/deployment/overview.md#switching-modes) section of the deployment overview for separate-repo and separate-worktree patterns.

## Conflict policy and `--force`

| Default | `--force` |
|---|---|
| Existing files are preserved (`SkipExisting`) | Existing files are overwritten (`Overwrite`) |
| Output reports `N written, M skipped` | Prompts `[y/N]` unless `--yes` is passed |

`--force` is destructive and bypasses the mode-mismatch check above only if
you `rm -rf` the relevant directories first. Force-overwriting an existing
mode (e.g. `--force --scaffold local` on a local project) refreshes the
template files in place — useful if you want to revert local edits.

## Deprecated invocation

```
$ fracta init                   # no --scaffold flag
warning: 'fracta init' without --scaffold is deprecated; defaulting to
--scaffold local. This will be required in the next release.
```

The next major release will require `--scaffold` explicitly. Update scripts
and documentation now.
