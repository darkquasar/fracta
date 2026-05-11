---
title: fracta config mcp
description: Fetch the MCP server catalog and inject servers into deployment configs.
---

# fracta config mcp

`fracta config mcp` is the entry point for catalog management and per-mode
server wiring. It is part of the broader `fracta config` umbrella for
project-config commands.

```
Manage MCP servers in this fracta project.

Usage:
  fracta config mcp [command]

Available Commands:
  add         Inject an MCP server into the current scaffold mode
  auth        Manage credentials for OAuth-protected MCP servers
  fetch       Populate <root>/mcp-servers/ from a catalog source
  inspect     Show full per-server metadata
  list        List every MCP server in the local catalog
  remove      Reverse 'add' for one server
```

## Command tree

```
fracta config mcp
├── fetch [<source>]               populate <root>/mcp-servers/
├── list
├── inspect <server>
├── add <server>
├── remove <server>
└── auth
    ├── login <server>
    ├── logout <server>
    ├── status [server]            (was: 'fracta mcp auth-status')
    └── export <server>
```

The old `fracta mcp <verb>` form is preserved as a deprecation alias for one
minor release — see [the migration page](../../migration/spec-43-config-mcp.md).

## `fetch`

Populates `<root>/mcp-servers/` from a catalog source. Operators commit the
result.

```
Usage: fracta config mcp fetch [<source>] [flags]

Source positional argument (optional; default: github:darkquasar/fracta@main):
  github:owner/repo@ref                       GitHub repo at tag, branch, or sha
  https://github.com/owner/repo[@ref]         Browser-pasted URL; trailing slash and
                                              .git suffix tolerated; ref defaults to "main"
  git@github.com:owner/repo[.git][@ref]       SSH form pasted from 'git remote -v';
                                              fetched over HTTPS codeload regardless
  https://example.com/cat.tar.gz              Raw HTTPS tarball; expects mcp-servers/ at root
  ./local/path  or  /abs/path                 Local directory; reads mcp-servers/ inside it

Flags:
  --merge                       Merge by id; preserve local-only entries. Default is
                                wholesale replace. --merge does NOT update .fracta-source.
  --filter <expr>               e.g. "status=tested,category=knowledge"
  --source-checksum <sha256>    Verify HTTPS tarball matches expected sha256.
                                Silently ignored for github:, git@, and path sources.
  --yes                         Skip the overwrite confirmation prompt.
```

### Pre-flight summary

```
Fetching catalog from github:darkquasar/fracta@main (≈ 8 KB):
  Local catalog state:    14 entries (<root>/mcp-servers/, last fetched 2026-05-10)
  Remote catalog state:   16 entries (catalog.yaml version: 2)
  Mode:                   replace (use --merge to preserve local-only entries)
  After fetch:            16 entries (2 added, 0 removed, 0 changed)
Proceed? [y/N]
```

Suppressed by `--yes`.

### Last-source memoization

After a successful **plain (non-merge)** `fetch`, the source spec used is
written to `<root>/mcp-servers/.fracta-source`. Subsequent `fetch`
invocations without a positional argument default to that recorded value
rather than the global default. To re-establish the canonical source:

```sh
$ fracta config mcp fetch github:darkquasar/fracta@main
```

`--merge` does NOT update `.fracta-source` — it's a one-shot overlay.

## `list`

Lists every MCP server in the local catalog, grouped by deployment mode.

```
Flags:
  --target-deployment {local|docker-compose|k8s|all}    Filter to one mode column.
                                                        Default: only-enabled-mode if
                                                        exactly one is scaffolded; else "all".
  --remote                                              Read-only diff against the canonical remote.
  --filter <expr>                                       e.g. "status=tested" or "category=knowledge"
  --output {table|json|yaml}                            Default: table
  --no-image-state                                      Skip docker/podman image presence inspection.
```

If `<root>/mcp-servers/` is missing or empty:

```
no catalog found at <root>/mcp-servers/
run 'fracta config mcp fetch' to populate it (default source: github:darkquasar/fracta@main)
```

Default output (`--target-deployment all`):

```
SERVER         MODES                          AUTH        TRANSPORT          IMAGE                                            IMAGE STATE         STATUS
elastic        local✓ compose✓ k8s✓           env_token   streamable-http    docker.elastic.co/mcp/elasticsearch:latest       present (docker)    tested
notion         local✓ compose✗ k8s✗           oauth       stdio (mcp-remote) —                                                n/a                 documented
readwise       local✗ compose✗ k8s✗           bearer      streamable-http    —                                                n/a                 documented
```

### `--remote`

Compares the local catalog against the canonical remote (`github:darkquasar/fracta@main`).
Read-only — never writes to the project tree. Output uses `catalog.yaml`'s
`version:` for the REMOTE column:

```
SERVER         LOCAL                          REMOTE   DELTA          AUTH        DESCRIPTION
elastic        configured (k8s+compose)       v2       up-to-date     env_token   Elasticsearch-backed search/enrichment
notion         not configured                 v2       available      oauth       Notion knowledge graph
```

Offline network: degrades to `"remote unavailable: <error>"` on stderr and
exits 0 with the local-only view.

## `inspect <server>`

Full per-server metadata.

```
$ fracta config mcp inspect elastic
Server: elastic (Elasticsearch MCP)
Category: security    Status: tested    Upstream: vendor (https://www.elastic.co/)

Auth modes: env_token
Required env: ES_URL, ES_API_KEY

Variants:
  local       transport=stdio              command=podman run -i --rm ... docker.elastic.co/mcp/elasticsearch stdio
  container   transport=streamable-http    image=docker.elastic.co/mcp/elasticsearch:latest (image_owner=external)

Per-mode support:
  local-process    supported
  docker-compose   supported
  kubernetes       supported

Container build: not required (public image, owner=external)
Image state (local docker daemon): present (sha256:abc...)

Configured in this project:
  local           yes (fracta.yaml mcp_servers.elastic.local)
  docker-compose  yes (fracta/docker-compose.yml service:elastic-mcp)
  kubernetes      yes (fracta/k8s/manifests/elastic-mcp.yaml)
```

## `add <server>`

Performs the per-mode hand-edits operators do today, deterministically and
idempotently, after explaining what running/building/pulling will be needed.

```
Flags:
  --target-deployment {local|docker-compose|k8s}    Default: only-enabled-mode if exactly
                                                    one is scaffolded; otherwise required.
  --variant <name>                                  Variant key from server.yaml.
  --dry-run                                         Print pre-flight summary; no writes.
  --force                                           Overwrite existing per-mode entries.
  --yes                                             Skip the pull/build confirmation.
  --pull                                            Eagerly 'docker pull <image>' after scaffolding.
  --build                                           Eagerly 'docker build' (only if Dockerfile is fracta-owned).
```

### Per-mode write contract

| Mode | Files touched |
|---|---|
| `local` | `<root>/fracta.yaml` (`mcp_servers.servers.<id>.local`) |
| `docker-compose` | `<root>/fracta.yaml` (`.remote`), `<root>/fracta/docker-compose.yml` (`services.<id>-mcp`), `<root>/.env.example` |
| `k8s` | `<root>/fracta.yaml` (`.remote`), `<root>/fracta/k8s/manifests/<id>-mcp.yaml` (Deployment+Service), `<root>/fracta/k8s/manifests/<id>-mcp-secret.yaml` (Secret stub — only if `auth.env_required` non-empty) |

All `fracta.yaml` and `docker-compose.yml` mutations are atomic, idempotent,
and comment-preserving. Reads decode to `*yaml.Node`; writes round-trip
through `gopkg.in/yaml.v3`; commits are temp+fsync+rename.

`add` is a strict layer on top of `fracta init --scaffold <mode>`. It
refuses to mutate a project that hasn't scaffolded the chosen mode, with an
explicit error pointing at the prereq command.

### Rollback

For multi-file mutations (compose/k8s), each mutation writes a transient
`.bak` immediately before applying, then removes it on success. On any
failure, the rollback restores from `.bak` and removes newly-written files.
Successful `add` leaves no `.bak` files.

## `remove <server>`

Reverse of `add`.

```
Flags:
  --target-deployment {local|docker-compose|k8s}    Default: only-enabled-mode if unique; else required.
  --keep-config                                     Remove generated manifests/compose entries;
                                                    leave fracta.yaml block intact.
  --yes                                             Skip confirmation prompt.
```

`add` then `remove --yes` returns the project byte-identical to pre-`add`
state (modulo filesystem timestamps).

## `auth`

Identical semantics to the old `fracta mcp` group; only the path moved and
`auth-status` was renamed to `status`. See [the auth reference page](./auth.md)
for full credential-store and OAuth-flow details.

```
fracta config mcp auth login <server>      Run the OAuth authorization-code flow
fracta config mcp auth logout <server>     Remove stored token + client registration
fracta config mcp auth status [server]     Show validity and expiry of stored tokens
fracta config mcp auth export <server>     Render credentials (env / k8s-secret / files)
```

## Authoring contract

The catalog at `<root>/mcp-servers/` is **operator-owned, git-tracked
configuration** — first-class checked-in config, not a runtime cache.
Operators commit it and review changes in PRs.

### Trust model

| Source                                    | Trust boundary                                                            |
|---|---|
| Default (`github:darkquasar/fracta@main`) | `darkquasar/fracta` GitHub repo. Use `--source-checksum` for tarball pinning. |
| Custom github source                      | The org/user that owns the repo.                                          |
| HTTPS tarball                             | The host serving it. `--source-checksum` recommended.                     |
| Local directory                           | The operator.                                                             |

In every case `add` is interactive and surfaces image refs in the pre-flight
summary — operators audit before writing.

### `catalog.yaml` `version:`

The `version:` field at the top of `catalog.yaml` is the catalog's own
version, independent of fracta releases. Maintainers bump it on
additions/removals/promotions. `list --remote` surfaces it in the REMOTE
column. `fracta config mcp` reads but does not write this field.

### Org-private catalogs

First-class supported workflow. Mirror the canonical schema in any GitHub
repo, HTTPS tarball, or local directory. See the
[org-private walkthrough](../../migration/spec-43-config-mcp.md#org-private-catalog-walkthrough)
on the migration page.
