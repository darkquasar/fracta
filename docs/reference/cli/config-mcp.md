---
title: fracta config mcp
description: Entry point for MCP catalog management and per-mode server wiring.
---

`fracta config mcp` is the entry point for catalog management and per-mode
server wiring. It is part of the broader `fracta config` umbrella for
project-config commands.

```
Manage MCP servers in this fracta project.

Usage:
  fracta config mcp [command]

Available Commands:
  add         Inject an MCP server into the current scaffold mode (or explicit targets)
  auth        Manage credentials for OAuth-protected MCP servers
  fetch       Populate <root>/mcp-servers/ from a catalog source
  inspect     Show full per-server metadata
  list        List every MCP server in the local catalog
  manifest    Emit deployment artifacts for an MCP backend from the catalog
  remove      Reverse 'add' for one server
  tool        Manage individual MCP server tools (enable, disable, list, policy)
```

## Command tree

```
fracta config mcp
├── fetch [<source>]               populate <root>/mcp-servers/
├── list
├── inspect <server>
├── add <server>                   write per-mode config + manifests
├── manifest <server>              render-only — no writes, no project required
├── remove <server>
├── tool
│   ├── enable <server> <tool>
│   ├── disable <server> <tool>
│   ├── list
│   └── policy
└── auth
    ├── login <server>
    ├── logout <server>
    ├── status [server]            (was: 'fracta mcp auth-status')
    └── export <server>
```

The old `fracta mcp <verb>` form is preserved as a deprecation alias for one
minor release — see [`fracta mcp` (deprecated alias)](./mcp.md) for the
remapping table and a sed one-liner.

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
  docker-compose  yes (deployment/docker-compose.yml service:elastic-mcp)
  kubernetes      yes (deployment/k8s/manifests/elastic-mcp.yaml)
```

## `add <server>`

Performs the per-mode hand-edits operators do today, deterministically and
idempotently, after explaining what running/building/pulling will be needed.

By default `add` operates on the surrounding fracta project (walks up for
`.fracta/`) and derives the target mode from which scaffolds are enabled. To
run outside a fracta project — adding a backend to a remote deployment from a
non-project directory — supply `--target-deployment` plus the relevant
**standalone-mode** path flag(s) listed below.

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

Standalone-mode flags (any one opts out of the project walk-up):
  --config <fracta.yaml path>                       Target fracta.yaml; bypasses project walk-up.
  --compose-file <docker-compose.yml path>          Target compose file; for --target-deployment docker-compose.
  --k8s-manifest-dir <dir>                          Where to write <id>-mcp.yaml; for --target-deployment k8s.
  --catalog-dir <mcp-servers/ path>                 Source catalog dir; bypasses project walk-up for the catalog.
```

### Per-mode write contract

| Mode | Files touched |
|---|---|
| `local` | `<root>/fracta.yaml` (`mcp_servers.servers.<id>.local`) |
| `docker-compose` | `<root>/fracta.yaml` (`.remote`), `<root>/deployment/docker-compose.yml` (`services.<id>-mcp`), `<root>/.env.example` |
| `k8s` | `<root>/fracta.yaml` (`.remote`), `<root>/deployment/k8s/manifests/<id>-mcp.yaml` (Deployment+Service), `<root>/deployment/k8s/manifests/<id>-mcp-secret.yaml` (Secret stub — only if `auth.env_required` non-empty) |

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

### Standalone mode

When any of `--config`, `--compose-file`, `--k8s-manifest-dir`, or
`--catalog-dir` is supplied, `add` runs in **standalone mode**: no project
walk-up happens, and the project's scaffold state isn't consulted.

In standalone mode:

- `--target-deployment` is **required** (no project state to infer from).
- The relevant path flag for the chosen mode must be supplied:

  | Mode | Required flag |
  |---|---|
  | `local` | `--config <fracta.yaml path>` |
  | `docker-compose` | `--compose-file <docker-compose.yml path>` (`--config` is also accepted for the `fracta.yaml` block) |
  | `k8s` | `--k8s-manifest-dir <dir>` (and `--config` for the `fracta.yaml` block) |

- The `.env.example` side-effect (compose mode) is dropped — the operator
  owns env-var declaration when running standalone.

Example: add `fracta-test-server` to a k8s deployment whose ConfigMap and
manifests live in a separate directory:

```bash
fracta config mcp add fracta-test-server --target-deployment k8s \
    --catalog-dir ~/GitHub/fracta/mcp-servers \
    --k8s-manifest-dir /opt/fracta-deploy/k8s/manifests \
    --config /opt/fracta-deploy/fracta.yaml \
    --yes
```

Without any standalone flag, `add` falls back to the project-walk-up
behaviour — useful when you're inside a fracta project and want the default
paths.

## `manifest <server>`

Renders the deployment artifacts for an MCP backend from the catalog to
stdout. Does **not** require a fracta project directory and does **not**
write any files — pipe to `kubectl apply -f -`, paste into a compose file,
or capture for review.

```
Usage: fracta config mcp manifest <server> [flags]

Flags:
  --variant <name>            Variant name (default: first variant supporting the chosen output).
  --namespace <ns>            Kubernetes namespace (k8s output only; default: fracta).
  --image <image:tag>         Override the variant's image (k8s/compose output only).
  --catalog-dir <path>        Path to mcp-servers/ catalog
                              (default: project's mcp-servers/, else cwd/mcp-servers).
  -o, --output {k8s|compose|fracta-yaml}    Output format (default: k8s).
```

### Output formats

| `-o` value | Emits | Variant must declare |
|---|---|---|
| `k8s` (default) | Deployment + Service YAML, ready for `kubectl apply -f -` | `image` |
| `compose` | A single docker-compose `services.<id>-mcp:` block | `image` |
| `fracta-yaml` | An `mcp_servers.servers.<id>:` snippet for `fracta.yaml` | `image` or `url` |

### Examples

Render a Deployment+Service for the bundled test server and apply it:

```bash
fracta config mcp manifest fracta-test-server \
    --catalog-dir ~/GitHub/fracta/mcp-servers \
    | kubectl apply -f -
```

Render a docker-compose service block for inline pasting:

```bash
fracta config mcp manifest elastic -o compose --image my-fork/elastic-mcp:v2
```

Render a `fracta.yaml` snippet to add to an existing project by hand:

```bash
fracta config mcp manifest notion -o fracta-yaml
```

### When to use `manifest` vs `add`

| | `manifest` | `add` |
|---|---|---|
| Side effects | None — stdout only | Writes files; can be rolled back |
| Project required | No | No (with standalone flags); yes (without) |
| Idempotency | Trivial (pure function) | Yes — re-running is a no-op |
| Use for | Inspection, CI templating, ad-hoc deploys | Committing config to a fracta project |

If you find yourself piping `manifest` output into your editor repeatedly,
that's a sign `add` (or `add` with standalone flags) is the right tool.

## `tool`

Per-tool enable/disable + policy inspection. See [Gateway Tool Policy](../../guides/gateway-tool-policy.md)
for the operator narrative.

```
fracta config mcp tool enable <server> <tool>     Mark a tool enabled in the registry
fracta config mcp tool disable <server> <tool>    Mark a tool disabled in the registry
fracta config mcp tool list [--server <name>]     Show tools with enabled/policy/visible columns
fracta config mcp tool policy [--server <name>]   Show effective tool_policy from fracta.yaml
```

`enable` / `disable` write to the registry store (postgres or sqlite,
depending on the backend) — they're the imperative override for individual
tools. The declarative path is `tool_policy:` in `fracta.yaml`; see the
[gateway tool policy guide](../../guides/gateway-tool-policy.md).

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

### The `--config <yaml>` flag (required for in-cluster OAuth)

`fracta config mcp auth login` and `fracta config mcp auth export` read the
target server's definition from a YAML file rather than introspecting a
running gateway. Pass `--config <path>` to point them at one.

- In deployments where the in-cluster gateway ConfigMap is the source of
  truth (Kubernetes, Docker Compose), the host's `fracta.yaml` typically
  describes only the thin-client control-plane connection — it does NOT
  list the OAuth servers. Without `--config`, the CLI errors with
  `server "<name>" not found in config`.
- The recommended pattern is a tiny **throwaway** `<server>-login.yaml` per
  server, describing only the OAuth endpoint shape (no token file paths).
  The CLI uses it to drive the OAuth dance; the tokens land in your OS
  keyring, and `fracta config mcp auth export --format k8s-secret --config
  <server>-login.yaml` renders them as a Kubernetes Secret you can apply.

```bash
# Login (opens browser, writes tokens to OS keyring)
fracta config mcp auth login notion --config ./notion-login.yaml

# Export as a k8s Secret for the gateway to consume
fracta config mcp auth export notion \
  --format k8s-secret --config ./notion-login.yaml \
  > notion-oauth-secret.yaml
```

See [Reading Garden — Setup](/patterns/reading-garden/setup#step-1--wire-the-hosted-mcps-through-fractas-native-oauth) for an end-to-end worked example covering both Notion and Readwise.

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
[org-private catalog walkthrough](../../guides/mcp-catalog.md#org-private-catalogs)
in the MCP catalog guide. 
