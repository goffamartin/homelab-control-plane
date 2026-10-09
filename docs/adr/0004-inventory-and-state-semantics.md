# ADR-0004: Session-scoped inventory revision and observed state

- Status: Accepted
- Date: 2026-10-09

## Decision

An inventory revision is monotonic within one authenticated session. A new
session starts a new revision scope and replaces the previous inventory only
after its complete inventory and matching state snapshot validate successfully.
Every asynchronous update carries the session ID and inventory revision.

Resource state is observed from the local runtime. YAML config declares identity,
adapter, target and allowed actions; it is not a source of current state. Each
observation includes `observed_at`, a typed state and optional bounded diagnostic.
The controller exposes freshness and marks observations stale when their session
disconnects or their configured age limit expires.

Node liveness, resource state and command outcome are separate concepts. For
example, an online node can report a stopped container and a failed restart.

## Consequences

The agent observes all resources on connection, after an action and periodically.
The controller rejects updates from stale sessions or unrelated revisions.
