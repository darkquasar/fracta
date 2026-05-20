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

## See also

- [Runtime Configuration](/guides/authentication/runtime-configuration)
- [MCP Server Authentication](/guides/authentication/mcp-server-auth)
