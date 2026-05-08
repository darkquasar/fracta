# NotePlan MCP

## What It Is

`noteplan` is the official local NotePlan MCP for notes, tasks, reminders, and calendar-aware planning.

Source: https://help.noteplan.co/article/277-how-to-install-the-noteplan-mcp-server

## Fracta Status

Cataloged but not yet smoke-tested through fracta. macOS only.

## Local-Process Mode

```yaml
noteplan:
  local:
    command: npx
    args: ["-y", "@noteplanco/noteplan-mcp"]
```

Prerequisites:

- macOS
- NotePlan installed and running
- Node.js 18+

## Docker / Kubernetes

Not supported. This depends on the local NotePlan app.
