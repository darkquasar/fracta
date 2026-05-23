---
title: fracta debug
description: Diagnostic commands for inspecting a running fracta deployment — gateway policy snapshots and the runtime registry.
---

`fracta debug` is the entry point for diagnostic commands that inspect a running
deployment. Unlike most CLI verbs it does **not** require a fracta project
directory: every subcommand resolves its target from explicit flags or
environment variables, so you can run it from anywhere.

```
Debugging and diagnostic commands.

Usage:
  fracta debug [command]

Available Commands:
  gateway     Inspect a running fracta-gateway pod's live state.
  registry    Manage the MCP server registry (debug/admin).
```

## Command tree

```
fracta debug
├── gateway
│   └── policy        Fetch the gateway's tool-policy and visibility snapshot.
└── registry          (manage the MCP server registry — see existing notes in
                       reference/cli/registry.md; debug entry retained for
                       operator muscle memory)
```

## `gateway policy`

Fetches the gateway's tool-policy and visibility snapshot. This is the
verification command for [Gateway Tool Policy](../../guides/gateway-tool-policy.md):
after you edit a `tool_policy` block, `gateway policy` confirms the gateway
loaded it and shows you exactly which tools are visible vs denied.

```
Usage:
  fracta debug gateway policy [flags]

Flags:
      --cp-url string        control-plane API URL (default: control_plane_api.url in fracta.yaml)
      --direct               skip the CP API and call the gateway directly
                             (requires cluster-internal DNS or port-forward)
      --gateway-url string   gateway base URL for --direct mode (default:
                             gateway.url in fracta.yaml or $FRACTA_GATEWAY_URL)
      --verbose              include per-tool visibility breakdown
      --json                 emit raw JSON instead of pretty-printed text
```

### URL resolution

By default the request is routed through the control-plane API (which proxies
to the gateway pod), so thin-client operators don't need cluster-internal DNS
or `kubectl port-forward` to the gateway Service. Resolution order:

1. **`--cp-url <url>` / `control_plane_api.url` in fracta.yaml** — preferred path; the CP daemon proxies to its configured gateway URL.
2. **`--direct` flag** — skip the CP API and hit the gateway directly.
3. **`--gateway-url <url>` / `$FRACTA_GATEWAY_URL` / `gateway.url` in fracta.yaml** — direct fallback.

When `--direct --gateway-url` is supplied, no project lookup happens — the
command works from any directory. Without the explicit flag, it'll best-effort
walk up for a fracta project and read `gateway.url` if one is found.

### Default output

Without `--verbose`, the command prints a summary of the gateway's policy
state:

```
$ fracta debug gateway policy --direct --gateway-url http://localhost:8080
Gateway: http://localhost:8080
Has registry store:   true
Has policies:         true
Visible set built:    true (generation 2)
Catalog size:         4
Visible:              2
Denied by policy:     2
Disabled by registry: 0

Policies (1 servers):
  fracta-test-server:
    deny:       (none)
    allow_only: [ping, echo]
```

Field meanings:

| Field | Meaning |
|---|---|
| `Has registry store` | The gateway is wired up to a persistent registry (postgres/sqlite). Without one, `disabled_by_registry` always reports 0. |
| `Has policies` | At least one `tool_policy` block was loaded from config. False means policy is misconfigured or the pod hasn't picked up a ConfigMap change. |
| `Visible set built` | The visibility pass completed at least once. `generation N` increments on every reload. |
| `Catalog size` | Total tools discovered across all configured MCP backends. |
| `Visible` | Tools agents will see in `tools/list`. |
| `Denied by policy` | Filtered out by `allow_only` / `deny` rules. |
| `Disabled by registry` | Turned off via `fracta config mcp tool disable`. |

`Visible + Denied by policy + Disabled by registry == Catalog size`. If that
identity doesn't hold, the gateway is in an inconsistent state — file an issue.

### `--verbose` per-tool breakdown

`--verbose` adds a per-tool listing with the reason each non-visible tool was
filtered:

```
$ fracta debug gateway policy --direct --gateway-url http://localhost:8080 --verbose
... (header as above) ...

Tools:
  + fracta-test-server.echo
  - fracta-test-server.forbidden_action [denied_by_policy]
  + fracta-test-server.ping
  - fracta-test-server.restricted_action [denied_by_policy]
```

Tools are namespaced as `<server>.<tool>`. `+` is visible to agents; `-` is
filtered out. The bracketed reason is either `denied_by_policy` or
`disabled_by_registry`.

### `--json`

Emits the raw response body for machine consumption — useful for assertions in
shell scripts or CI:

```bash
$ fracta debug gateway policy --direct --gateway-url http://localhost:8080 --json --verbose \
  | jq '.tools | map(select(.visible == false)) | map(.namespaced_name)'
[
  "fracta-test-server.forbidden_action",
  "fracta-test-server.restricted_action"
]
```

### From outside a fracta project

The most common use:

```bash
# From any directory, against a port-forwarded local gateway:
kubectl -n fracta port-forward svc/fracta-gateway 8080:8080 &
fracta debug gateway policy --direct --gateway-url http://localhost:8080 --verbose

# Or via $FRACTA_GATEWAY_URL:
export FRACTA_GATEWAY_URL=http://localhost:8080
fracta debug gateway policy --direct --verbose
```

No `.fracta/` directory is required and no `git init` — `--direct` plus the URL
is sufficient context.

## `registry`

The `registry` subtree predates the `debug` group and retains its own page:
see [`fracta registry`](./registry.md) for the full surface. The `debug
registry` alias is kept for operator muscle memory.

## What's next

- [Gateway Tool Policy](../../guides/gateway-tool-policy.md) — the operator narrative for configuring and verifying policy.
- [`fracta config mcp`](./config-mcp.md) — adding backends and managing their policy declaratively.
