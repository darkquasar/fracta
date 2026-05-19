---
title: MCP Catalog Workflow
description: Operator guide for fracta's MCP server catalog — fetch, commit, add, remove.
---

# MCP Catalog Workflow

Fracta ships a curated catalog of MCP server definitions and a `fracta config mcp` command suite that turns them into per-mode deployment config. This page is the operator narrative: when to use which verb, what gets committed where, and how org-private catalogs fit in.

For the exhaustive flag reference, see [`fracta config mcp`](../reference/cli/config-mcp.md). For the schema of a `server.yaml` entry, see [MCP Servers Catalog](../reference/configuration/mcp-servers-catalog.md).

## The mental model

Three things to keep separate:

1. **The catalog** — a tree at `<root>/mcp-servers/` containing one `server.yaml` per MCP server fracta knows about. Operator-owned, git-tracked, edited like any other config.
2. **Your scaffold** — the per-mode files `fracta init` wrote (`fracta.yaml`, plus `deployment/docker-compose.yml` or `deployment/k8s/manifests/` depending on the mode).
3. **`fracta config mcp`** — the CLI that reads (1) and mutates (2) on your behalf.

`fetch` populates (1). `add` reads (1) and writes (2). Both are reversible.

## The default workflow

From a fresh project:

```bash
git init my-fracta-deploy && cd my-fracta-deploy
fracta init --scaffold k8s             # or local / docker-compose

fracta config mcp fetch                # pulls from github:darkquasar/fracta@main by default
git add mcp-servers/ && git commit -m "fetch fracta MCP catalog"

fracta config mcp list                 # browse what's available
fracta config mcp inspect elastic      # full per-server metadata
fracta config mcp add elastic          # writes fracta.yaml + deployment/k8s/manifests/elastic-mcp.yaml
git add . && git commit -m "add elastic MCP server"
```

Three commits land on your repo: the scaffold (`fracta init`), the catalog (`fetch`), and each server (`add`). Reviewing those PRs is the whole point — your team sees exactly which image, which auth mode, and which endpoint is being introduced before it hits production.

### What `add` writes per mode

| Mode | Files touched |
|---|---|
| `local` | `fracta.yaml` only (inserts `mcp_servers.servers.<id>.local`) |
| `docker-compose` | `fracta.yaml` + `deployment/docker-compose.yml` + `.env.example` |
| `k8s` | `fracta.yaml` + `deployment/k8s/manifests/<id>-mcp.yaml` + `deployment/k8s/manifests/<id>-mcp-secret.yaml` (if the server needs env vars) |

`add` refuses to run if the scaffold for the chosen mode isn't present — the error names the prereq command (`fracta init --scaffold <mode>`) verbatim.

### The pre-flight summary

Every `add` shows you what it's about to do and waits for confirmation:

```
Adding 'elastic' for target-deployment 'k8s':
  Container required:    yes (image: docker.elastic.co/mcp/elasticsearch:latest, owner: external)
  Container build:       not required (public image)
  Image present locally: yes (docker)
  Auth: env_token; you must populate ES_URL and ES_API_KEY in a Secret
  Files to be written:
    + deployment/k8s/manifests/elastic-mcp.yaml
    + deployment/k8s/manifests/elastic-mcp-secret.yaml
    ~ fracta.yaml
Proceed? [y/N]
```

Pass `--yes` in CI. Pass `--dry-run` to see the summary without writing.

### Removing a server

`fracta config mcp remove <server>` reverses an `add`: it deletes the per-mode manifest and removes the `mcp_servers.servers.<id>` block from `fracta.yaml`. The result is byte-identical to the pre-`add` state (modulo filesystem timestamps).

If `add` fails partway through a multi-file write, the rollback is automatic — transient `.bak` files restore the original content and are then removed. After a successful `add`, no `.bak` files remain on disk.

## Why the catalog is checked in

The fetched `mcp-servers/` directory is **first-class config**, not a cache. It belongs in git for three reasons:

- **Audit.** Reviewers should see the image refs and OAuth endpoints before they ship. A git diff on `mcp-servers/elastic/server.yaml` makes that review trivial.
- **Local entries.** Operators routinely add servers that aren't in the canonical catalog — an internal CRM MCP, a vendor-specific bridge. Those live as `mcp-servers/<id>/server.yaml` alongside the fetched ones; `fetch --merge` preserves them.
- **Reproducibility.** The catalog's `version:` field bumps over time. Pinning a specific version in git means everyone on the team runs against the same definitions.

Fracta deliberately does *not* bake the catalog into the binary. `fracta config mcp list` errors with a clear remediation if `<root>/mcp-servers/` is missing:

```
no catalog found at <root>/mcp-servers/
run 'fracta config mcp fetch' to populate it (default source: github:darkquasar/fracta@main)
```

## Catalog sources

`fracta config mcp fetch [<source>]` accepts the same syntax everywhere fracta takes a source:

| Form | Example | Notes |
|---|---|---|
| Default (canonical) | `fracta config mcp fetch` | Equivalent to `github:darkquasar/fracta@main` |
| GitHub repo | `fracta config mcp fetch github:acme/fracta-cat@v1` | Tag, branch, or sha after `@` |
| Browser-pasted URL | `fracta config mcp fetch https://github.com/acme/fracta-cat` | Trailing slash and `.git` suffix tolerated |
| SSH form | `fracta config mcp fetch git@github.com:acme/fracta-cat.git@v1` | Pasted from `git remote -v`; fetched over HTTPS regardless |
| Raw tarball | `fracta config mcp fetch https://example.com/cat.tar.gz` | Expects `mcp-servers/` at archive root |
| Local directory | `fracta config mcp fetch ./internal-cat` | Reads `<path>/mcp-servers/` |

After a successful non-merge fetch, the source is recorded in `mcp-servers/.fracta-source`. Subsequent `fetch` calls without arguments use the recorded source rather than the global default.

### Replace vs merge

By default `fetch` is **replace**: the existing `mcp-servers/` is removed and rewritten from the source. Pass `--merge` to overlay the source's entries on top of what you have, preserving local-only entries by id. Merge does **not** update `.fracta-source` — it's a one-shot overlay, not a primary-source change.

```bash
# Canonical only
fracta config mcp fetch

# Org overlay on top of canonical
fracta config mcp fetch                                              # 14 canonical entries
fracta config mcp fetch github:acme/fracta-mcp-catalog@v1 --merge    # adds 3 acme entries

git diff mcp-servers/                                                # review before commit
```

### Trust boundaries

| Source | Trust |
|---|---|
| `github:darkquasar/fracta@main` | The `darkquasar/fracta` GitHub repo. Pin with `--source-checksum` if using the tag-archive HTTPS form. |
| Custom github source | The org or user that owns the repo. |
| HTTPS tarball | The host serving it. `--source-checksum sha256:...` recommended. |
| Local directory | You. |

`add` is interactive by default and shows every image ref before writing — operators audit at the moment of adoption, not as a separate exercise.

## Org-private catalogs

Mirror the canonical schema (`catalog.yaml` + `mcp-servers/<id>/server.yaml`) in any GitHub repo, HTTPS tarball, or local directory. There's no fracta-side registration — the source spec is the entire interface.

```
acme/fracta-mcp-catalog/
├── README.md
├── catalog.yaml                    # version: 3
└── mcp-servers/
    ├── catalog.yaml
    ├── acme-crm/server.yaml
    ├── acme-soc/server.yaml
    └── acme-vendor/server.yaml
```

Authoring loop for the org admin:

```bash
cd ~/work/fracta-mcp-catalog
mkdir mcp-servers/acme-newthing
$EDITOR mcp-servers/acme-newthing/server.yaml
$EDITOR mcp-servers/catalog.yaml         # add { id: acme-newthing, path: acme-newthing/server.yaml }
$EDITOR catalog.yaml                     # bump version: 4
git commit -am "add acme-newthing" && git tag v1.5 && git push --tags
```

Operators on the team then run:

```bash
fracta config mcp fetch github:acme/fracta-mcp-catalog@v1.5
```

For CI reproducibility, pin a specific archive sha256 via the HTTPS tarball form:

```bash
fracta config mcp fetch \
  https://github.com/acme/fracta-mcp-catalog/releases/download/v1.5/catalog.tar.gz \
  --source-checksum sha256:abc123...
```

## Diffing local vs remote

`fracta config mcp list --remote` compares the local catalog against the canonical remote without writing anything. Use it as a "what's new upstream" check before deciding to refetch:

```
SERVER         LOCAL                          REMOTE   DELTA          AUTH        DESCRIPTION
elastic        configured (k8s+compose)       v2       up-to-date     env_token   Elasticsearch-backed search/enrichment
notion         not configured                 v2       available      oauth       Notion knowledge graph
google-drive   not configured                 v2       available      oauth       Google Drive/Workspace
zotero         not configured                 v2       available      bearer      Zotero papers/citations
```

To incorporate remote-only entries, run `fracta config mcp fetch --merge` (preserves local-only) or `fracta config mcp fetch` (wholesale replace).

## OAuth-protected servers

Some MCP servers (Notion, Readwise, Google Drive) authenticate via OAuth rather than env tokens. The credential flow stays under `fracta config mcp auth`:

```bash
fracta config mcp auth login notion       # opens browser
fracta config mcp auth status             # what's authenticated
fracta config mcp auth export notion      # token export for CI
fracta config mcp auth logout notion
```

These are the same runners that used to live at `fracta mcp <verb>` — the [deprecated alias](../reference/cli/mcp.md) still works for one minor release.

## What's next

- [`fracta config mcp` reference](../reference/cli/config-mcp.md) — every flag, every subcommand.
- [MCP Servers Catalog reference](../reference/configuration/mcp-servers-catalog.md) — `server.yaml` schema, the bundled entries.
- [`fracta mcp` (deprecated alias)](../reference/cli/mcp.md) — old top-level verbs and the remapping table. 
