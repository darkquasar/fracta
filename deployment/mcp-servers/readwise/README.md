# Readwise / Reader MCP

## What It Is

`readwise` is the official Readwise and Reader MCP server for highlights, Reader documents, tags, inbox triage, digests, and resurfacing saved reading history.

Source: https://readwise.io/mcp

## Fracta Status

Cataloged but not yet smoke-tested through fracta. Native remote support is blocked on gateway-owned MCP OAuth.

## Remote Mode

```yaml
readwise:
  remote:
    url: https://mcp2.readwise.io/mcp
    transport: streamable-http
```

This requires OAuth and will need fracta gateway OAuth support.

## Local Proxy Mode

Use `mcp-remote` as a bridge until fracta owns OAuth:

```yaml
readwise:
  local:
    command: npx
    args: ["-y", "mcp-remote", "https://mcp2.readwise.io/mcp"]
```

This stores auth outside fracta in the proxy's auth cache.
