# Notion MCP

## What It Is

`notion` is the official hosted Notion MCP server for pages, databases, comments, search, and workspace updates.

Source: https://developers.notion.com/guides/mcp/overview

## Fracta Status

Cataloged but not yet smoke-tested through fracta. Native remote support is blocked on gateway-owned MCP OAuth.

## Remote Mode

```yaml
notion:
  remote:
    url: https://mcp.notion.com/mcp
    transport: streamable-http
```

Notion's hosted MCP requires OAuth with PKCE. Static Notion integration tokens do not authenticate the hosted MCP endpoint.

## Local Proxy Mode

```yaml
notion:
  local:
    command: npx
    args: ["-y", "mcp-remote", "https://mcp.notion.com/mcp"]
```

Use this as a temporary bridge until fracta supports MCP OAuth directly.
