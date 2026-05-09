---
title: Your First Agent
description: Spawn, peek, and clean up a fracta agent
---

# Your First Agent

This walkthrough takes about 5 minutes. It assumes you have fracta [installed](/getting-started/installation) and at least one runtime CLI configured.

<Steps>
  <Step title="Initialize fracta in your project">
    From inside any git repository:

    ```bash
    cd /path/to/your/git/project
    fracta init
    ```

    This creates a `.fracta/` directory for state and worktrees. It does not modify your repo's tracked files.
  </Step>

  <Step title="Spawn an agent">
    ```bash
    fracta spawn --task hello --contract "Write a haiku about distributed systems."
    ```

    Fracta creates a worktree under `.fracta/worktrees/hello`, launches your default runtime CLI in that worktree, and tracks the agent in its state store.

    **Useful flags:**

| Flag | What it does |
|---|---|
| `--runtime` | Override the default runtime (`claude`, `codex`, `opencode`). Default comes from your `fracta.yaml`. |
| `--mode` | `batch` (default) — single-shot execution and return; `stream` — long-lived MCP session for follow-ups via `fracta say`. |
| `--tier` | `heavy`, `medium`, or `light` — picks a model from your `fracta.yaml` `model_tiers` map. |
| `--model` | Set the model directly (overrides config and tier). |
| `--base` | Branch to base the worktree on (default from config, usually `main`). |
| `--contract` | Inline contract text or a path to a contract file. Optional. |
| `--dry-run` | Resolve the full spawn chain without creating an agent. Pair with `--format yaml\|json`. |
  </Step>

  <Step title="Watch progress">
    ```bash
    fracta watch hello
    ```

    Streams the agent's events (tool calls, output, status transitions) to your terminal in real time. `Ctrl-C` to detach without killing the agent.
  </Step>

  <Step title="See the result">
    ```bash
    fracta peek hello
    ```

    Prints the agent's recent semantic output. Run after the agent has completed (status `completed` in `fracta list`).
  </Step>

  <Step title="Clean up">
    ```bash
    fracta kill hello
    ```

    <Warning>
      `fracta kill` removes the agent's worktree and state entry. The feature branch in the underlying git repo is also deleted. Make sure you've merged anything you want to keep.
    </Warning>
  </Step>
</Steps>

## Spawn multiple in parallel

```bash
fracta spawn --task task-a --contract "Refactor the auth module."
fracta spawn --task task-b --contract "Add a unit test for the cache."
fracta spawn --task task-c --contract "Update the README."
```

Then list them:

```bash
fracta list
```

Each agent runs independently, in its own worktree. They can message each other via `fracta_send` if you ask them to.

## What's next

<Card title="Pick a Deployment Mode" href="/guides/deployment/overview">
  Local-process vs docker-compose vs kubernetes.
</Card>

<Card title="Configure Your Runtime" href="/guides/authentication/runtime-configuration">
  Claude, Codex, and OpenCode setup.
</Card>

<Card title="CLI Reference" href="/reference/cli/overview">
  Every command and every flag.
</Card>
