# Obsidian MCP

## What It Is

`obsidian` is a local-first MCP entry for Obsidian vaults. The preferred candidate is `cyanheads/obsidian-mcp-server`, which talks to the Obsidian Local REST API plugin.

Source: https://github.com/cyanheads/obsidian-mcp-server

## Fracta Status

Candidate. This is the highest-priority local knowledge-garden MCP, but it still needs a fracta smoke test before being marked `tested`.

## Local-Process Mode

```yaml
obsidian:
  local:
    command: npx
    args: ["-y", "obsidian-mcp-server"]
    env:
      OBSIDIAN_API_KEY: "${OBSIDIAN_API_KEY}"
      OBSIDIAN_BASE_URL: "http://127.0.0.1:27123"
```

Prerequisites:

- Obsidian desktop running locally
- Obsidian Local REST API plugin installed and enabled
- `OBSIDIAN_API_KEY`
- `OBSIDIAN_BASE_URL`

## Docker / Kubernetes

Not recommended. This MCP depends on a local desktop app and localhost vault access.
