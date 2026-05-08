# Raindrop.io MCP

## What It Is

`raindrop` is the official Raindrop.io MCP server for bookmarks, collections, tags, highlights, and saved page content.

Source: https://help.raindrop.io/integrations/mcp

## Fracta Status

Cataloged but not yet smoke-tested through fracta. Unlike many hosted SaaS MCPs, Raindrop supports direct bearer tokens, so it can work before fracta has native OAuth.

## Remote Bearer Mode

```yaml
raindrop:
  remote:
    url: https://api.raindrop.io/rest/v2/ai/mcp
    transport: streamable-http
    headers:
      Authorization: "Bearer ${RAINDROP_TOKEN}"
```

Use a Raindrop API token in `RAINDROP_TOKEN`.

## Remote OAuth Mode

```yaml
raindrop:
  remote:
    url: https://api.raindrop.io/rest/v2/ai/mcp
    transport: streamable-http
```

This requires fracta gateway OAuth support.

## Local Proxy Mode

```yaml
raindrop:
  local:
    command: npx
    args: ["-y", "mcp-remote", "https://api.raindrop.io/rest/v2/ai/mcp"]
```
