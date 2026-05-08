# Logseq MCP

## What It Is

`logseq` is a local knowledge-graph MCP entry for Logseq pages, blocks, journals, and graph exploration.

Candidate source: https://github.com/joelhooks/logseq-mcp-tools

## Fracta Status

Candidate. Needs fracta smoke testing and final package choice before being marked `tested`.

## Local-Process Mode

```yaml
logseq:
  local:
    command: npx
    args: ["-y", "logseq-mcp-tools"]
    env:
      LOGSEQ_API_TOKEN: "${LOGSEQ_API_TOKEN}"
```

Prerequisites:

- Logseq running locally
- Logseq API token

## Docker / Kubernetes

Not recommended for desktop-local graph access.
