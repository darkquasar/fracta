# Joplin MCP

## What It Is

`joplin` is a local/open-source notes MCP entry for Joplin notes, notebooks, todos, and search.

Catalog source: https://mcp.directory/servers/joplin

## Fracta Status

Candidate. Needs fracta smoke testing before being marked `tested`.

## Local-Process Mode

```yaml
joplin:
  local:
    command: npx
    args: ["-y", "joplin-mcp-server", "--token", "${JOPLIN_TOKEN}"]
```

Prerequisites:

- Joplin token
- Joplin sync/backend setup as required by the chosen server

## Docker / Kubernetes

Possible if the Joplin backend and token are available to the container, but not catalog-tested yet.
