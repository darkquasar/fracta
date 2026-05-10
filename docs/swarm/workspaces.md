---
title: Workspaces
description: Per-agent workspaces — git worktrees, directory workspaces, and what each mode supports
---

# Workspaces

Every agent runs in its own per-agent workspace. The shape of that workspace depends on the deployment mode, and that shape determines which capabilities are available — most notably, whether `fracta merge` is meaningful.

## The three workspace shapes

| Mode | Workspace type | Path | Git semantics |
|---|---|---|---|
| Local-process | `GitWorkspace` (git worktree) | `.fracta/worktrees/<task>` on the host | Full — feature branch per agent, shared `.git` object store, `fracta merge` works |
| Docker Compose | `DirectoryWorkspace` | `/workspace/agents/<task>` inside the container, bind-mounted from host | None — no per-agent branches, no `fracta merge` |
| Kubernetes | `DirectoryWorkspace` on a PVC | `/workspace/agents/<task>` in the agent pod | None — branches and merge are not available |

The shared interface — same MCP tool surface, same lifecycle, same mailbox — is what makes agents portable across modes. The git-specific capabilities only light up when the workspace is a real git worktree.

## Local-process: git worktrees

In local-process mode each agent gets a full git worktree:

- Shares the main repo's `.git` object store, so commits in any worktree are visible to all others.
- Runs on a feature branch named after the task.
- Is isolated — agents don't see each other's uncommitted files until something is merged.
- N agents can work simultaneously on the same repo without stepping on each other.

This is the only mode where `fracta merge` is meaningful.

### Merging back (local-process only)

When an agent's work is good, the chessmaster (the developer or another agent) merges its feature branch:

```bash
fracta merge <task>
```

This is **non-destructive** — the agent stays alive and can keep working. The current branch picks up the agent's commits via `git merge feature/<task>`. Merging back into the integration branch never happens from inside a worktree (it causes conflicts); only the chessmaster does it.

In Docker Compose and Kubernetes modes, `fracta merge` is not available. For git-based workflows in those modes you commit and push from the agent workspace explicitly, or fall back to local-process mode.

### Cleaning up

To remove an agent (any mode):

```bash
fracta kill <task>
```

In local-process mode this removes the worktree and deletes the feature branch. In Docker Compose / Kubernetes modes it removes the agent's directory and state entry.

## Why workspace isolation matters

Without isolation, ten agents running on the same project would race on the same files, overwrite each other's edits, and create commit storms on a single branch. The worktree-per-agent model gives each agent a complete, independent view of the repo while sharing the underlying object store — so nothing is duplicated on disk and merging back is a normal `git merge`.

## What this means for strategies

Strategies don't run in agent workspaces — they live in the strategy runner sidecar, which is shared. Workspaces are for the *exploration* stage; strategies are the *execute* stage. An agent in any workspace can call `strategy_run` and get the same answer.

## Source

- Spawn flow: [`internal/orchestrator/spawn.go`](https://github.com/darkquasar/fracta/blob/main/internal/orchestrator/spawn.go)
- Merge: [`internal/orchestrator/merge.go`](https://github.com/darkquasar/fracta/blob/main/internal/orchestrator/merge.go)
- Kill: [`internal/orchestrator/kill.go`](https://github.com/darkquasar/fracta/blob/main/internal/orchestrator/kill.go)
