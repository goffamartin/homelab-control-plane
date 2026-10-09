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
restart queries the existing command ID before any execution retry. Retry policy
is bounded and configurable for experiments.

## Consequences

HLCP guarantees at-most-once execution only within the retained journal window.
It does not claim distributed exactly-once execution. An uncertain operation is
reported as uncertain and requires read-only reconciliation or an explicit new
operator decision.
