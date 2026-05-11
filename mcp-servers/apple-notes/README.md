# Apple Notes MCP

## What It Is

`apple-notes` is a macOS-local MCP entry for searching and reading Apple Notes.

Candidate source: https://github.com/sirmews/apple-notes-mcp

## Fracta Status

Candidate and macOS-only. This is useful for recovering personal notes, but it is platform-specific and privacy-sensitive.

## Local-Process Mode

```yaml
apple-notes:
  local:
    command: uvx
    args: ["apple-notes-mcp"]
```

Prerequisites vary by server, but commonly include:

- macOS
- Apple Notes configured locally
- Full Disk Access and/or Automation permissions

## Docker / Kubernetes

Not supported. This depends on a local macOS app and local note database access.
