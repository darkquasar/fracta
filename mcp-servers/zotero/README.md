# Zotero MCP

## What It Is

`zotero` is a research-library MCP entry for Zotero papers, metadata, citations, PDFs, and annotations.

Current candidate source: https://github.com/kujenga/zotero-mcp

## Fracta Status

Candidate. Zotero has several community MCPs; this catalog entry should be promoted only after fracta smoke-tests one preferred server.

## Local-Process Mode

```yaml
zotero:
  local:
    command: uvx
    args: ["zotero-mcp"]
    env:
      ZOTERO_LOCAL: "true"
      ZOTERO_API_KEY: "${ZOTERO_API_KEY}"
      ZOTERO_LIBRARY_ID: "${ZOTERO_LIBRARY_ID}"
```

Use local-library mode when possible. Use API credentials for cloud/group libraries.

## Docker / Kubernetes

Possible with API credentials, but not catalog-tested yet.
