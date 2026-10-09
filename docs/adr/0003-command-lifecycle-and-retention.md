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
`DUPLICATE_CONFLICT`. A crash after admission but before a durable result becomes
`unknown`; it is not automatically replayed.

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
