---
title: fracta mcp (deprecated alias)
description: One-release deprecation alias for the relocated MCP commands.
---

# `fracta mcp` (deprecated alias)

The top-level `fracta mcp <verb>` group has moved to `fracta config mcp auth
<verb>` in spec-43. The old paths are preserved as a deprecation alias for
one minor release. Every invocation emits a stderr warning naming the new
path, then runs the same underlying runner — the OAuth flow, keyring writes,
and credential-store behaviour are unchanged.

For all new code and scripts, use the canonical paths under
[`fracta config mcp`](./config-mcp.md). The remapping table and a sed
one-liner for updating scripts are below.

## What still works on this path

```
fracta mcp login <server>          ->  fracta config mcp auth login <server>
fracta mcp logout <server>         ->  fracta config mcp auth logout <server>
fracta mcp auth-status [server]    ->  fracta config mcp auth status [server]
fracta mcp export <server>         ->  fracta config mcp auth export <server>
```

All flags carry over verbatim — `--device-code`, `--format`, `--output-dir`.

The hyphenated `auth-status` is preserved on the alias path because the
alias can't transparently re-target a renamed verb on a different parent.
The deprecation warning points at the new spelling (`status`) so operators
update once and stay updated.

## Sample output

```
$ fracta mcp login notion
warning: 'fracta mcp login' is deprecated; use 'fracta config mcp auth login'. This alias will be removed in a future minor release.
Opening browser to authorize with notion...
```

## Removal timeline

The alias group will be removed in the next minor release after spec-43
ships. Plan your migration window accordingly — the sed substitution below
takes seconds per repo, and there are no semantic differences between the
old and new paths.

## Updating scripts

```sh
# Replace 'fracta mcp <verb>' with the new path. Note the auth-status rename.
sed -i.bak \
  -e 's|fracta mcp auth-status|fracta config mcp auth status|g' \
  -e 's|fracta mcp login|fracta config mcp auth login|g' \
  -e 's|fracta mcp logout|fracta config mcp auth logout|g' \
  -e 's|fracta mcp export|fracta config mcp auth export|g' \
  $(grep -rln 'fracta mcp ' . --include='*.sh' --include='*.yaml' --include='*.yml' --include='*.md')
```

Order matters: `auth-status` must be substituted before `login`/`logout` so the
`auth-` prefix isn't mangled.

## Where the rest of MCP management lives

Everything beyond OAuth credential management is brand-new on the
`fracta config mcp` path — there's no old equivalent:

- `fracta config mcp fetch` — populate the catalog
- `fracta config mcp list` — browse local + remote
- `fracta config mcp inspect` — full per-server metadata
- `fracta config mcp add` — scaffold a server into your deployment
- `fracta config mcp remove` — reverse `add`

See [`config-mcp`](./config-mcp.md) for the full surface.
