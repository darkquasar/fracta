---
title: Building from source
description: Local builds, Makefile targets, version stamping
---

# Building from source

Fracta is a single Go binary plus a Python strategy-runner sidecar. The Go binary is the only thing you need to build for most development work — the Python sidecar runs separately and only matters when you're testing strategy execution.

## Prerequisites

| Tool | Minimum | Notes |
|---|---|---|
| Go | 1.25.x | Declared in `go.mod`. Newer is fine; older won't work. |
| Make | any modern version | Targets are POSIX-compatible bash. |
| Docker | 20+ | Only required for `make docker-build` and the Compose/K8s deployment modes. |
| `uv` | latest | Only required if you're working on Python strategies. Install with `brew install uv` or see [astral.sh/uv](https://docs.astral.sh/uv/). |

Check your Go version:

```bash
go version
# go version go1.25.5 darwin/arm64   ← matches go.mod
```

## Build the binary

The canonical build command:

```bash
make build
```

This is equivalent to:

```bash
go build -o bin/fracta .
```

Output lands at `bin/fracta`. From the repo root, you can run it directly:

```bash
./bin/fracta --help
./bin/fracta --version    # → "fracta version dev"
```

For a system-wide install:

```bash
make install
```

(Currently the same as `make build`. A future iteration may copy to `$GOPATH/bin` or `/usr/local/bin`.)

## Running the binary in development

Most development happens against the **local-process** deployment mode (no Docker, no K8s). The fastest loop:

```bash
# 1. Build
make build

# 2. Symlink the local-process Claude config to the repo root
ln -sf deployment/local-process/runtimes/claude/.mcp.json .mcp.json

# 3. From a separate fracta-managed directory, run `bin/fracta init` to bootstrap state
cd /path/to/your/fracta-workspace
/path/to/fracta/bin/fracta init

# 4. Use Claude Code (or `bin/fracta spawn`) to drive the orchestrator
```

For full end-to-end setup, see the [Local Process Quickstart](/guides/deployment/local-process).

## Version stamping

The binary embeds a version string accessible via `--version`. By default it's `"dev"`. To stamp a specific version:

```bash
go build -ldflags "-X main.version=v0.2.0" -o bin/fracta .
./bin/fracta --version
# → "fracta version v0.2.0"
```

The `main.version` variable is defined in [`main.go`](https://github.com/darkquasar/fracta/blob/main/main.go) and passed to the Cobra root command via `cmd.SetVersion(...)`. The release workflow (see [Releasing](/development/releasing)) injects the git tag here automatically when building tagged versions.

## Cross-compilation

Go cross-compiles natively. To produce a Linux ARM64 binary from a Mac:

```bash
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go build -trimpath -o bin/fracta-linux-arm64 .
```

The `-trimpath` flag is recommended for distributable binaries — it strips local filesystem paths from the binary, making builds reproducible across machines and avoiding leaking developer paths.

`CGO_ENABLED=0` produces a fully static binary (no glibc dependency) suitable for distroless and scratch base images. The release workflow uses this combination for all four platform binaries.

## Docker build

The Dockerfile is a two-stage build:

1. **Stage 1**: Go module compiled to `/out/fracta`
2. **Stage 2**: `node:20-slim` base, installs the Claude / Codex / OpenCode CLIs via npm, installs `uv` for Python deps, copies the strategy directory, sets up the entrypoint script

To build locally:

```bash
make docker-build       # → fracta:latest
```

To verify it runs:

```bash
docker run --rm fracta:latest fracta --version
```

For multi-arch builds (used by the release workflow for ghcr.io publishing), see [Releasing](/development/releasing).

## Build artifacts

| Artifact | Produced by | Used by |
|---|---|---|
| `bin/fracta` | `make build` | Local development, manual testing |
| `fracta:latest` (Docker) | `make docker-build` | Compose mode (`make compose-up-op`) |
| `fracta/vendor-mcp:latest` (Docker) | `make vendor-mcp-build` | K8s mode (vendor MCP backend pod) |
| `bin/fracta` (in-pod) | Stage 1 of Dockerfile | K8s agent pods, Compose containers |

`bin/` is gitignored — never committed. The release workflow produces dist artifacts attached to GitHub Releases (see [Releasing](/development/releasing)).

## Common build issues

**`undefined: someSymbol` after pulling**
Run `go mod download` to ensure dependencies match `go.sum`.

**`package X is not in std` errors**
Likely a Go version mismatch. Check `go version` against the version in `go.mod`.

**`make: *** missing separator`**
Tabs vs. spaces in the Makefile. The repo uses tabs; some editors silently convert. Reset with `git checkout Makefile` and re-edit carefully.

**Slow `go test ./...`**
The first run rebuilds all dependencies; subsequent runs are fast (Go caches packages). To force a clean rebuild: `go clean -testcache && go test ./...`. To bypass the cache for a single run: `go test ./... -count=1`.

