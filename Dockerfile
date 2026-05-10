# Stage 1: Build the Go binaries
FROM golang:1.25-bookworm AS builder

WORKDIR /src

# Core module deps (cached layer)
COPY go.mod go.sum ./
RUN go mod download

# Copy all source
COPY . .

# Build core binary
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/fracta .

# Stage 2: Production image
FROM node:20-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      git ca-certificates python3 curl procps && \
    rm -rf /var/lib/apt/lists/*

RUN npm install -g @anthropic-ai/claude-code@2.1.96 @openai/codex@0.125.0 opencode-ai@1.14.28

# Install uv for Python dependency management
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv

# fracta binary from build stage
COPY --from=builder /out/fracta /usr/local/bin/fracta

# Strategy dependencies (cached layer — only invalidated when lockfile changes)
COPY strategies/pyproject.toml strategies/uv.lock /opt/fracta/strategies/
RUN cd /opt/fracta/strategies && uv sync --frozen --no-dev

# Strategy source (changes more often than deps)
COPY strategies/ /opt/fracta/strategies/

# Auth-helpers directory: operators mount their own helpers here via volumes
# / configmaps (see spec-42 §6). Empty by default — the image is auth-agnostic.
RUN mkdir -p /opt/fracta/auth-helpers

# Generic entrypoint — copies orchestrator-prepared auth, starts sidecar
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

WORKDIR /workspace

ENTRYPOINT ["entrypoint.sh"]
