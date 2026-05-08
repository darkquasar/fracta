# Tana MCP

## What It Is

`tana` covers Tana's official local MCP/API and the community Input API MCP for creating structured nodes, tasks, fields, and supertags.

Official local API source: https://tana.inc/docs/local-api-mcp
Community Input API source: https://github.com/tim-mcdonnell/tana-mcp

## Fracta Status

Cataloged but not yet smoke-tested through fracta.

## Local API Mode

Use this when Tana Desktop is running with Local API enabled:

```yaml
tana:
  remote:
    url: http://localhost:8262/mcp
    transport: streamable-http
```

## Input API Mode

Use this for token-backed write automation:

```yaml
tana:
  local:
    command: npx
    args: ["-y", "tana-mcp"]
    env:
      TANA_API_TOKEN: "${TANA_API_TOKEN}"
      TANA_DEFAULT_TARGET: "INBOX"
```

## Docker / Kubernetes

The local API mode is desktop-local. Input API token mode may work in containers, but is not catalog-tested yet.
