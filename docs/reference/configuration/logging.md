---
title: Logging
description: Structured logging configuration and component registry
---

# Logging

## Overview

Fracta uses Go's `log/slog` for structured JSON logging, wrapped by the `internal/fractalog` package. All log output includes a `"component"` tag identifying the subsystem that emitted it.

Fracta also has an internal event bus for structured lifecycle and infrastructure events. The event bus does **not** replace `slog` or `fractalog`; it uses them through `LogSink`. See [docs/event-bus.md](/reference/events) for the event-bus architecture and how it relates to logging.

## Configuration

Add a `logging` section to `fracta.yaml`:

```yaml
logging:
  file: fracta.log   # log file path
  level: info       # debug | info | warn | error
```

**Path resolution:**
- Absolute path (e.g., `/var/log/fracta.log`) — used as-is
- Relative path or bare filename (e.g., `fracta.log`, `logs/fracta.log`) — resolved from the working directory where the `fracta` binary runs

**Level precedence:** `logging.level` in config > `FRACTA_LOG_LEVEL` env var > default (`info`)

When `logging.file` is set, logs go to **both** stderr and the file. When omitted, logs go to stderr only (original behavior).

### Full fracta.yaml example

```yaml
# fracta.yaml — logging in context with other config sections

logging:
  file: .fracta/fracta.log       # logs alongside state.db in .fracta/
  level: info                 # debug | info | warn | error

connections:
  falkordb:
    type: redis
    url: ${FALKORDB_URL}
    graph_name: fracta_knowledge
  elastic_main:
    type: elasticsearch
    url: ${ELASTIC_URL}
    api_key: ${ELASTIC_API_KEY}

runtime:
  backend: local
  state:
    driver: sqlite
    sqlite:
      path: .fracta/state.db

project:
  default_base_branch: main

agents:
  default_host_type: claude
  default_mode: batch

hosts:
  claude:
    adapter: claude
    model_tiers:
      heavy: opus
      medium: sonnet
      light: haiku

ontology:
  schemas:
    - path: graph-schema/core
    - path: graph-schema/threat-hunting

mcp_servers:
  servers:
    elastic:
      local:
        command: podman
        args: ["run", "-i", "--rm", "docker.elastic.co/mcp/elasticsearch", "stdio"]
        env:
          ES_URL: "${ELASTIC_URL}"
          ES_API_KEY: "${ELASTIC_API_KEY}"

strategy:
  dir: strategies
  pool_size: 2
```

## Architecture

```
fractalog.Init()         ← called once at startup (cmd/root.go init)
                         sets slog.Default() → JSON handler → stderr

fractalog.AttachFile()   ← called after config parse (cmd/serve.go, cmd/worker.go)
                         replaces slog.Default() → JSON handler → MultiWriter(stderr, file)

fractalog.Component(name) ← called by every subsystem
                           returns *slog.Logger with "component" = name
```

## Event Bus Interaction

Logging architecture is still based on `slog` + `fractalog`.

What changed with spec-28 is the path for event-style observability:

- bus emitters send `events.Event` through `events.Bus`
- `LogSink` writes the event as a normal structured log line under component `events`
- `StoreSink` persists the event without logging again
- `K8sEventSink` may also export selected events to Kubernetes Events in K8s mode

So:

- ordinary application logs still use `fractalog.Component("name")` directly
- bus events become structured log lines through `LogSink`
- logging configuration and output destinations are unchanged

## Usage

### Struct with a logger field

```go
import "github.com/darkquasar/fracta/internal/fractalog"

type Gateway struct {
    logger *slog.Logger
}

func New() *Gateway {
    return &Gateway{
        logger: fractalog.Component("gateway"),
    }
}

func (g *Gateway) DoSomething() {
    g.logger.Info("something happened", "key", "value")
}
```

### Standalone function

```go
func reconcileOrphans(ctx context.Context) {
    log := fractalog.Component("serve")
    log.Info("reconciling", "count", n)
}
```

### Rules

1. **Always use `fractalog.Component("name")`** — never bare `slog.Info()` or `slog.Default().With("component", ...)`.
2. **Never use package-level `var log = fractalog.Component(...)`** — the handler is captured at call time, which is before `AttachFile` runs. Always create loggers inside functions.
3. **Component names** should match the package or subsystem name (e.g., `"gateway"`, `"reconciler"`, `"serve"`, `"worker"`).

## Component Registry

| Component | Package |
|---|---|
| `serve` | `cmd/serve.go` |
| `worker` | `cmd/worker.go` |
| `controlplane` | `internal/controlplane` |
| `reaper` | `internal/controlplane` |
| `signal-handler` | `internal/controlplane` |
| `admission` | `internal/admission` |
| `orchestrator` | `internal/orchestrator` |
| `worker` | `internal/worker` |
| `pgqueue` | `internal/queue/postgres` |
| `mcpclient` | `internal/mcpclient` |
| `mcpserver` | `internal/mcpserver` |
| `strategy` | `internal/mcpserver` (strategy tools) |
| `gateway` | `internal/gateway` |
| `graph` | `internal/graph/falkordb` |
| `rag` | `internal/graph/rag` |
| `reconciler` | `internal/registry` |
| `sidecar` | `internal/strategy` |
| `mailbox` | `internal/state/sqlitestore` |
| `config` | `internal/config` |
| `loaders` | `internal/loaders` |

## Log Format

All output is JSON (one object per line):

```json
{"time":"2026-03-30T12:00:00Z","level":"INFO","msg":"registered server","component":"gateway","server":"vendor_mcp","tools":12}
```

## Filtering Examples

```bash
# All gateway logs
jq 'select(.component == "gateway")' fracta.log

# Errors only
jq 'select(.level == "ERROR")' fracta.log

# Strategy warnings
jq 'select(.component == "strategy" and .level == "WARN")' fracta.log
```
