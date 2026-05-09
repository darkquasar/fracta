---
title: Contributing
description: How to work on fracta itself
---

# Contributing

## Project layout

| Directory | Purpose |
|---|---|
| `cmd/` | CLI commands (cobra) |
| `internal/` | All implementation packages |
| `deployment/` | Docker Compose, Kubernetes, and local-process recipes |
| `strategies/` | Python DAG strategy runner |
| `graph-schema/` | Knowledge graph node and edge schema |
| `scripts/` | Helper scripts |
| `docs/` | This documentation |

## Build

```bash
go build -o bin/fracta .
```

Or use the Makefile:

```bash
make build
```

## Test

```bash
go test ./...
```

{/* TODO: integration test setup, fixtures, when to mock vs hit real services */}

## Code conventions

### Logging

All logging goes through `internal/fractalog`:

```go
log := fractalog.Component("orchestrator")
log.Info("agent spawned", "task", task)
```

Never use bare `slog.Info` or `slog.Default().With(...)` — the handler is captured at call time, before config-driven `AttachFile` runs.

{/* TODO: error wrapping convention, naming, package boundaries */}

## Adding a feature

Fracta uses spec-first development. Feature specs live in a peer repo at `../fracta-specs/`. The pattern:

1. Open or claim a spec number under `fracta-specs/N-feature-name/`.
2. Write `spec.md` (the design) and `plan.md` (the execution).
3. Get the spec reviewed.
4. Implement against the plan.

{/* TODO: link to the spec template */}

## Pull requests

{/* TODO: branch naming, commit message style, review process */}

## What's next

<Card title="Adding a Runtime" href="/contributing/adding-runtime">
  How to onboard a new LLM CLI as a fracta host runtime.
</Card>

<Card title="Changelog" href="/contributing/changelog">
  Notable changes to fracta.
</Card>
