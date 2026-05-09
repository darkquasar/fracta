# Fracta Multi-Agent Orchestration System

## Overview

Fracta is a multi-agent orchestration system that allows spawning autonomous Claude agents, each working in isolated git worktrees. Agents communicate via mailbox messaging and coordinate through the chessmaster (main orchestrator).

## Git Rules for Subagents

All worktrees share the same `.git` directory, so branch refs are always in sync.

### Committing (always allowed)

Commit your work to your feature branch at any time. These patterns are pre-approved in don't-ask mode:

```bash
git add <files>
git commit -m "description of changes"
git add . && git commit -m "chunk name"
```

Use a heredoc or `-F` file for multi-line messages when you need longer summaries.

### Merging INTO main (never do this)

**DO NOT** merge your feature branch into `main` from your worktree. The `main` branch is checked out in the main repository — merging into it from a worktree causes conflicts. Only the chessmaster merges into main.

### Pulling FROM main or green (do this when told)

When the chessmaster tells you that `main` or `green` has been updated, sync your feature branch:

```bash
git merge main   # or: git merge green
```

These merges are also pre-approved and safe because you're merging INTO your feature branch (which you own), not into the integration branch. Do this when:
- The chessmaster sends you a message saying the integration branch was updated
- You need another agent's work that has already been merged upstream

### Read-Only Commands (always allowed)

Commands such as `ls -la`, `cat`, `find`, and `grep` are whitelisted so you can inspect the repository without intervention. If you hit a permission error on a read-only command, call it out in your status update immediately.

### Inbox Rhythm

Follow a consistent cadence so you're aligned with other agents:

1. **Start of iteration:** Check `fracta_inbox(name="<you>")` for new instructions.
2. **Deep work:** Execute 5–10 tool calls focused on the current component.
3. **Commit checkpoint:** Wrap the component, run tests if applicable, and commit.
4. **Sync:** Check inbox again for dependency or merge notices, then repeat.

If you're waiting for a specific dependency, check your inbox every 2–3 other actions or use `fracta_peek` to inspect peer progress without spamming.

## Coordination Pattern

### Basic flow (independent tasks)

1. Chessmaster spawns agents with tasks
2. Agents work independently, commit to their feature branches
3. Agents notify chessmaster when done via `fracta_send`
4. Chessmaster merges each branch into main with `git merge feature/<agent>`

### Iterative flow (dependent tasks)

When Agent B needs Agent A's work:

1. **Agent A** commits and notifies chessmaster: "My work is committed, ready to merge"
2. **Chessmaster** runs `fracta_merge(name="agent-a")` — merges Agent A's branch into the current branch (agent stays alive)
3. **Chessmaster** tells Agent B via `fracta_send`: "Main updated with A's work, run `git merge main`"
4. **Agent B** runs `git merge main` from its worktree — gets Agent A's code
5. **Agent B** continues building on top of Agent A's changes

This works because all worktrees share the same git object store. When the chessmaster updates the integration branch, every worktree can immediately see the new commits.

### Important: `fracta_merge` vs `fracta_kill`

- **`fracta_merge`**: Merges the agent's feature branch into the current branch. Non-destructive — the agent stays alive and can keep working.
- **`fracta_kill`**: Kills the agent — removes worktree, deletes feature branch, removes from state. Use when an agent is completely done.

## Inter-Agent Communication

Agents have access to these MCP tools:
- `fracta_list` — See all agents, their status, intent, and unread message count
- `fracta_peek(name)` — Read another agent's recent semantic output
- `fracta_send(from, to, message)` — Send a message to another agent's mailbox
- `fracta_inbox(name)` — Read unread messages from your mailbox
- `fracta_set_intent(name, intent)` — Set your current intent so others know what you're working on

Check your inbox periodically. Coordinate with other agents when your work overlaps.

## Shared Progress Snapshot

Fracta maintains `.fracta/progress.md` with the latest status/intent for every agent plus the chessmaster's last action. Review that file to see peer progress without spamming their inbox.

## Graph Update Protocol (MANDATORY)

Every investigation, hunt, or external data query MUST update the knowledge graph. This is not optional — the graph is the persistent memory of the platform.

### 4-tier resolution chain

```
DomainSource -[:STORED_IN]-> DataStore -[:QUERYABLE_VIA]-> MCPServer -[:PROVIDES]-> MCPTool -[:RETURNS_FIELD]-> MCPField
  (what)                      (where)                       (server)                (callable)                 (schema)
```

### What you create vs what the system manages

**You create** (scaffold/discovered — these persist):
- `DomainSource` — a named data stream (e.g., "AWS CloudTrail", "VendorSecurity Alerts")
- `DataStore` — where data physically lives, keyed by URI
- `MCPField` — fields a tool returns, with `semantic` annotation linked to a `Semantic` node
- `Semantic` — vocabulary concepts
- Edges: `STORED_IN` (DomainSource→DataStore), `QUERYABLE_VIA` (DataStore→MCPServer), `HAS_FIELD`, `RETURNS_FIELD`
- Discovered entities: `Hunt`, `System`, `Identity`, `IP`, `Event`, `Finding`

**The reconciler manages** (inventory — do NOT create):
- `MCPServer` — auto-created from fracta.yaml config
- `MCPTool` — auto-created when backends connect
- Edge: `PROVIDES` (MCPServer→MCPTool)

### DataStore URI patterns
- Elasticsearch: `elasticsearch://<config_key>/<index-pattern>` (e.g., `elasticsearch://elastic_audit/.ds-logs-audit-platform-*`)
- Snowflake: `snowflake://<account>/<db>/<schema>/<table>`
- S3: `s3://<bucket>/<prefix>/`
- Gateway-only (API, no physical storage): `fracta-mcp-gateway://<server>/` (e.g., `fracta-mcp-gateway://vendor/`)

### After every external tool query

Run `graph_checkpoint(mcp_servers="<server1>,<server2>")` immediately. Fix every gap before continuing.

When you discover a new data source:
1. `MERGE (d:DomainSource {name: $name})` — set `_source = 'agent:<you>'`
2. `MERGE (ds:DataStore {uri: $uri})` — use the correct URI pattern above
3. Wire: `MERGE (d)-[:STORED_IN]->(ds)`
4. Wire: `MATCH (ms:MCPServer {config_key: $server}) MERGE (ds)-[:QUERYABLE_VIA]->(ms)` — MATCH the existing MCPServer, don't create one
5. For key fields: `MERGE (f:MCPField {name: $name}) SET f.semantic = $sem` then `MATCH (mt:MCPTool {name: $tool}) MERGE (mt)-[:RETURNS_FIELD]->(f)`

### After every finding or discovered entity

Create nodes for everything real you found:
- `System` — any host, device, cloud resource, or domain
- `Identity` — any user, service account, or principal
- `IP` — any IP address seen in events
- `Event` — any alert, finding, or security event (set `semantic` property)
- `Hunt` — one node per investigation thread

Wire relationships: `Hunt -[:TARGETS]-> System`, `Identity -[:USES]-> System`, `Event -[:DETECTED_ON]-> System`, etc.

### End of every investigation

Call `graph_checkpoint()` — confirm `all_clear: true` before declaring complete.

<hr />

## Pre-Approved Go Dependencies

You may install these libraries without requesting permission when they are relevant to your task:

- `github.com/charmbracelet/bubbletea`
- `github.com/nsf/termbox-go`
- `github.com/gdamore/tcell`
- `github.com/fatih/color`
- `github.com/stretchr/testify`
- `github.com/spf13/cobra`

For anything outside this list, describe the need in your status update or mailbox message before running `go get`.

## Logging Convention

All logging MUST go through `internal/fractalog`. Never use bare `slog.Info/Warn/Error()` or `slog.Default().With()`.

- **`fractalog.Component("name")`** — returns a `*slog.Logger` tagged with `"component"` key. Use this in every file that logs.
- **Struct with logger field:** call `fractalog.Component("name")` in the constructor.
- **Standalone function:** call `log := fractalog.Component("name")` at function start.
- **Never use package-level `var log = fractalog.Component(...)`** — the handler is captured at call time, before config-driven `AttachFile` runs.
- Component names should match the package name (e.g., `"gateway"`, `"reconciler"`, `"serve"`).
