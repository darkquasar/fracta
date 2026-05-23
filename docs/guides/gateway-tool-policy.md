---
title: Gateway Tool Policy
description: Restrict which MCP tools agents can see and call. Per-server allow_only / deny lists, configuration in fracta.yaml, and how to verify enforcement with fracta debug.
---

The fracta gateway enforces a per-MCP-server **tool policy**: a small declarative block in `fracta.yaml` that filters which tools agents see in `tools/list` and which they can invoke via `tools/call`. Enforcement happens twice — at visibility build time (tools the agent can't see don't appear in their tool list) and at request time (a denied tool call returns a structured error).

This page covers the policy shape, where it sits in your config, and the workflow for verifying enforcement end-to-end.

For the CLI ref, see [`fracta debug gateway policy`](../reference/cli/debug.md). For the broader catalog narrative, see [MCP Catalog Workflow](./mcp-catalog.md).

## The policy shape

A `tool_policy` block under any `mcp_servers.servers.<id>` entry carries two optional lists:

```yaml
mcp_servers:
  servers:
    fracta-test-server:
      remote:
        url: http://fracta-test-server.fracta.svc:8000/mcp
        transport: streamable-http
      tool_policy:
        allow_only:
          - ping
          - echo
        deny:
          - dangerous_action
```

The fields:

| Field | Behaviour | When to use |
|---|---|---|
| `allow_only` | Whitelist. Only tools matching one of these names are visible. Everything else on the backend is denied. | When you know the small set of tools agents should use. |
| `deny` | Blacklist. Tools matching one of these names are denied; everything else is allowed. | When the backend has a few risky tools but most are fine. |

Both lists are matched against the **raw tool name** as the backend declares it — without the `<server>.` namespace prefix the gateway adds when proxying. So `ping` matches `fracta-test-server.ping`.

Both lists support exact names, `*` as wildcard, and `prefix*` glob matching (spec-47 §2A). `allow_only: [read_*]` lets `read_file`, `read_url`, etc. through and denies everything else.

You can combine them: `allow_only` runs first, then `deny` narrows further.

### Default behaviour without a policy

Omit `tool_policy` entirely (or leave both lists empty) and the gateway makes every tool the backend exposes visible to every agent. The default is permissive — policy is opt-in per server.

## Where the policy lives

Two files matter:

1. **Project `fracta.yaml`** — the source of truth operators commit and review. Edit the `tool_policy` block here.
2. **Gateway ConfigMap** (k8s: `deployment/k8s/manifests/fracta-gateway.yaml`; compose: `deployment/configs/gateway.yaml`) — what the running gateway pod actually reads.

Today these don't auto-sync — the gateway picks up changes only after the ConfigMap is updated and the pod is restarted. Copy the `tool_policy` block to both locations until the live-sync command lands.

After editing the gateway ConfigMap on k8s:

```bash
kubectl -n fracta apply -f deployment/k8s/manifests/fracta-gateway.yaml
kubectl -n fracta rollout restart deployment fracta-gateway
```

The gateway also re-evaluates policies on its own internal config-reload signals (no full restart needed in some paths) — restart is the simplest reliable trigger.

## Verifying enforcement

The verification workflow is the same in any deployment mode: ask the gateway what it sees, and confirm the visible/denied counts match what your policy declares.

### Snapshot the policy state

[`fracta debug gateway policy --verbose`](../reference/cli/debug.md) prints a per-tool breakdown:

```
$ fracta debug gateway policy --direct --gateway-url http://localhost:8080 --verbose
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

Tools:
  + fracta-test-server.echo
  - fracta-test-server.forbidden_action [denied_by_policy]
  + fracta-test-server.ping
  - fracta-test-server.restricted_action [denied_by_policy]
```

What each column means:

- `+` and `-` prefix every tool in the catalog. `+` is visible to agents; `-` is filtered out.
- `[denied_by_policy]` — your `allow_only`/`deny` rules excluded the tool.
- `[disabled_by_registry]` — an operator turned the tool off explicitly via [`fracta config mcp tool disable`](../reference/cli/config-mcp.md#tool).
- The summary line counts (`Visible`, `Denied by policy`, `Disabled by registry`) must add up to `Catalog size`.

`--direct --gateway-url <url>` bypasses the controlplane API and hits the gateway's own debug endpoint directly. Use it from any directory — no fracta project required (spec-49 §1.4).

If `Has policies: false`, the gateway never received a `tool_policy` block. Either it's missing from the ConfigMap or the pod hasn't restarted to pick it up.

### Confirm at request time

The visibility check is one side of enforcement. The other is what happens when an agent (or a curl-wielding human) tries to call a denied tool. Drive an MCP request through the gateway and look at the response:

```bash
# Initialize a session (every agent does this on connect).
curl -sX POST http://localhost:8080/agents/policy-check/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize",
       "params":{"protocolVersion":"2024-11-05","capabilities":{},
                 "clientInfo":{"name":"policy-check","version":"1"}}}'

# Call an allowed tool — succeeds.
curl -sX POST http://localhost:8080/agents/policy-check/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call",
       "params":{"name":"fracta-test-server.ping","arguments":{}}}'

# {"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"pong"}], ... }}

# Call a denied tool — returns a structured error.
curl -sX POST http://localhost:8080/agents/policy-check/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call",
       "params":{"name":"fracta-test-server.forbidden_action","arguments":{}}}'

# {"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text",
#   "text":"tool \"fracta-test-server.forbidden_action\" is not available (blocked by policy or disabled)"}],
#   "isError":true}}
```

The `isError: true` flag plus the `(blocked by policy or disabled)` suffix are the gateway's request-time signal that policy intervened. Agents see this just like any other tool error.

`tools/list` over the same connection returns only the visible tools — the denied ones never reach the agent at all, so well-behaved clients won't even attempt the call.

## Common patterns

### Read-only access to a write-capable backend

A backend exposing both `read_*` and `write_*` tools, where agents should only read:

```yaml
tool_policy:
  allow_only:
    - "read_*"
```

### Block a specific destructive tool, allow everything else

```yaml
tool_policy:
  deny:
    - delete_everything
```

### Block destructive tools but only allow a narrow set

```yaml
tool_policy:
  allow_only:
    - "read_*"
    - "search"
  deny:
    - "read_secrets"   # still blocked even though read_* is allowed
```

`deny` always wins over `allow_only` — useful when a prefix glob would otherwise let through a tool you specifically want blocked.

## Per-tool disable vs policy

`tool_policy` is the *declarative* approach (committed in YAML, reviewed in PRs, applies to every agent). For one-off operator overrides — disabling a specific tool while you investigate a misbehaving backend, for example — use the imperative [`fracta config mcp tool disable <server> <tool>`](../reference/cli/config-mcp.md#tool) which writes to the registry store.

Both gate the same visibility computation:

| Source | Persistence | Use for |
|---|---|---|
| `tool_policy` in `fracta.yaml` | Git-tracked, ConfigMap-deployed | Long-lived per-environment rules |
| `fracta config mcp tool disable` | Registry store (postgres/sqlite) | Operator-driven incident response or experimentation |

A tool denied by either mechanism shows up in `fracta debug gateway policy --verbose` with the appropriate `[denied_by_policy]` or `[disabled_by_registry]` reason.

## What's next

- [`fracta debug gateway policy`](../reference/cli/debug.md) — CLI ref for the verification command.
- [`fracta config mcp tool`](../reference/cli/config-mcp.md#tool) — imperative per-tool enable/disable.
- [MCP Server Examples](./mcp-server-examples.md) — `tool_policy` block alongside the rest of an MCP server config.
- [Troubleshooting](../operations/troubleshooting.md) — what to check when agents report "tool not available" unexpectedly.
