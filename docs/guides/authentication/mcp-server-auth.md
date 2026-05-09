---
title: MCP Server Authentication
description: Bearer, header, basic, and OAuth flows for remote MCP servers
---

# MCP Server Authentication

Fracta supports authenticated remote MCP servers through the `mcp_servers.*.remote.auth` block in `fracta.yaml`.

This document covers authentication for MCP backend servers such as Notion, Raindrop, Elasticsearch, and internal HTTP MCP services. It is separate from LLM runtime authentication for Claude, Codex, or OpenCode. For runtime credentials, see [credential-pipeline.md](/guides/authentication/credential-pipeline).

For the catalog of known MCP servers, see [deployment/mcp-servers/README.md](https://github.com/darkquasar/fracta/blob/main/deployment/mcp-servers/README.md).

## Ownership Model

Remote MCP authentication is owned by the gateway-side MCP client pool:

```text
agent runtime ->  fracta gateway MCP -> remote MCP server -> upstream API
```

Agents do not receive MCP backend secrets directly. The gateway builds the remote transport, resolves configured secrets, attaches headers or OAuth token stores, and proxies tools to agents.

## Auth Types

| Type | Use when | Credential shape |
|---|---|---|
| `none` | The remote MCP server is internal or unauthenticated | No auth block required |
| `bearer` | The server accepts `Authorization: Bearer <token>` | `token` as a `SecretValue` |
| `header` | The server expects an API key or custom header | `header_name` plus `header_value` |
| `basic` | Legacy HTTP Basic auth | `username` and `password` |
| `oauth` | Hosted MCP OAuth, mounted OAuth tokens, or client credentials | OAuth fields below |

The `auth` field is intentionally symmetric: non-OAuth and OAuth credentials all live in the same per-server `remote.auth` block.

## Secret Values

Secret fields use the same `SecretValue` shape everywhere. Exactly one source must be set:

```yaml
token:
  value: "literal-string"   # development only
  env: "ENV_VAR_NAME"       # read from the process environment
  file: "/path/to/file"     # read from a mounted file; trailing newline stripped
```

Supported fields include `token`, `header_value`, `username`, `password`, `client_id`, `client_secret`, `access_token`, and `refresh_token`.

## Non-OAuth Examples

Bearer token:

```yaml
mcp_servers:
  servers:
    raindrop:
      remote:
        url: https://mcp.raindrop.io/mcp
        transport: streamable-http
        auth:
          type: bearer
          token:
            env: RAINDROP_TOKEN
```

Custom header:

```yaml
mcp_servers:
  servers:
    custom:
      remote:
        url: https://mcp.example.com/mcp
        transport: streamable-http
        auth:
          type: header
          header_name: X-API-Key
          header_value:
            file: /run/secrets/custom-api-key
```

Basic auth:

```yaml
mcp_servers:
  servers:
    legacy:
      remote:
        url: https://mcp.example.com/sse
        transport: sse
        auth:
          type: basic
          username:
            value: admin
          password:
            env: LEGACY_PASSWORD
```

## OAuth Cycle

OAuth has two phases: interactive authorization and runtime consumption.

```text
local machine                         keyring                     deployment
--------------                        -------                     ----------
fracta mcp login <server>
  reads fracta.yaml
  discovers OAuth metadata
  dynamically registers client if needed
  opens browser or device flow
  exchanges code for token       ->    server:token
                                      server:client

fracta mcp export <server>       <-     reads stored token
  writes env, files, or K8s Secret

gateway starts in Compose/K8s
  reads token_file or env values
  builds OAuth transport
  refreshes in memory during process lifetime
```

### 1. Configure the OAuth server

For hosted MCP OAuth with browser login:

```yaml
mcp_servers:
  servers:
    notion:
      remote:
        url: https://mcp.notion.com/mcp
        transport: streamable-http
        auth:
          type: oauth
          scopes: []
```

`remote.url` is used for MCP protected-resource discovery. `metadata_url`, when set, must point to OAuth authorization server metadata, not protected-resource metadata.

If the server supports dynamic client registration, `client_id` can be omitted. If it does not, provide a client identity:

```yaml
auth:
  type: oauth
  client_id:
    env: NOTION_CLIENT_ID
  client_secret:
    env: NOTION_CLIENT_SECRET
```

### 2. Login locally

Run login from a machine that can open a browser:

```bash
bin/fracta --config fracta.yaml mcp login notion
```

The login command:

1. Reads the named MCP server from config.
2. Starts the local callback server before opening the browser.
3. Discovers OAuth metadata from `remote.url` and optional `metadata_url`.
4. Dynamically registers a client if no client identity is configured or stored.
5. Uses authorization code flow with PKCE S256 by default.
6. Exchanges the callback code for a token.
7. Stores the token and dynamic client registration in the local OS keyring.

Device code flow is available for servers that support it:

```bash
bin/fracta --config fracta.yaml mcp login notion --device-code
```

Device code flow requires a configured `client_id`.

### 3. Inspect or clear local credentials

```bash
bin/fracta --config fracta.yaml mcp auth-status
bin/fracta --config fracta.yaml mcp auth-status notion
bin/fracta --config fracta.yaml mcp logout notion
```

Local OAuth credentials use the configured `token_store`. The default is:

```yaml
token_store:
  driver: auto
```

`auto` and `keyring` both use the OS keyring through `zalando/go-keyring`.

Stored items use:

```text
service: fracta.oauth
account: <server>:token
account: <server>:client
```

The `file` token store driver is not implemented. For headless deployments, export from the local keyring and mount the exported files or K8s Secret.

### 4. Export for headless deployments

Export formats:

```bash
bin/fracta --config fracta.yaml mcp export notion --format env
bin/fracta --config fracta.yaml mcp export notion --format files --output-dir ./secrets/mcp
bin/fracta --config fracta.yaml mcp export notion --format k8s-secret > /tmp/fracta-mcp-notion.yaml
```

`env` emits variables such as:

```text
FRACTA_MCP_NOTION_ACCESS_TOKEN=...
FRACTA_MCP_NOTION_REFRESH_TOKEN=...
FRACTA_MCP_NOTION_CLIENT_ID=...
FRACTA_MCP_NOTION_CLIENT_SECRET=...
```

`files` writes:

```text
<output-dir>/notion-token.json
<output-dir>/notion-client-registration.json
```

`k8s-secret` emits a Secret named `fracta-mcp-<server>` with keys:

```text
token.json
client-registration.json
```

`client-registration.json` is only emitted when  fracta has a stored dynamic client registration.

## Runtime OAuth Config

### Local-process gateway

For local development, the gateway can read directly from the local keyring:

```yaml
mcp_servers:
  servers:
    notion:
      remote:
        url: https://mcp.notion.com/mcp
        transport: streamable-http
        auth:
          type: oauth
```

Run `fracta mcp login notion` once before starting the gateway. The transport token store reads and updates the OS keyring.

### Kubernetes gateway

Use `k8s-secret` export and mount the Secret into the gateway pod:

```yaml
mcp_servers:
  servers:
    notion:
      remote:
        url: https://mcp.notion.com/mcp
        transport: streamable-http
        auth:
          type: oauth
          token_file: /run/secrets/fracta-mcp-notion/token.json
          client_registration_file: /run/secrets/fracta-mcp-notion/client-registration.json
```

Mount example:

```yaml
volumeMounts:
  - name: notion-oauth
    mountPath: /run/secrets/fracta-mcp-notion
    readOnly: true

volumes:
  - name: notion-oauth
    secret:
      secretName: fracta-mcp-notion
```

If the exported Secret does not contain `client-registration.json`, omit `client_registration_file`.

### Docker Compose gateway

Use `files` export and Compose secrets:

```bash
bin/fracta --config fracta.yaml mcp export notion --format files --output-dir ./secrets/mcp
```

Compose example:

```yaml
services:
  fracta-gateway:
    secrets:
      - source: notion_token
        target: notion-token.json
      - source: notion_client_registration
        target: notion-client-registration.json

secrets:
  notion_token:
    file: ./secrets/mcp/notion-token.json
  notion_client_registration:
    file: ./secrets/mcp/notion-client-registration.json
```

Gateway config:

```yaml
mcp_servers:
  servers:
    notion:
      remote:
        url: https://mcp.notion.com/mcp
        transport: streamable-http
        auth:
          type: oauth
          token_file: /run/secrets/notion-token.json
          client_registration_file: /run/secrets/notion-client-registration.json
```

If there is no client registration file, omit `client_registration_file`.

### Inline pre-authorized tokens

Inline token fields are useful when another system injects credentials as env vars or mounted files:

```yaml
mcp_servers:
  servers:
    notion:
      remote:
        url: https://mcp.notion.com/mcp
        transport: streamable-http
        auth:
          type: oauth
          access_token:
            env: FRACTA_MCP_NOTION_ACCESS_TOKEN
          refresh_token:
            env: FRACTA_MCP_NOTION_REFRESH_TOKEN
```

This path does not preserve full token metadata such as `expires_at`. Prefer `token_file` when you have the JSON exported by fracta.

## Client Credentials Grant

Use `client_credentials` for service-to-service MCP servers that do not need a user browser session:

```yaml
mcp_servers:
  servers:
    internal:
      remote:
        url: https://mcp.internal.example/mcp
        transport: streamable-http
        auth:
          type: oauth
          grant_type: client_credentials
          client_id:
            env: INTERNAL_MCP_CLIENT_ID
          client_secret:
            file: /run/secrets/internal-mcp-client-secret
          scopes:
            - tools.read
```

The token endpoint is discovered from `metadata_url` if set, otherwise from `remote.url` using authorization server discovery.

Client credentials tokens are fetched when the transport is created and attached as a bearer token. They are not stored in the local keyring.

## Token Refresh And Restart Behavior

For local keyring-backed OAuth, refreshed tokens are saved back to the keyring by the OAuth token store.

For `token_file`, mounted files are read-only bootstrap inputs. The gateway can refresh tokens in memory during its process lifetime, but refreshed tokens are not written back to the mounted file. On restart, the gateway re-reads the original file. If the provider rotates refresh tokens, periodically re-run:

```bash
bin/fracta --config fracta.yaml mcp login <server>
bin/fracta --config fracta.yaml mcp export <server> --format k8s-secret
```

then update the deployment Secret.

## Validation Rules And Gotchas

- `auth.type` must be one of `none`, `bearer`, `header`, `basic`, or `oauth`.
- `SecretValue` requires exactly one of `value`, `env`, or `file`.
- `remote.headers.Authorization` cannot be combined with auth types that generate `Authorization`.
- `auth.type: header` rejects case-insensitive collisions with existing `remote.headers`.
- OAuth `grant_type` must be `authorization_code`, `client_credentials`, or `device_code`.
- `client_credentials` requires `client_id` and `client_secret`.
- `access_token` and `token_file` are mutually exclusive.
- `client_registration_file` is a hard requirement if configured. Remove it when the exported credentials do not include that file.
- The gateway pod or container must have outbound network access to the hosted MCP server and OAuth issuer.
- OAuth scope step-up on `403 insufficient_scope` is not implemented yet. Re-login with the required scopes if a provider rejects the token.
