# Local Process Deployment

> **New to fracta?** Start with the [Local Process Quickstart](../../docs/quickstart-local-process.md) for a step-by-step guide, or the [Getting Started Guide](../../docs/getting-started.md) for an overview of all deployment modes.

Local-process mode runs fracta on one machine:

- control plane daemon on `:9090`
- gateway subprocess on `:8080`
- agents as local subprocesses
- SQLite state at `.fracta/state.db`
- local MCP backends through `podman`, `uvx`, or host CLIs

## Config

The canonical config is:

```bash
deployment/local-process/fracta.yaml
```

Run it directly:

```bash
bin/fracta serve --config deployment/local-process/fracta.yaml
```

With 1Password-backed environment materialization:

```bash
op run --env-file .op-env -- bin/fracta serve --config deployment/local-process/fracta.yaml
```

## Runtime Launch Configs

Use these as repo-root symlink targets when connecting tools:

```bash
ln -sf deployment/local-process/runtimes/claude/.mcp.json .mcp.json
mkdir -p .codex
ln -sf ../deployment/local-process/runtimes/codex/config.toml .codex/config.toml
```
