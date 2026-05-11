---
title: MCP Servers Catalog
description: Reference for the bundled MCP server definitions
---

# MCP Servers Catalog

Fracta ships a catalog of pre-configured MCP server definitions in `mcp-servers/`. The catalog index lives at `mcp-servers/catalog.yaml`. Each server has its own `server.yaml`.

## Catalog index

{/* TODO: list of all servers with one-line description and status (tested / documented / candidate) */}

| Server | Category | Status | Notes |
|---|---|---|---|
| `fracta` | first-party | tested | Built-in fracta MCP surface |
| `elastic` | security | tested | Elasticsearch MCP server (vendor image) |
| `vendor` | security | tested | VendorSecurity (vendor) MCP server |
| `notion` | knowledge | documented | Hosted MCP, OAuth |
| `obsidian` | knowledge | candidate | Local API token |
| `readwise` | knowledge | documented | Hosted MCP, OAuth |
| `raindrop` | knowledge | documented | Hosted MCP, OAuth |
| `zotero` | knowledge | candidate | Local API token |
| `google-drive` | knowledge | documented | OAuth |
| `logseq` | knowledge | candidate | Local API |
| `joplin` | knowledge | candidate | Local API |
| `apple-notes` | knowledge | candidate | macOS-only |
| `noteplan` | knowledge | candidate | macOS-only |
| `tana` | knowledge | candidate | API token |

## Per-server configuration

{/* TODO: explain server.yaml format, document fields: id, name, version, description, env, command, args, secrets, notes */}

## Authentication

For OAuth-protected servers, see [MCP Server Authentication](/guides/authentication/mcp-server-auth).
