---
title: Coordination
description: Mailbox, intent, peek — how agents stay aligned without colliding
---

# Coordination

Agents in the swarm work in isolation by default — that's the point of per-agent workspaces. But they're not deaf to each other. Three lightweight mechanisms keep them aligned: the mailbox, declared intent, and non-disruptive peeks.

## Mailbox

Every agent has a per-agent mailbox. Two MCP tools drive it:

- `fracta_send(from, to, message)` — push a message into another agent's inbox.
- `fracta_inbox(name)` — read unread messages from your own inbox.

This is the primary handoff mechanism. When agent A finishes a piece of work that agent B is waiting on, A sends B a message. B reads it next time it checks the inbox. No locks, no shared state, no tight coupling.

## Intent

Each agent exposes a one-line description of what it's currently working on — its **intent**:

- `fracta_set_intent(name, intent)` — set your own intent.
- `fracta_list` — see every agent's status and intent at a glance.

Intent is how a fleet of agents stays legible. A developer running ten parallel investigations can scan `fracta list` and immediately see who's looking at what.

## Peek

`fracta_peek(name)` lets one agent read another agent's recent semantic output **without disturbing it**. No message is delivered, no session is interrupted. This is for "is the dependency done yet?" and "what did agent A actually find?" — read-only inspection.

`fracta peek` is also available as a CLI command for humans watching the swarm work.

## The inbox rhythm

Agents that coordinate well follow a consistent cadence rather than constantly polling:

1. **Start of iteration:** Check `fracta_inbox(name="<you>")` for new instructions.
2. **Deep work:** Execute focused tool calls on the current component.
3. **Commit checkpoint:** Wrap the component, run tests if applicable, and commit.
4. **Sync:** Check inbox again for dependency or merge notices, then repeat.

If an agent is waiting on a specific dependency, it can check the inbox more often or peek the peer. If it's deep in independent work, twice per iteration is enough.

## Coordinating dependent work

When agent B's work depends on agent A's output, the typical flow is:

1. **Agent A** finishes its piece, commits, and notifies the orchestrator (or a chessmaster agent) via `fracta_send`.
2. **Chessmaster** runs `fracta merge <a>` — A's work lands on the integration branch.
3. **Chessmaster** sends agent B a message: "main updated, run `git merge main`".
4. **Agent B** merges main into its own feature branch and continues building on top.

This works because all worktrees share the same git object store. When the integration branch advances, every worktree can see the new commits immediately.

## Shared progress snapshot

Fracta also maintains `.fracta/progress.md` — a file with the latest status and intent for every agent, plus the chessmaster's last action. Agents (and humans) can read this file to get a peer-progress overview without spamming inboxes.

## Source

- Mailbox: [`internal/mailbox/`](https://github.com/darkquasar/fracta/blob/main/internal/mailbox)
- Lifecycle writer: [`internal/agentlifecycle/`](https://github.com/darkquasar/fracta/blob/main/internal/agentlifecycle)
- Say flow: [`internal/orchestrator/say.go`](https://github.com/darkquasar/fracta/blob/main/internal/orchestrator/say.go)
