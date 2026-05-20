---
title: Event Bus
description: Internal event buses, sinks, and lifecycle events
---

## Overview

Fracta has an internal in-process event bus in `internal/events`.

Its job is to let subsystems emit structured lifecycle and infrastructure events without knowing where those events go. Emitters publish an `events.Event`; sinks decide whether to:

- write a structured log line
- persist a durable row in `agent_events`
- write a Kubernetes Event in K8s mode

This is an internal observability and decoupling seam. It is not a workflow engine, a broker, or an event-sourced state system.

## What It Does

The event bus standardizes event emission across layers such as:

- `orchestrator`
- `runtime.k8s`
- `mcpclient`
- `gateway`
- `mcpserver.GatewayServer`

Those packages emit structured events through `events.Bus` instead of importing Postgres/SQLite event-writing helpers directly.

The current built-in sinks are:

- `LogSink`: emits one structured JSON log line via `fractalog.Component("events")`
- `StoreSink`: persists the event to `agent_events`
- `K8sEventSink`: writes selected infra-facing events to Kubernetes Events when the runtime backend is Kubernetes

## Architecture

```text
emitter
  -> events.Bus
     -> FanoutBus
        -> LogSink
        -> StoreSink
        -> K8sEventSink (K8s runtime only)
```

`ControlPlane` constructs the core bus. In normal agent-serving mode it builds a `FanoutBus` with:

- `LogSink` always
- `StoreSink` when the active store can persist events
- `K8sEventSink` when the runtime backend is Kubernetes and the backend can provide a Kubernetes event recorder

Gateway-only control plane builds a smaller bus:

- `LogSink`
- `StoreSink`

No runtime backend exists there, so no `K8sEventSink` is attached.

## Event Model

Events use a shared structured model:

- `ID`: random UUID for event tracking
- `Time`: timestamp
- `Component`: emitting subsystem
- `Category`: event family
- `Resource`: concrete thing the event is about
- `Action`: event label such as `create`, `connect_attempt`, `status_change`
- `Outcome`: normalized result such as `success`, `failure`, `timeout`
- `Severity`: `debug`, `info`, `warn`, `error`
- `Task`, `MissionID`, `ObjectiveID`: fracta-native correlation keys
- `Detail`: short human-readable context
- `Attrs`: metadata bag for extra structured fields

Example:

```go
e := events.Warn("mcpclient", "connect_attempt", err.Error())
e.Category = "backend"
e.Resource = "mcp_server:vendor"
e.Outcome = "failure"
bus.Emit(ctx, e)
```

## Relation To Logging

The event bus does not replace fracta's logging system.

Fracta still uses:

- Go `log/slog`
- `internal/fractalog`
- `fractalog.Component("name")` for structured JSON logs

What changed is how *event-style* observability is emitted.

Before the event bus:

- normal logs were emitted directly through `fractalog.Component(...)`
- some lifecycle events were written through the old `state.EventEmitter`
- store-backed event writes also logged as a side effect
- lower-level packages had no shared, store-independent event seam

After the event bus:

- normal application logs still work the same way as before
- event-producing code emits `events.Event` through `events.Bus`
- `LogSink` is now the single built-in logging side effect for bus events
- `StoreSink` is persistence-only and does not log
- K8s-native event export is handled by `K8sEventSink`, not by emitters

So the answer is:

- general logging architecture: mostly unchanged
- event logging architecture: changed substantially

## What Stayed The Same

- JSON logging still goes through `slog` and `fractalog`
- component-tagged logs are still the primary raw debug path
- stderr/file logging configuration is unchanged
- code that is not emitting bus events should still log directly with `fractalog.Component(...)`

## What Changed

For event-like signals,  fracta now has one consistent flow:

1. Code emits a structured event.
2. The bus fans it out to sinks.
3. Logging, persistence, and Kubernetes export are handled centrally.

This gives fracta:

- less coupling between emitters and stores
- durable event rows without store imports in low-level packages
- one canonical event log format
- no duplicate log line from the store sink
- a K8s-native operator path in Kubernetes mode

## Durable Storage

`StoreSink` writes structured fields to `agent_events` and also writes a derived legacy flat `event` alias for compatibility with older readers/tests.

That means:

- new code should think in structured fields
- old consumers of the flat `event` column can continue to work during the migration window

## When To Use The Bus vs Direct Logging

Use the event bus when the signal is:

- a lifecycle or observability event that may need multiple sinks
- worth persisting durably
- useful as a Kubernetes Event in K8s mode
- something multiple layers may want to emit consistently

Use direct `fractalog.Component(...)` logging when the signal is:

- local debug detail
- step-by-step operational tracing
- not part of the shared event taxonomy
- not something that needs durable storage or K8s Event export

In short:

- bus = structured cross-sink event
- direct log = ordinary application log
