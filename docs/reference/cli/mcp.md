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
[`fracta config mcp`](./config-mcp.md). See the
[migration page](../../migration/spec-43-config-mcp.md) for the remapping
table and a sed one-liner.

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
ships. Plan your migration window accordingly — the sed substitution in the
migration doc takes seconds per repo, and there are no semantic differences
between the old and new paths.

## Where the rest of MCP management lives

Everything beyond OAuth credential management is brand-new on the
`fracta config mcp` path — there's no old equivalent:

- `fracta config mcp fetch` — populate the catalog
- `fracta config mcp list` — browse local + remote
- `fracta config mcp inspect` — full per-server metadata
- `fracta config mcp add` — scaffold a server into your deployment
- `fracta config mcp remove` — reverse `add`

See [`config-mcp`](./config-mcp.md) for the full surface.
