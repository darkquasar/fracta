---
title: Glossary
description: Terms you'll encounter in the fracta docs
---

# Glossary

## Agent
A spawned AI task with its own worktree, state entry, and mailbox.

## Backend Runtime
The process executor that launches the agent — local subprocess or kubernetes Job.

## Control Plane
The HTTP API and orchestrator that manages agent state, lifecycle, and concurrency. Listens on port 9090.

## Credential Profile
A named bundle of auth configuration (API keys, OAuth tokens, helper commands) referenced by a runtime.

## Feature Branch
The git branch each agent commits to. Named after the task. Created automatically when the worktree is created.

## Gateway
The MCP server that proxies backend tools and maintains the knowledge graph. Listens on port 8080.

## Host Runtime
A specific CLI implementation (Claude, Codex, OpenCode) that owns the protocol, batch format, and resumption semantics.

## Intent
A human-readable string an agent sets to describe what it's currently working on. Visible to other agents and to `fracta list`.

## Knowledge Graph
A FalkorDB-backed store of domain sources, data stores, MCP servers, tools, and discovered entities (System, Identity, Event, Hunt).

## Mailbox
A per-agent message queue used for inter-agent communication. Send with `fracta_send`; read with `fracta_inbox`.

## MCP Server
A tool provider that speaks the Model Context Protocol. Fracta proxies tools from registered MCP servers through its gateway.

## MCP Tool
A specific function exposed by an MCP server (e.g. `elastic.search_logs`, `notion.get_page`).

## Resume Token
An opaque string that lets an agent continue a session from where it left off. Stored in the state store and replayed by the runtime on resume.

## State Store
The persistent agent registry tracking status, output, intent, and resume tokens. Backed by SQLite or PostgreSQL.

## Strategy
A python DAG pipeline (in `strategies/`) for multi-step investigations. Run by the strategy sidecar.

## Task
The descriptive name of an agent (e.g. `investigate-login-spike`). Doubles as the agent's identifier and feature branch name.

## Worktree
An isolated git checkout created per agent, sharing the main repo's `.git` object store. Lives under `.fracta/worktrees/<task>`.
