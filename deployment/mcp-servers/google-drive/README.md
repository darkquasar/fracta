# Google Drive / Docs MCP

## What It Is

`google-drive` is a corpus connector for Google Drive and Google Workspace files such as Docs, Sheets, Slides, PDFs, and shared folders.

Reference source: https://github.com/modelcontextprotocol/servers

## Fracta Status

Candidate. Useful for knowledge bases stored in shared docs, but the setup depends on Google OAuth credentials and token persistence.

## Local-Process Mode

```yaml
google-drive:
  local:
    command: npx
    args: ["-y", "@modelcontextprotocol/server-gdrive"]
```

This requires the Google Drive MCP server's OAuth setup before launch.

## Docker / Kubernetes

Blocked until fracta has a clear pattern for mounting Google OAuth credentials or owning MCP OAuth/token storage.
