# Docker Compose Deployment

> **New to fracta?** Start with the [Docker Compose Quickstart](../../docs/quickstart-docker-compose.md) for a step-by-step guide, or the [Getting Started Guide](../../docs/getting-started.md) for an overview of all deployment modes.

Docker Compose mode runs the full fracta stack in containers: control plane, gateway, strategy runner, MCP backends (Elastic, Vendor), Postgres, and FalkorDB.

## Quick Start

```bash
make docker-build          # build the fracta image
make compose-up-op         # start all services with 1Password secrets
bin/fracta serve --config deployment/docker-compose/client/fracta.yaml   # connect thin client
bin/fracta spawn --config deployment/docker-compose/client/fracta.yaml --task my-agent --contract "do something"
```

## Secrets

MCP backend containers need API credentials to connect to Elastic and VendorSecurity. These are injected from 1Password using the same `.op-env` file as local-process mode:

```bash
# make compose-up-op is equivalent to:
op run --env-file .op-env -- docker compose -f deployment/docker-compose/docker-compose.yml up -d
```

`op run` resolves `op://` references into real environment variables on the host process. Docker Compose inherits them and interpolates `${VAR}` in the YAML to pass values into containers. No secrets touch disk.

Required variables (defined in `.op-env`):

| Variable | Used by | Source |
|---|---|---|
| `ELASTIC_URL` | elastic-mcp | Plaintext URL |
| `ELASTIC_API_KEY` | elastic-mcp | 1Password reference |
| `VENDOR_MCP_CONSOLE_BASE_URL` | vendor-mcp | Plaintext URL |
| `VENDOR_MCP_CONSOLE_TOKEN` | vendor-mcp | 1Password reference |

Without secrets (`make compose-up`), core services start but MCP backends won't connect — agents will only have graph/strategy tools, not Elastic or Vendor.

Any secret injector that sets env vars works: `doppler run --`, `vault exec --`, etc.

## Services

| Service | Port (host) | Purpose |
|---|---|---|
| controlplane | 19090 | Lifecycle authority, HTTP API, queue workers |
| gateway | 8080 | MCP proxy, graph tools, strategy engine |
| strategy-runner | — | Python strategy DAG engine (shared socket) |
| elastic-mcp | — | Elasticsearch MCP server |
| vendor-mcp | — | VendorSecurity Generic MCP server |
| postgres | 5432 | State store and mission queue |
| falkordb | 16379 | Knowledge graph |

## Files

| File | Purpose |
|---|---|
| `docker-compose.yml` | Compose stack definition (7 services). |
| `configs/controlplane.yaml` | Server-side control plane config mounted into the controlplane container. |
| `configs/gateway.yaml` | Server-side gateway config mounted into the gateway container. |
| `client/fracta.yaml` | Host-side thin-client config for `bin/fracta serve` and `bin/fracta spawn`. |

## Commands

```bash
make compose-up-op         # start with 1Password secrets (full MCP backends)
make compose-up            # start without secrets (core services only)
make compose-down          # stop and remove containers
make compose-logs          # tail all container logs
make compose-ps            # show container status
```

Direct Docker Compose commands should pass the compose file explicitly:

```bash
docker compose -f deployment/docker-compose/docker-compose.yml up -d
docker compose -f deployment/docker-compose/docker-compose.yml down -v   # also remove volumes
```
