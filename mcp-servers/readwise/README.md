# Readwise / Reader MCP

## What It Is

`readwise` is the official Readwise and Reader MCP server for highlights, Reader documents, tags, inbox triage, digests, and resurfacing saved reading history.

Source: https://readwise.io/mcp

## Fracta Status

Tested. Reached through fracta's native gateway-managed OAuth.

## Remote Mode (recommended)

```yaml
readwise:
  remote:
    url: https://mcp2.readwise.io/mcp
    transport: streamable-http
    auth:
      type: oauth
      pkce: true
      token_file: /etc/fracta/oauth/readwise/token.json
      client_registration_file: /etc/fracta/oauth/readwise/client-registration.json
```

Drive the OAuth flow with:

```bash
fracta config mcp auth login readwise
```

A browser opens at Readwise's consent page; the resulting access + refresh tokens land in the OS keyring under service `fracta.oauth`. Export them for the gateway:

```bash
fracta config mcp auth export readwise --format k8s-secret > readwise-oauth-secret.yaml
kubectl apply -f readwise-oauth-secret.yaml
kubectl rollout restart deploy/fracta-gateway
```

The gateway reads the mounted token file at boot and uses it on every Readwise call. See `docs/patterns/reading-garden/setup.mdx` for the full Kubernetes wiring (volumeMounts + volumes) and the corresponding compose-mode mounts.

## Local Proxy Mode (fallback)

If you don't have a fracta gateway in front of the MCP — for example, local-process mode driving a hosted server directly — `mcp-remote` is the workable bridge:

```yaml
readwise:
  local:
    command: npx
    args: ["-y", "mcp-remote", "https://mcp2.readwise.io/mcp"]
```

This stores tokens outside fracta, in `~/.mcp-auth/` as plaintext. Prefer the remote-mode flow above when you have a gateway.
