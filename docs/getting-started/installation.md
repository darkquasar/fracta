---
title: Installation
description: Prerequisites and how to install fracta
---

# Installation

## Prerequisites

<Steps>
  <Step title="Go 1.22 or later">
    Fracta is a Go binary. You need Go 1.22+ to build it from source.

    ```bash
    go version
    ```

    Install via your platform package manager or from [golang.org/dl](https://golang.org/dl).
  </Step>

  <Step title="A runtime CLI">
    Fracta orchestrates one or more of these AI CLIs. Install at least one:

    - **Claude CLI** — {/* TODO: link to install instructions */}
    - **Codex CLI** — {/* TODO: link */}
    - **OpenCode** — {/* TODO: link */}
  </Step>

  <Step title="Optional: Docker">
    Required only for the [docker-compose deployment mode](/guides/deployment/docker-compose).
  </Step>

  <Step title="Optional: A Kubernetes cluster">
    Required only for the [kubernetes deployment mode](/guides/deployment/kubernetes). `kind` or `k3d` works locally.
  </Step>
</Steps>

## Build from source

```bash
git clone https://github.com/darkquasar/fracta
cd fracta
go build -o bin/fracta .
./bin/fracta --version
```

The binary lands at `bin/fracta`. Add `bin/` to your PATH or copy the binary somewhere on PATH:

```bash
cp bin/fracta /usr/local/bin/
```

## Verify

```bash
fracta --help
```

You should see the list of subcommands (`spawn`, `list`, `peek`, `say`, `kill`, etc.).

## What's next

<Card title="Your First Agent" href="/getting-started/first-agent">
  Spawn, peek, and clean up an agent in five minutes.
</Card>

<Card title="Pick a Deployment Mode" href="/guides/deployment/overview">
  Decide whether to run fracta locally, in compose, or on kubernetes.
</Card>
