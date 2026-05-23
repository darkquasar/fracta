# Fracta Test MCP Server

A throw-away streamable-HTTP MCP server used to exercise the gateway's
tool-policy and visibility surfaces. Not for production traffic.

## Tools

| Tool | Returns | Role in policy matrix |
|---|---|---|
| `ping` | `"pong"` | always visible |
| `echo(message)` | `"echo: <message>"` | always visible |
| `forbidden_action` | `"should be blocked by deny policy"` | placed on the policy `deny` list |
| `restricted_action` | `"should be excluded by allow_only policy"` | excluded from `allow_only` |

## Build & Load

The Dockerfile is auto-discovered by the root Makefile. Both targets accept
the standard `K8S_IMAGE_LOADER` / `KIND_CLUSTER` overrides.

```bash
make mcp-build/fracta-fracta-test-server
K8S_IMAGE_LOADER=kind KIND_CLUSTER=fracta-policy \
  make mcp-load/fracta-fracta-test-server
```

The resulting image is `fracta/mcp-fracta-fracta-test-server:latest`.

## Wiring into a fracta deployment

Reference the Service from the `fracta-controlplane-config` ConfigMap so the
gateway's policy/registry pipeline picks it up:

```yaml
mcp_servers:
  servers:
    fracta-test-server:
      remote:
        url: http://fracta-test-server.fracta.svc:8000/mcp
        transport: streamable_http
      tool_policy:
        deny: ["forbidden_action"]
        allow_only: ["ping", "echo", "forbidden_action"]
registry:
  bootstrap_from_config: true
```

After rollout, `fracta debug gateway policy --verbose` should report:

```
+ fracta-test-server/ping
+ fracta-test-server/echo
- fracta-test-server/forbidden_action [denied_by_policy]
- fracta-test-server/restricted_action [denied_by_policy]
```

## Smoke test

```bash
docker run --rm -p 8000:8000 fracta/mcp-fracta-fracta-test-server:latest
# in another shell
curl -s -X POST http://localhost:8000/mcp \
  -H 'content-type: application/json' \
  -H 'accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```
