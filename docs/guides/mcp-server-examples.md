---
title: MCP Server Examples
description: Worked configs for OAuth-protected, env-token, and SSE-transport MCP backends — the patterns the scaffolded gateway used to ship inline.
---

This page collects copy-paste-ready configurations for the most common MCP backend shapes. Earlier versions of fracta shipped these as inline dummy entries in the scaffolded gateway ConfigMap; they now live here as reference so operators can pick the pattern that matches their backend and apply it via [`fracta config mcp add`](../reference/cli/config-mcp.md#add-server).

For the broader catalog narrative — when to use which verb, what gets committed where — see [MCP Catalog Workflow](./mcp-catalog.md).

## Where these go

Every example below maps to a single entry under `mcp_servers.servers.<id>` in **two** places:

1. **Project `fracta.yaml`** — written by `fracta config mcp add` and committed to your repo.
2. **Gateway ConfigMap** (`deployment/k8s/manifests/fracta-gateway.yaml` for k8s, or the embedded controlplane config for compose) — the runtime config the gateway pod actually reads.

Today these two locations don't auto-sync; the gateway ConfigMap is the source of truth for the pod. Until the live-sync command lands, copy the snippet into both.

## OAuth-protected backend (Notion pattern)

Backends whose upstream requires OAuth (Notion, Readwise, Google Drive) authenticate via a token issued by the upstream's authorization server. Fracta stores the token + dynamic client registration as a Kubernetes Secret (or local keyring in dev) and mounts them as files the gateway reads at request time.

```yaml
# fracta.yaml — mcp_servers.servers.notion
notion:
  remote:
    url: https://mcp.notion.com/mcp
    transport: streamable-http
    auth:
      type: oauth
      token_file: /run/secrets/fracta-mcp-notion/token.json
      client_registration_file: /run/secrets/fracta-mcp-notion/client-registration.json
```

The two files come from the OAuth flow. Run it once on a host with a browser, then export to a k8s Secret:

```bash
fracta config mcp auth login notion           # opens browser
fracta config mcp auth export notion --format k8s-secret > notion-secret.yaml
kubectl apply -f notion-secret.yaml
```

The Secret must be named `fracta-mcp-notion` and the gateway pod must mount it at `/run/secrets/fracta-mcp-notion`. Add the mount to the gateway Deployment:

```yaml
# deployment/k8s/manifests/fracta-gateway.yaml — gateway container
volumeMounts:
  - name: notion-oauth
    mountPath: /run/secrets/fracta-mcp-notion
    readOnly: true

# ... and under spec.template.spec.volumes:
volumes:
  - name: notion-oauth
    secret:
      secretName: fracta-mcp-notion
```

See [MCP Server Auth](./authentication/mcp-server-auth.md) for the full OAuth flow, refresh semantics, and the per-server token-store layout.

## Env-token backend (Elasticsearch pattern)

Backends that take a bearer token or API key passed via environment variable (Elastic, Bearer-token services) are the simplest case. The token lives in a Kubernetes Secret; the gateway container reads it into the env via `secretKeyRef`.

```yaml
# fracta.yaml — mcp_servers.servers.elastic
elastic:
  remote:
    url: http://elastic-mcp.fracta.svc:3000/mcp
    transport: streamable-http
```

The `auth.env_required` declaration on the catalog `server.yaml` (`ES_URL`, `ES_API_KEY`) drives where the secret references go in the rendered Deployment. After `fracta config mcp add elastic --target-deployment k8s`, the generated `<root>/deployment/k8s/manifests/elastic-mcp-secret.yaml` stub is what you populate:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: elastic-mcp-secrets
  namespace: fracta
type: Opaque
stringData:
  url: https://my-cluster.es.io:9243
  api-key: <your-api-key>
```

And the gateway Deployment picks up:

```yaml
# deployment/k8s/manifests/fracta-gateway.yaml — gateway container
env:
  - name: ELASTIC_URL
    valueFrom:
      secretKeyRef:
        name: elastic-mcp-secrets
        key: url
  - name: ELASTIC_API_KEY
    valueFrom:
      secretKeyRef:
        name: elastic-mcp-secrets
        key: api-key
```

Operators usually rotate these by editing the Secret directly (`kubectl create secret generic ... --dry-run=client -o yaml | kubectl apply -f -`); no fracta-side rotation hook is needed.

## SSE-transport backend (vendor pattern)

Some MCP servers expose their tool surface over Server-Sent Events instead of streamable HTTP. The difference is one field on the remote entry:

```yaml
# fracta.yaml — mcp_servers.servers.vendor
vendor:
  remote:
    url: http://vendor-mcp.fracta.svc:3000/sse
    transport: sse
```

Everything downstream — gateway proxying, tool policy enforcement, agent visibility — works identically for SSE and streamable-HTTP backends. Pick whichever the upstream MCP server speaks.

## Combining patterns

A single project usually mixes all three shapes. Here's a fragment showing them together in a gateway ConfigMap:

```yaml
# deployment/k8s/manifests/fracta-gateway.yaml — data.fracta-k8s.yaml
mcp_servers:
  servers:
    elastic:
      remote:
        url: http://elastic-mcp.fracta.svc:3000/mcp
        transport: streamable-http
    vendor:
      remote:
        url: http://vendor-mcp.fracta.svc:3000/sse
        transport: sse
    notion:
      remote:
        url: https://mcp.notion.com/mcp
        transport: streamable-http
        auth:
          type: oauth
          token_file: /run/secrets/fracta-mcp-notion/token.json
          client_registration_file: /run/secrets/fracta-mcp-notion/client-registration.json
```

Add a `tool_policy:` block under any server to restrict which tools agents can see and call — see [Gateway Tool Policy](./gateway-tool-policy.md).

## Local / docker-compose variants

The same patterns apply in the other deployment modes — only the URL changes.

| Mode | Endpoint format | Example |
|---|---|---|
| `local` | stdio subprocess; no URL | `command: npx`, `args: [-y, mcp-remote, https://...]` |
| `docker-compose` | Compose service DNS | `http://elastic-mcp:3000/mcp` |
| `k8s` | Service FQDN | `http://elastic-mcp.fracta.svc:3000/mcp` |

The `fracta config mcp add <server> --target-deployment <mode>` flow generates the right shape for each mode automatically. These examples document what the rendered output looks like so operators can read or hand-tune it.

## What's next

- [`fracta config mcp` reference](../reference/cli/config-mcp.md) — every flag, every subcommand.
- [Gateway Tool Policy](./gateway-tool-policy.md) — restrict which tools agents see per backend.
- [MCP Server Auth](./authentication/mcp-server-auth.md) — OAuth flow, token storage, exporting to secrets.
- [MCP Catalog Workflow](./mcp-catalog.md) — the operator narrative for catalog management.
