# Bounded controller diagnostics — F10C

This API reads controller telemetry and its in-memory structured audit ring.
It does not read arbitrary files, the system journal, agent journals, controller
ledger files or container logs. Remote Docker/Podman log transport remains M2.
No CLI grammar, wire message or durable persistence format changes in this phase.

## Endpoints and permissions

All endpoints use the existing loopback API and bearer/Host/Origin boundary.

| GET endpoint | Permission / data |
|---|---|
| `/v1/diagnostics/metrics` | Explicit `scope.all_nodes: true`; global fixed-cardinality snapshot |
| `/v1/diagnostics/logs` | Same global scope; retained controller audit events |
| `/v1/nodes/{node}/diagnostics/logs` | Allowed node; events filtered by resource scope |
| `/v1/nodes/{node}/resources/{resource}/diagnostics/logs` | Allowed node/resource; only matching resource events |

Observer and operator can read these endpoints within their scopes. Restricted
observers cannot obtain global totals by guessing the metrics URL. A node view
includes allowed-resource events and node-level session/liveness events, but
omits unscoped command-ID events: those might refer to hidden resources. Global
events with no node attribution are omitted from a restricted node view. Direct
out-of-scope access is generic 403. Missing inventory is not a reason to discard
authorized historical log entries; an allowed but never-observed target can
return an empty page. No read sends COMMAND or admits a ledger record.

## Query and response contract

Metrics takes no query parameters. Log queries accept only:

- `limit`: integer 1..200, default 100.
- `cursor`: a non-empty cursor returned by the same query/credential.

Unknown/duplicate/empty parameters, malformed escaping and queries over 1024
bytes are rejected with `INVALID_QUERY` (400). No `follow`, arbitrary time/path
selector or streaming mode is introduced. For example:

```text
GET /v1/nodes/debian-01/resources/example-catalog/diagnostics/logs?limit=100
GET /v1/nodes/debian-01/resources/example-catalog/diagnostics/logs?cursor=<next_cursor>
```

Log response fields:

| Field | Meaning |
|---|---|
| `boot_id` | Random diagnostics-instance identity; changes with controller recreation |
| `observed_at` | UTC query time, not an assertion that all resource states are fresh |
| `events` | Chronological retained event records, possibly empty |
| `next_cursor` | Continuation after the last scanned record |
| `truncated` | A matching record remains beyond the count/byte limit |
| `gap` | Earlier buffer history was overwritten; continuity is not guaranteed |
| `reset` | A foreign/restarted diagnostics-instance cursor was reset to retained history |

The response includes no internal sequence numbers, buffer watermarks or count
of hidden records. Nonmatching events are skipped while advancing the cursor.
Retention is a shared 512-event ring, so a gap is conservative: unrelated traffic
can overwrite history too. An initial query starts at the oldest retained event
and can report a gap if history was already overwritten. There is no guarantee
of lossless delivery and no durable cursor store or unlimited log history.

Cursors encrypt the sequence using AES-GCM with a per-instance key and random
nonce. Associated data binds the credential digest, principal identity/role /
scope and node/resource query. A current-instance cursor cannot be shared between
credentials or query targets; token/scope changes invalidate it. Count limits
can change between pages. Invalid/tampered current-instance cursors return
`INVALID_CURSOR` (400). A shape-valid foreign-boot cursor instead signals reset
and starts a new authorized window; it is not authenticated as an old cursor
and never grants access to its former owner's data. The boot identity is public
metadata; the sequence and credential binding are not exposed in clear text.

Metrics response contains `boot_id`, `observed_at` and `metrics` with its
`started_at`, fixed counters and gauges. It is a process-lifetime, individually
concurrency-safe snapshot, not a transactionally consistent multi-subsystem view.

## Resource budgets and cancellation

- Every successful encoded reply, including its trailing newline, is at most
  64 KiB. Log pagination accounts for actual JSON escaping and metadata size.
- Maximum two concurrent diagnostic queries per principal and eight globally.
  Admission is immediate; there is no unbounded diagnostic wait queue.
- Ten requests and 256 KiB of encoded response budget per principal per fixed
  ten-second window. Boundary bursts are possible; this is not a sliding limiter.
- Reserve 64 KiB before work, then refund unused bytes for a successful encoded
  reply. Errors after reservation conservatively retain the full charge. Failed
  writes retain the encoded-reply charge, not only bytes actually delivered.
- Quota rejection is 429 `DIAGNOSTIC_BUDGET` with conservative `Retry-After: 10`.
  It does not restrict normal inventory, command execution or reconciliation.
- Identity budget storage is bounded to 64 slots. Inactive entries older than
  one minute are reclaimed when a new identity needs a slot; otherwise capacity
  exhaustion denies diagnostics. Token rotation does not reset an identity budget.
- Five-second request context and socket write deadline. Cancellation produces
  `CANCELED` (499) when a response can still be written. Normal Go HTTP writers
  enforce the socket deadline; test recorders without deadline support are allowed.

Requests still produce the existing authentication/rejection audit events.
Global log polling can therefore see its own audit activity. A restricted query
does not expose those unrelated global records. Poll at a moderate cadence and
honor Retry-After; neither HTTP reads nor cursor gaps imply new external actions.

## Verification and next gate

Tests cover scope/hidden-ID protection, cursor pagination without duplicates,
overwrite gaps, restart reset, cross-identity/query binding and ciphertext
tampering; worst-case escaped-event byte limits; malformed queries; bounded
rate/byte/concurrency/identity tables; slot release, cancellation and socket
deadline propagation through the audit wrapper. The real mTLS integration adds
30 diagnostic polls without COMMAND/admission/side effects, then still executes
an authorized action once. Only the external container runner is fake.

Full Windows tests/vet, repeated mTLS integration and Linux race validation are
the local gate. Production service installation, actual retention/disk behavior,
live Podman and real MCP client connectivity are still separate acceptance gates.
Next is F10D: Debian management-service packaging and runbooks.
