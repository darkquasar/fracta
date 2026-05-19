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
| `internal/project/scaffolds/templates/{local,docker-compose,k8s}/` | Embedded scaffold sources for `fracta init --scaffold <mode>` — the files materialized into operator projects |
| `mcp-servers/` | Canonical MCP server catalog (fetched by `fracta config mcp fetch`; spec-43) |
| `strategies/` | Python DAG strategy runner |
| `graph-schema/` | Knowledge graph node and edge schema |
| `scripts/` | Helper scripts |
| `docs/` | This documentation |

The old `deployment/` directory was removed in spec-43 Concern L. Its contents split two ways: the operator-facing scaffolds (Compose stack, K8s manifests, auth-helper templates) moved into the embedded templates at `internal/project/scaffolds/templates/`, and the MCP server catalog moved to `mcp-servers/` at the repo root. Editing a scaffold template changes what `fracta init` writes; editing `mcp-servers/<id>/server.yaml` changes what `fracta config mcp fetch` distributes.

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

Most tests are unit tests that run with no external services. Integration tests that need FalkorDB or Postgres are gated on environment variables and skip when those aren't set — CI runs them as skips. To run them locally, start the service and export the env var:

```bash
docker run --rm -d -p 6379:6379 --name falkordb falkordb/falkordb:latest
FALKORDB_ADDR=localhost:6379 go test ./internal/graph -run TestIntegration -v
```

The full breakdown of test categories (unit, integration, golden, wire-protocol) and conventions (where fixtures live, how `testdata/` is organized, when to use `t.TempDir()`) lives in [CI and tests](/development/ci-and-tests#test-categories). Default to writing unit tests that exercise pure logic; reach for integration only when the behavior under test is the integration itself.

## Code conventions

### Logging

All logging goes through `internal/fractalog`:

```go
log := fractalog.Component("orchestrator")
log.Info("agent spawned", "task", task)
```

Never use bare `slog.Info` or `slog.Default().With(...)` — the handler is captured at call time, before config-driven `AttachFile` runs.

### Errors

Wrap with `%w` and a package-scoped prefix so callers can `errors.Is` / `errors.As` upward and tracebacks read as a chain:

```go
return nil, fmt.Errorf("mcpcatalog: load local catalog: %w", err)
```

Conventions:

- **Prefix with the package name**, lowercase, followed by what the function was attempting. Reads top-down when nested: `cmd: config mcp add: mcpcatalog: load local catalog: open <path>: ...`.
- **Always `%w`, never `%v`** when wrapping. `%v` flattens the chain and breaks `errors.Is`.
- **Standard library only.** No `pkg/errors`, no `errors.Wrap`. The standard `errors` package and `fmt.Errorf` cover everything.
- **Sentinel errors for callers to match on.** Declare as package-level `var ErrFoo = errors.New("mcpcatalog: foo")`. Callers use `errors.Is(err, mcpcatalog.ErrFoo)`.

### Package boundaries

`internal/` is the implementation; only `cmd/` and `main.go` may import the world. Within `internal/`, packages depend downward (orchestrator → host adapters → graph / state stores), never upward. If you find yourself wanting to import `internal/orchestrator` from a lower-level package, the type wants to live one level up.

## Adding a feature

Fracta uses spec-first development. Feature specs live in a peer repo at `../fracta-specs/`. The pattern:

1. **Open or claim a spec number** under `fracta-specs/N-feature-name/` (kebab-case feature name). Numbers are sequential — check the directory listing for the next free integer. Completed specs are renamed `done-N-feature-name/` once their implementation ships.
2. **Write the spec set:**
   - `research.md` — gap analysis, prior art, constraints. Optional for tiny specs.
   - `spec.md` — the design contract: goals, non-goals, architecture rules, the full CLI/config surface, breaking changes, risks.
   - `plan.md` — execution sequencing: phases, concerns (lettered A, B, C…), dependencies.
   - `tasks.md` — checkbox-style work breakdown referencing the plan's concerns (e.g. `J6 — implement add`).
3. **Get the spec reviewed.** The review gate is the spec document; coding starts after spec consensus, not before.
4. **Implement against the plan.** Commit messages reference the concern (`cli: implement 'fracta config mcp add' (J6)`).

Look at any `done-*` directory under `fracta-specs/` for a worked example — `done-43-config-mcp-server-management/` is recent and exercises the full pattern.

## Pull requests

### Branch naming

No enforced convention. Use whatever helps you. The author's preference is a short kebab-case slug (`mcp-catalog-fetch`, `fix-codex-race`); spec-driven work often uses the concern key (`spec-43-j6-add`).

### Commit messages

Short, imperative, and prefixed with the area of the codebase touched. Look at `git log` for the style — recent examples:

```
cli: implement 'fracta config mcp fetch' (J2)
mcpcatalog: rename render.go modeKey→supportKey to avoid collision
docs: sweep spec-42/43 staleness, add Install + MCP catalog guide
merge: spec-43 cli + catalog-core (J1-J7, K1-K3, E1-G1)
```

Conventions:

- **`<area>: <imperative summary>`** in the subject line. Area is usually the package name or a recognized short tag (`cli`, `docs`, `merge`).
- **No conventional-commits prefixes** (`feat:` / `fix:` / etc.). Domain prefixes are clearer for this codebase.
- **Reference the concern** when working from a spec (`(J6)`, `(K1-K3)`). Future-you reading `git log` wants the link back to the plan.
- **Body for the "why."** Subject is the "what." Use the body for context that wouldn't be obvious from the diff — design trade-offs, why this approach over an alternative, what won't work and why.

### Review process

PRs are reviewed before merge. Aim for a small, single-purpose PR — easier to review, easier to revert if something regresses. If a feature naturally spans many concerns (spec-43 had 13), split into a series of PRs that build on each other (`merge:` commits chain them on `main`).

CI must be green before merge. The pipeline is described in [CI and tests](/development/ci-and-tests). If CI fails for a flake (not a real regression), re-run the failed jobs — don't force-merge.

## What's next

<Card title="Adding a Runtime" href="/contributing/adding-runtime">
  How to onboard a new LLM CLI as a fracta host runtime.
</Card>

<Card title="Changelog" href="/contributing/changelog">
  Notable changes to fracta.
</Card>
