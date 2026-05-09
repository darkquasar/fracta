---
title: Development Overview
description: Building, testing, and releasing fracta from source
---

# Development

This section covers everything you need to work on fracta itself: building the binary, running the test suite, the CI pipeline that runs on every push, and the release process for cutting tagged versions.

If you're looking to **use** fracta rather than develop it, start with [Getting Started](/getting-started/installation) instead.

## Audience

- **Casual contributors** who want to build from source and verify changes locally before opening a PR.
- **Maintainers** who cut releases, manage CI, and own the build/test infrastructure.
- **Operators** who need reproducible builds for internal deployment.

## Pages in this section

| Page | What it covers |
|---|---|
| [Building from source](/development/building) | Local build, the `make` targets, Go version requirements, embedding a version string |
| [CI and tests](/development/ci-and-tests) | The `ci.yml` workflow, what runs on every push and PR, how to reproduce CI failures locally |
| [Releasing](/development/releasing) | The `release.yml` workflow, semver tag conventions, multi-arch binary + Docker image artifacts |

## Repository layout (relevant to development)

```
fracta/
├── main.go                 # Binary entry point — sets version, calls cmd.Execute()
├── cmd/                    # Cobra command definitions (root, spawn, list, serve, etc.)
├── internal/               # Implementation packages (controlplane, gateway, mcpserver, ...)
├── strategies/             # Python strategy runner + sample strategies
├── deployment/             # Docker Compose, K8s manifests, MCP server bundles
├── docs/                   # Mintlify documentation site (you're reading this)
├── Dockerfile              # Two-stage build: Go binary + Python sidecar runtime
├── Makefile                # `make build`, `make test`, `make docker-build`, etc.
├── go.mod                  # Go module — declares minimum Go version (1.25+)
└── .github/workflows/      # ci.yml (per-push) and release.yml (per-tag)
```

## Quick start

```bash
git clone https://github.com/darkquasar/fracta.git
cd fracta

# Build the binary
make build               # → bin/fracta

# Run the test suite
make test                # → go test ./...

# Run a specific package's tests
go test ./internal/orchestrator/... -count=1 -race
```

For deeper details on each step, follow the pages linked above.

