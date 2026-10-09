# ADR-0003: Durable command lifecycle and bounded replay

- Status: Accepted
- Date: 2026-10-09

## Decision

A command is identified by `(authenticated node ID, command_id)` and has an
immutable resource/action fingerprint. Its controller-visible states are
`accepted`, `running`, `completed`, `unknown` and `expired`. External success or
failure belongs to a terminal result and is separate from lifecycle state.

The agent durably admits a new command before sending ACK. Execution happens in
a bounded worker queue. A duplicate with the same fingerprint is never executed
again while its journal entry is protected; a different fingerprint produces
`DUPLICATE_CONFLICT`. Admission records `queued`; only after successful ACK write
does the worker durably record `running` and invoke the adapter. Recovery of
`queued` produces `EXECUTION_NOT_STARTED`; recovery of `running` (or an older
entry without a phase) produces `EXECUTION_UNKNOWN`. Neither is replayed.

F5 uses one worker and at most 16 admitted jobs, with a 20-second execution
context and 4096-byte diagnostic limit. An active duplicate receives only an
`in_progress` ACK, not a fabricated terminal result. Work survives session loss;
agent shutdown cancels it and joins the worker before closing the journal.
Interrupted mutations are uncertain; interrupted read-only status is `TIMEOUT`.
Subprocess output is bounded while collecting it, and pipe cleanup has a one-second
wait limit. Executors must honor cancellation.

The agent journal is protected by both time and capacity. The initial v1 target
is a 24-hour minimum replay window, at most 1024 entries and an explicit byte
limit. Running and unresolved unknown entries cannot be evicted. If no safe entry
can be removed, admission fails with `JOURNAL_FULL`.

The controller persists its own bounded operation ledger. HTTP client timeout
does not delete the operation. Reconciliation after reconnect or controller
restart only queries the existing command ID; it never resumes execution retry
for a restored operation. Retry policy is bounded and configurable for experiments.

F6 uses a version-1 controller JSON ledger with an exclusive process lock and the
same Linux durable-replace primitives as the agent journal. Admission, send-attempt
count, ACK and terminal result are written before their effects become visible.
Storage failure stops further sends and prevents unpersisted success from being
returned. Limits are 128 active operations, 1024 retained operations and 8 MiB.
Definitive results are retained for at least 24 hours; unresolved/unknown results
are protected indefinitely within that capacity, with explicit `LEDGER_FULL`.

Same node/ID/payload submissions share the operation (including concurrent waits);
different payloads return HTTP 409 `COMMAND_CONFLICT`. Client cancellation stops
its delivery retries, not the admitted agent action, and never deletes history.
Late results are durable and may resolve an expired wait without creating a new
command. A canceled operation with zero delivery attempts is definitively
`EXECUTION_NOT_STARTED`; attempted delivery without a result remains uncertain.

`COMMAND_QUERY` carries the original immutable command fingerprint and has no
admission/execution side effects. `COMMAND_STATE` reports not_found, queued,
running, unknown or completed with a correlated terminal result. A missing or
uncertain entry never authorizes automatic replay. One bounded query loop per
authenticated session polls at most 128 retained operations per round; its interval
is the ACK timeout with a one-second minimum. API lookup works without current
inventory via `GET /v1/nodes/{node}/commands/{id}`. Unknown outcomes require an
explicit operator decision; a fresh action must use a fresh ID, not erase history.

## Consequences

HLCP guarantees at-most-once execution only within the retained journal window.
It does not claim distributed exactly-once execution. An uncertain operation is
reported as uncertain and requires read-only reconciliation or an explicit new
operator decision.
