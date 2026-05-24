# Notion MCP

## What It Is

`notion` is the official hosted Notion MCP server for pages, databases, comments, search, and workspace updates.

Source: https://developers.notion.com/guides/mcp/overview

## Fracta Status

Tested. Reached through fracta's native gateway-managed OAuth.

## Remote Mode (recommended)

```yaml
notion:
  remote:
    url: https://mcp.notion.com/mcp
    transport: streamable-http
    auth:
      type: oauth
      pkce: true
      token_file: /etc/fracta/oauth/notion/token.json
      client_registration_file: /etc/fracta/oauth/notion/client-registration.json
```

Drive the OAuth flow with:

```bash
fracta config mcp auth login notion
```

A browser opens at Notion's consent page; the resulting access + refresh tokens land in the OS keyring under service `fracta.oauth`. Export them for the gateway:

```bash
fracta config mcp auth export notion --format k8s-secret > notion-oauth-secret.yaml
kubectl apply -f notion-oauth-secret.yaml
kubectl rollout restart deploy/fracta-gateway
```

The gateway reads the mounted token file at boot and uses it on every Notion call. See `docs/patterns/reading-garden/setup.mdx` for the full Kubernetes wiring (volumeMounts + volumes) and the corresponding compose-mode mounts.

Static Notion integration tokens (the `secret_...` strings from "My integrations") do **not** authenticate the hosted MCP endpoint — only the OAuth grant above works.

## Local Proxy Mode (fallback)

If you don't have a fracta gateway in front of the MCP — for example, local-process mode driving a hosted server directly — `mcp-remote` is the workable bridge:

```yaml
notion:
  local:
    command: npx
    args: ["-y", "mcp-remote", "https://mcp.notion.com/mcp"]
```

This caches tokens in `~/.mcp-auth/` as plaintext. Treat that directory like an SSH private key, or override `MCP_REMOTE_CONFIG_DIR` on shared machines. Prefer the remote-mode flow above when you have a gateway.
