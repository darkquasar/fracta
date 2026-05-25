---
title: fracta.yaml
description: Schema for the project-root fracta.yaml — connections, auth, agents, runtimes, MCP servers.
---

## Top-level sections

| Section | Purpose |
|---|---|
| `connections` | Datastore connection URLs (Elasticsearch, Redis/FalkorDB, etc.) |
| `auth` | Credential profiles |
| `agents` | Default runtime, default mode, runtime-specific config |
| `hosts` | Per-host runtime configuration (deprecated alias for `agents.agent_runtimes`) |
| `project` | Project-level defaults: base branch, allowed tools |
| `mcp_servers` | MCP server registrations |
| `strategy` | Strategy runner settings: dir, pool_size, gateway_access |

## connections

{/* TODO: full schema. Examples for elasticsearch, redis, falkordb, snowflake. */}

## auth

{/* TODO: profiles, sources, resolvers. Cross-link to credential pipeline. */}

See [Credential Pipeline](/guides/authentication/credential-pipeline) for the full auth model.

## agents

{/* TODO: default_runtime, default_mode, agent_runtimes block. */}

## hosts

<Note>
  `hosts` is the legacy name for `agents.agent_runtimes`. New configs should use `agents.agent_runtimes`.
</Note>

## project

{/* TODO: default_base_branch, allowed_tools list */}

## mcp_servers

{/* TODO: each entry's config block. Cross-link to MCP servers catalog. */}

See [MCP Servers Catalog](/reference/configuration/mcp-servers-catalog) for the bundled server definitions.

## strategy

Strategy-runner configuration. Used by gateway and runner sidecars to discover strategies and decide what context they have at run time.

| Key | Type | Default | Description |
|---|---|---|---|
| `dir` | string | `/opt/fracta/strategies` | Directory the runner walks to discover strategies. In Kubernetes this is the shared `emptyDir` populated by the `stage-strategies` init container; locally it is the `strategies/` directory in the fracta source tree. |
| `pool_size` | int | `1` | Number of strategy sidecar processes to keep warm. Higher values reduce cold-start latency for concurrent `strategy_run` calls but cost RAM (each carries its own DuckDB connection). |
| `gateway_access` | bool | `false` | When `true`, the strategy runner is given the gateway URL and agent task per-request, enabling `ctx.mcp.call_tool()` from within strategies. **Required if any strategy declares `requires.mcp: true`** (e.g. `highlight-distill`, `notion-publish`). In Kubernetes external-sidecar mode this affects only the per-request payload — the runner's own `--gateway-url` CLI arg must also be set, typically via the deployment manifest. Since v0.5.2 all three scaffolds (`local`, `docker-compose`, `k8s`) ship with `gateway_access: true` by default. |

Example:

```yaml
strategy:
  dir: /opt/fracta/strategies
  pool_size: 1
  gateway_access: true
```

## See also

- [Runtime Configuration](/guides/authentication/runtime-configuration)
- [MCP Server Authentication](/guides/authentication/mcp-server-auth)
