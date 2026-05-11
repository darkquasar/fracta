---
title: Migrating to `fracta config mcp`
description: Spec-43 moves OAuth/MCP credential management under a new project-config umbrella and adds catalog management.
---

# Migrating to `fracta config mcp`

Spec-43 introduces a new `fracta config` umbrella and reorganizes everything
MCP-server-related underneath it:

- The old top-level `fracta mcp <verb>` is now a deprecation alias for the
  new `fracta config mcp auth <verb>`.
- New verbs land alongside auth on the new path:
  `fracta config mcp {fetch,list,inspect,add,remove}`.
- The MCP server catalog moves from `deployment/mcp-servers/` to
  `<root>/mcp-servers/` as first-class checked-in config.

This page is the operator-facing migration reference: what changed, what to
sed-replace in your scripts, and how the new catalog workflow fits together.

## Why the move

`fracta mcp` originally meant "OAuth credential management" — it has only ever
been able to log in, log out, show status, and export tokens. As the MCP
ecosystem grew, the same command name became a poor fit for the broader
problem: discovering servers, fetching the catalog, scaffolding per-deployment
config, and rolling those changes back. Rather than overloading the top-level
verb, spec-43 introduces `fracta config` as a project-config umbrella so the
related work groups under one place:

- `fracta config mcp auth ...` keeps the OAuth flow exactly as it was; only
  the path changed.
- `fracta config mcp fetch` populates the catalog from a source.
- `fracta config mcp {list,inspect}` browses what's available.
- `fracta config mcp {add,remove}` wires servers into your scaffold.

Future siblings of `mcp` (e.g. `fracta config validate`, `fracta config
show`) will live under the same umbrella.

## Command remapping

The hyphenated `auth-status` is renamed to `status` on the new path. The
deprecation alias preserves the hyphenated form on the old path for one
minor release.

| Old (deprecated alias)         | New (canonical)                             |
|---|---|
| `fracta mcp login <server>`    | `fracta config mcp auth login <server>`     |
| `fracta mcp logout <server>`   | `fracta config mcp auth logout <server>`    |
| `fracta mcp auth-status [s]`   | `fracta config mcp auth status [s]`         |
| `fracta mcp export <server>`   | `fracta config mcp auth export <server>`    |

All flags carry over verbatim — `--device-code`, `--format`,
`--output-dir`, etc. The runner functions are the same; only the Cobra
wiring moved.

## What you'll see at the terminal

The alias is functional but warns on every invocation:

```
$ fracta mcp login notion
warning: 'fracta mcp login' is deprecated; use 'fracta config mcp auth login'. This alias will be removed in a future minor release.
Opening browser to authorize with notion...
...
```

The OAuth flow then runs exactly as before. The deprecation warning goes to
stderr so it doesn't pollute stdout-parsing CI pipelines, but it does name
the new path verbatim — copy-paste the fix into your scripts.

## Deprecation window

One minor release. After that the top-level `fracta mcp` group disappears
entirely. The migration steps below take less than a minute per repo if you
have grep installed.

## CI / script update — one-liner

```sh
# Replace 'fracta mcp <verb>' with the new path. Note the auth-status rename.
sed -i.bak \
  -e 's|fracta mcp auth-status|fracta config mcp auth status|g' \
  -e 's|fracta mcp login|fracta config mcp auth login|g' \
  -e 's|fracta mcp logout|fracta config mcp auth logout|g' \
  -e 's|fracta mcp export|fracta config mcp auth export|g' \
  $(grep -rln 'fracta mcp ' . --include='*.sh' --include='*.yaml' --include='*.yml' --include='*.md')
```

(Order matters: `auth-status` must be substituted before `login`/`logout` to
avoid the `auth-` prefix being mangled.)

## What's new beyond the rename

The other new verbs at `fracta config mcp` are not aliases — they're new
capability that previously did not exist:

```
fracta config mcp fetch [<source>]    populate <root>/mcp-servers/ from a catalog source
fracta config mcp list                show every server in the local catalog
fracta config mcp inspect <server>    full per-server metadata
fracta config mcp add <server>        scaffold server into the current deployment mode
fracta config mcp remove <server>     reverse 'add'
```

The catalog at `<root>/mcp-servers/` is first-class checked-in
config — operators commit it and review changes in PRs. See
[`docs/reference/cli/config-mcp.md`](../reference/cli/config-mcp.md) for the
full surface, or jump to the
[authoring contract](../reference/cli/config-mcp.md#authoring-contract) for
how to publish an org-private or community catalog.

## Authoring contract — quick summary

| Source                                  | Trust boundary                                                  |
|---|---|
| `github:darkquasar/fracta@main` (default) | `darkquasar/fracta` GitHub repo. `--source-checksum` pins a tarball. |
| Custom github source                    | The org/user that owns the repo.                                |
| HTTPS tarball                           | The host serving it. `--source-checksum` recommended.           |
| Local directory                         | The operator.                                                   |

The maintainer of the canonical catalog is the curation gate: only they flip
an entry to `status: tested`. Contributors mark new servers as `documented`
or `candidate` and submit PRs. The same shape works for org-private
catalogs — mirror the schema in any GitHub repo, HTTPS tarball, or local
directory, and point `fetch` at it.

## Org-private catalog walkthrough

```sh
# Org admin maintains a private catalog at github:acme/fracta-mcp-catalog
$ tree github:acme/fracta-mcp-catalog
acme/fracta-mcp-catalog/
├── README.md
├── catalog.yaml                   # version: 3
└── mcp-servers/                   # mirrors the canonical schema; same shape
    ├── catalog.yaml
    ├── acme-crm/server.yaml
    ├── acme-soc/server.yaml
    └── acme-vendor/server.yaml

# Operator joining the org pulls the org catalog
$ fracta config mcp fetch github:acme/fracta-mcp-catalog@v1
$ git add mcp-servers/ && git commit -m "switch to acme catalog v1"

# Or layers it on top of the canonical
$ fracta config mcp fetch                                            # canonical
$ fracta config mcp fetch github:acme/fracta-mcp-catalog@v1 --merge  # acme on top

# CI: pin the catalog version with --source-checksum
$ fracta config mcp fetch \
    https://github.com/acme/fracta-mcp-catalog/releases/download/v1.4/catalog.tar.gz \
    --source-checksum sha256:abc123...
```

The org-admin authoring loop:

```sh
# Add a new private MCP server
$ cd ~/work/fracta-mcp-catalog
$ mkdir mcp-servers/acme-newthing
$ vim mcp-servers/acme-newthing/server.yaml
$ vim mcp-servers/catalog.yaml         # add { id: acme-newthing, path: acme-newthing/server.yaml }
$ vim catalog.yaml                     # bump version: 4
$ git add . && git commit -m "add acme-newthing" && git tag v1.5 && git push --tags

# Operators in the org then run:
$ fracta config mcp fetch github:acme/fracta-mcp-catalog@v1.5
```

## Smoke test — end to end

```sh
# Fresh project
$ git init my-fracta-deploy && cd my-fracta-deploy
$ fracta init --scaffold k8s
$ fracta config mcp list           # errors: no catalog yet
$ fracta config mcp fetch          # populates mcp-servers/ from canonical default
$ fracta config mcp inspect elastic
$ fracta config mcp add elastic --yes      # auto-selects k8s (only enabled mode)
$ kubectl edit secret elastic-mcp-secrets -n fracta   # fill values
$ kubectl apply -f fracta/k8s/manifests/elastic-mcp.yaml
$ kubectl apply -f fracta/k8s/manifests/elastic-mcp-secret.yaml

# Old form still works (with deprecation warning)
$ fracta mcp login elastic
warning: 'fracta mcp login' is deprecated; use 'fracta config mcp auth login'. This alias will be removed in a future minor release.
Opening browser to authorize with elastic...
```

## Rollback

The new `add` command is fully reversible — `fracta config mcp remove
<server> --yes` returns the project byte-identical to its pre-`add` state
(modulo filesystem timestamps). For partial-write failures during `add`, the
rollback is automatic: the new file is removed and the previous file content
is restored from transient `.bak` backups. After a successful `add`, no
`.bak` files remain on disk.

If you want to back out the alias migration entirely, hold any release that
removes the alias group; the alias is a one-release window so the upgrade
path is "land the rename in your scripts before the next minor."
