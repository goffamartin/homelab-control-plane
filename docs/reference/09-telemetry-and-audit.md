# Bounded telemetry and audit

The controller and agent binaries emit structured JSON audit events on stderr.
CLI stdout/JSON, wire messages, admission rules and persistence formats are
unchanged. This is best-effort operational diagnostics, **not** a durable,
complete security audit trail or a replacement for the command ledger/journal.

## Event and metric contract

`internal/telemetry` owns a fixed event vocabulary and fixed-cardinality counters.
Events cover authentication/API rejection, connection rejection, session
start/end/replacement/reconnect, liveness changes, admission/duplicate/rejection,
attempt/retry/ACK/running/result/unresolved, storage failure, reconciliation
queries and write-queue saturation. The same logical operation has independent
controller and agent events; their counters are not added as unique operations.

Fields are time, controlled event/outcome, actor, node/resource/command/session
IDs, action, attempt and duration. Each string is copied into at most 128 ASCII
bytes with control/non-ASCII bytes replaced. No raw error, result message,
adapter output, request path/query, HTTP header, token or private key is copied
into events. Unknown agent outcome codes become `OTHER_RESULT`. IDs intentionally
remain visible metadata; do not put secrets in identifiers.

In legacy token mode the local API identity is `local-api`, not a per-person account.
Policy mode instead attributes API calls to authenticated principal IDs;
see the [credential contract](10-scoped-api-credentials.md).
Authenticated agents use their certificate node identity. Internal calls use
`controller`; results restored from the ledger use `controller-recovery` because
actor attribution is not durable yet.
Command session ID is the admission session, not proof of the result's delivery
session; restored operations have no retained admission session metadata.

Successful controller lifecycle events follow successful persistence. Repeated
ACKs/results do not increment transition counters again. Attempts describe
durably recorded delivery attempts, not proof of successful transmission. Agent
ACK events follow a successful frame write, and running follows MarkRunning.
Agent result events may be `EXECUTION_UNKNOWN` after a journal failure; they do
not imply durable terminal storage. Result duration includes waiting from
admission to completion, including disconnected/recovery time on the controller.

Counters include successful complete frames and actual plaintext HLCP bytes in
both directions, including partial I/O. Frame decoding precedes semantic payload
validation, so a decoded frame is not necessarily an accepted session message.
TLS handshake/ciphertext, TCP overhead and HTTP API bytes are excluded. F11 must
use independent network measurement when evaluating total transport overhead.
Result duration has count/sum/max, not a percentile histogram.

Gauges cover connections, sessions, active controller operations, ledger entries,
agent pending jobs and journal entries, plus configured capacity limits. Snapshot
fields are individually safe under concurrency, not one atomic view of every
subsystem. Controller/agent collectors should not be shared. Counters reset on
process start, reported by `started_at`; journal/ledger capacities survive only
through their existing persistence. No new telemetry database is created.

`Server.Telemetry().Snapshot()` and `Events()` are internal read-only accessors.
The diagnostics API implements
[authorized bounded queries and cursor semantics](11-bounded-diagnostics-api.md);
MCP is not implemented. Container logs use a separate
[bounded log channel](56-container-logs.md), not this audit ring.

## Memory, overload and shutdown

- The in-memory event ring retains the latest 512 events, overwriting older
  entries. Event retrieval returns a bounded independent copy.
- An optional sink gets one worker and a separate 512-event queue. Full queue
  drops new output and increments `audit_dropped`; counters/ring still update.
- Sink errors increment `audit_sink_errors`, without retrying indefinitely or
  logging the raw sink error. No external logging I/O runs under lifecycle locks.
- Shutdown cancels the worker and discards queued output. Close has a caller
  deadline and does not guarantee a final flush. A sink that ignores cancellation
  can leave at most its one worker blocked until process exit; never a worker
  per record. Command admission remains governed by durable storage, not audit.
- Journald can independently discard rate-limited events. Its drops are not
  counted by the application sink counters; inspect journal suppression messages.

## Opt-in system journald profile

Prepared files, not automatically installed:

- `deploy/systemd/hlcp-telemetry.conf`: system-service drop-in routing stdout and
  stderr into namespace `hlcp`, with per-service rate settings.
- `deploy/systemd/journald-hlcp-retention.conf`: namespace-specific storage policy.

For an opted-in system service, the operator installs the first file as
`/etc/systemd/system/hlcp-controller.service.d/10-telemetry.conf` (and optionally
the system agent service), and the second as
`/etc/systemd/journald@hlcp.conf.d/10-retention.conf`. It must **not** be installed
as machine-wide `journald.conf`. Do not apply LogNamespace to a rootless Podman
user service; use the user profile and host/user journald policy. Check the target
systemd version and units before enabling, then reload/restart under the approved
deployment procedure.

Initial namespace policy: persistent journal target 128 MiB, runtime fallback
32 MiB, file targets 8/4 MiB, keep-free targets 256/32 MiB, maximum retention
7 days and file age 1 day. Rate settings are 1000 messages/30 seconds per service.
These are journald policies, not an exact byte/time boundary: active files,
rotation and available space affect enforcement, and the effective rate limit
may be multiplied by free disk space. Filesystem capacity must be monitored.
Forwarding to syslog/console/wall is disabled in this namespace to avoid another
unbounded local copy. See upstream [journald configuration](https://github.com/systemd/systemd/blob/main/man/journald.conf.xml)
and [service execution settings](https://github.com/systemd/systemd/blob/main/man/systemd.exec.xml).

After approved deployment, inspect only the intended namespace, for example:

```text
journalctl --namespace=hlcp -u hlcp-controller.service -n 100 --no-pager
journalctl --namespace=hlcp --disk-usage
```

These optional policies are not a guarantee of exact disk retention. Real
journald disk-retention and storage-full behavior need host-specific testing;
see [validation](../validation.md).

## Verification evidence

Automated regression tests cover 8 concurrent producers/8000 uniquely identified
results without growing metric labels or the ring; 10000 events against a blocked
sink; bounded close when a sink ignores cancellation; sink failure/cancellation;
field/JSON bounds and untrusted outcome handling; complete/partial wire I/O;
API header/URI credential privacy; durable transition deduplication, late result
and failed admission; agent queue/journal capacity and adapter output privacy.

`TestMTLSHeartbeatAndCommandWithBlockedAuditSink` uses the production mTLS
controller, agent, journals and sessions. A blocked sink and 10000-event burst do
not prevent a subsequent heartbeat or a one-side-effect resource action. It
checks both audit streams/counters and zero session/connection gauges on shutdown.
Only the external container runner is fake: this is not live Podman evidence.

Current deployment evidence and limitations are recorded in
[validation](../validation.md), separately from automated coverage.
