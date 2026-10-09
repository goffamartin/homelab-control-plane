# HLCP v1 design draft

**Status:** Implementation draft. Core v1 decisions are frozen in `docs/adr`;
wire details can change until the first tagged v1 release, but only through a
coordinated protocol, test and documentation change.

## 1. Terminology

| Term | Meaning |
|---|---|
| Controller | Central control-plane process that accepts and tracks agent sessions. |
| Agent | Process on a managed node that creates an outbound session to the controller. |
| Node | A uniquely identified managed machine running an agent. |
| Resource | An agent-local, configured entity that exposes state and allowed actions. |
| Adapter | Agent component mapping a resource to an implementation such as systemd or Docker. |
| Command | A requested action on one resource, with a stable command identifier. |
| ACK | Proof that an agent accepted a command for processing; not proof of completion. |
| Result | Terminal outcome of a command. |
| Session | One live TLS/TCP connection between an agent and controller. |

## 2. Architecture

```text
CLI ── local/admin API ── Controller ── HLCP over TLS/TCP ── Agent ── Adapter ── Resource
                               │                              │
                         inventory/state                 allow-listed config
```

The agent initiates and re-initiates the connection. This avoids inbound firewall exposure on managed nodes. The controller is authoritative for currently connected sessions and command tracking. The agent is authoritative for the immediate observed state of its locally configured resources.

## 3. Node and resource model

Each agent has a stable `node_id` set in configuration. It registers its capabilities and a complete current resource inventory after session establishment.

```yaml
node_id: debian-01
resources:
  - id: example-catalog
    type: container
    adapter: docker
    target: example-catalog
    actions: [status, start, stop, restart]
```

`id`, `type`, `state`, supported `actions`, and optional metadata are protocol data. The `adapter` and `target` are agent-local configuration and are never selected by a controller command.

## 4. Transport and session lifecycle

HLCP v1 runs over one long-lived TCP connection protected by TLS 1.3 or newer. **mTLS is mandatory from the first implementation stage:** the controller validates the agent certificate and the agent validates the controller certificate. The negotiated ALPN value is `hlcp/1`.

```text
TCP/TLS connect
  → HELLO
  ← WELCOME
  → INVENTORY_SNAPSHOT + STATE_SNAPSHOT
  ↔ HEARTBEAT / COMMAND / ACK / RESULT / EVENT
connection loss
  → reconnect with bounded exponential backoff
```

On every new session, the agent sends a full inventory and state snapshot. The controller treats earlier dynamic state as stale until that synchronization completes.

The initial synchronization has strict order: `INVENTORY_SNAPSHOT` followed by `STATE_SNAPSHOT`. The controller validates both payloads and requires the state payload's `inventory_revision` to match the inventory just received. Only then can the controller mark the session ready for subsequent liveness and command processing.

The controller selects the highest mutually supported protocol version, returns a cryptographically random session ID, and sets the heartbeat interval and missed-heartbeat threshold in `WELCOME`. Both directions apply a bounded handshake timeout; on any invalid frame or payload, the session is closed without becoming registered.

## 5. Framing

Every frame has a fixed binary header and one encoded payload.

```text
0                   1                   2                   3
0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
| Magic (16)     | Version (8)   | Type (8)      | Flags (16)   |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
| Message ID (64)                                                |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
| Payload length (32)                                            |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
| Payload (variable, bounded)                                    |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

`Message ID` identifies an individual transport message. A command additionally carries a stable `command_id`, which survives retransmission and reconnect. The implementation must cap payload length before allocation and close a session after malformed framing or an unsupported mandatory protocol version.

Payload encoding is **CBOR** (RFC 8949). The Go reference implementation uses `github.com/fxamacker/cbor/v2`, canonical encoding, definite-length values, unique map keys, and bounded decoding. CBOR keeps the fixed framing independent from the payload schema while remaining smaller than JSON and practical to inspect during development.

Structured payloads use stable field names in v1. New optional fields may be added under the versioning rules; a recipient must reject malformed values and values exceeding its decoding limits.

## 6. Message types

| Group | Messages | Purpose |
|---|---|---|
| Session | `HELLO`, `WELCOME`, `GOODBYE`, `ERROR` | Version negotiation, identity, and session closure. |
| Liveness | `HEARTBEAT`, `PING`, `PONG` | Liveness and clock/diagnostic probes. |
| Synchronization | `INVENTORY_SNAPSHOT`, `STATE_SNAPSHOT`, `STATE_UPDATE` | Resource discovery and current state. |
| Command | `COMMAND`, `COMMAND_ACK`, `COMMAND_RESULT`, `COMMAND_QUERY`, `COMMAND_STATE` | Admission/execution and read-only recovery. |
| Observation | `EVENT`, `METRIC` | Optional asynchronous observations. |

The current prototype implements `HELLO`, `WELCOME`, `HEARTBEAT`,
`INVENTORY_SNAPSHOT`, `STATE_SNAPSHOT`, `COMMAND`, `COMMAND_ACK` and
`COMMAND_RESULT`, `COMMAND_QUERY` and `COMMAND_STATE`. `STATE_UPDATE` is required by completed v1. `GOODBYE`, `ERROR`,
`PING`, `PONG`, `EVENT` and `METRIC` are reserved by the v1 type registry but are
not yet supported behaviours; receipt is rejected until their contracts exist.

Unknown optional message types may be ignored only when their flag explicitly permits it. Unknown mandatory types produce `ERROR(PROTOCOL_ERROR)` and session closure.

### Snapshot payload contract

`INVENTORY_SNAPSHOT` is a complete replacement of the resource inventory for the session's node. Every resource contains only its general `id`, `type`, and allowed `actions`; adapter names and local implementation targets are not sent. The snapshot has a monotonically increasing agent-owned `revision`.

`STATE_SNAPSHOT` contains the matching `inventory_revision` and the agent's current observed state for its declared resources. The controller rejects a state snapshot whose revision does not match the latest validated inventory. This prevents state from one resource configuration being interpreted under another configuration.

## 7. Command lifecycle and reliability semantics

```text
Controller                         Agent
    COMMAND(command_id) ───────────→ durable deduplication check
    ←──────────── COMMAND_ACK       accepted or already known
                                     execute allowed action once
    ←──────────── COMMAND_RESULT    terminal success or failure
```

The controller retries the same `command_id` after an acknowledgement timeout. The agent retains a bounded command journal keyed by `command_id`:

- a new command is durably recorded as queued, ACK is written, then one worker
  durably records running before invoking the adapter;
- a duplicate while queued/running receives only `COMMAND_ACK` with `in_progress`;
- a duplicate after completion receives the cached terminal result;
- a command outside the registered resource/action allow-list is rejected without execution.

This produces **at-least-once delivery attempt with at-most-once execution within the retained journal window**. It does not claim global exactly-once behaviour: an unrecoverable agent crash between external side effect and durable journal update remains a fundamental ambiguity. V1 therefore limits actions to operationally idempotent resource lifecycle actions and documents the remaining risk.

`COMMAND` carries only `command_id`, `resource_id`, and `action`. `COMMAND_ACK`
reports `accepted`, `in_progress`, `completed` (cached) or `rejected`;
`COMMAND_RESULT` carries the terminal success flag, stable error code, and
diagnostic message. Neither payload contains a shell command, adapter target,
or implementation-specific service name.

F6 durably records command progress, keyed by node and command ID: immutable
payload, admission/deadline timestamps, ACK status, attempt count and terminal
result. Admission and each attempt are persisted before sending; ACK/result
updates are persisted before publishing them. It bounds active commands to 128. Default policy is a five-second ACK
window including sending, at most six attempts and a 30-second overall result
deadline. All delivery attempts (including disconnected/failed sends) consume
the budget. Only a missing ACK permits retransmission of the same command ID;
any valid ACK stops execution retries, including permanent rejection. A result
can arrive without an ACK and completes the wait directly.

Controller flags `--command-ack-timeout`, `--command-result-timeout` and
`--command-max-attempts` configure the policy. Limits are 1 minute, 24 hours and
100 attempts respectively; ACK timeout must not exceed the result deadline.
Caller cancellation/deadline also bounds sending. HTTP reports `ACK_TIMEOUT` or
`RESULT_TIMEOUT` (504), different-payload conflicts (409) and active
capacity exhaustion (429). Timeout does not prove the action was not executed.

The version-1 controller JSON ledger has a process lock and an 8 MiB byte limit,
with at most 1024 retained operations. Definitive results are protected for at least
24 hours from completion; unresolved operations and EXECUTION_UNKNOWN results
are never automatically evicted. Safe expired history is reclaimed on new
admission. Capacity exhaustion returns LEDGER_FULL (503); storage failure is
fail-closed with LEDGER_FAILURE (503). The command-line controller requires
--ledger-file (default /var/lib/hlcp/controller-operations.json); pathless trackers
exist only for embedded test fixtures.

HTTP same-ID/same-payload admission is idempotent; concurrent callers share the
same result, and completed results remain available even if inventory was removed.
A different payload is COMMAND_CONFLICT (409). Canceling the initial caller stops
further delivery attempts and preserves the operation. An attempted delivery is
uncertain until a durable result is known; zero attempted deliveries can produce
EXECUTION_NOT_STARTED. Client timeout never deletes the record, and a late result
is retained. Authenticated GET /v1/nodes/{node}/commands/{id} returns progress,
the fixed deadline, ACK/attempt metadata and any terminal result without sending.

After restart, unresolved records become unknown (expired if their deadline
elapsed), and **only read-only reconciliation** is allowed. Reconnect also polls
acknowledged/unresolved IDs through COMMAND_QUERY carrying the same fingerprint.
The agent's COMMAND_STATE response is not_found, queued, running, unknown or
completed with the cached result. Query never admits a missing command, starts a
queued job or grants execution permission. Missing/unknown history does not cause
replay; a fresh operator action needs a fresh ID. A late definitive result can
resolve the same expired operation. One query loop per authenticated session
sends at most 128 queries per round at the ACK timeout (minimum one second), and
joins at session shutdown. Uncertain entries may fill capacity; do not delete
journals or restore stale backups to work around replay protection.

These query/state types extend pre-release draft v1. Upgrade agent and controller
together; older binaries do not implement these message types. Linux is the
reference power-loss durability platform for both stores; Windows supports
locking and development validation.

The MVP uses a node-bound version-3 agent-local JSON journal protected by an
exclusive process lock. On Linux, persistence syncs the temporary file and the
directory after replacement. Newly created parent directories are synced too.
Windows provides locking and development-test support; Linux is the reference
platform for crash durability.

F5 migrates version-2 journals with a `.v2.bak` snapshot; legacy migration keeps
its `.v1.bak`. Do not downgrade against a version-3 journal or restore an old
backup after executing commands: that would lose replay protection. Older pending
entries without an execution phase recover as unknown. New queued entries recover
as `EXECUTION_NOT_STARTED`, while running entries recover as `EXECUTION_UNKNOWN`.
One worker accepts at most 16 jobs; excess admission returns `QUEUE_FULL` without
execution. Reader and heartbeats continue during actions. An action survives
session reconnect; shutdown cancels and joins execution. Adapter subprocesses have
a 20-second context, one-second pipe cleanup limit and 4096-byte output cap.

The default journal capacity is 1024 entries and its byte limit is 8 MiB. Terminal
results are protected for at least 24 hours from completion, regardless of command
volume. Pending and unresolved unknown results are never automatically removed.
When protected entries fill capacity, new admission fails with `JOURNAL_FULL`.
Expired terminal entries may be purged on subsequent admission. A clock rollback
keeps records protected longer. The configured count cannot be reduced below the
retained entry count during startup.

Missing journal files are initialized; existing empty, corrupt, unsupported or
node-mismatched files are rejected. Legacy journals are migrated only after
creating an exclusive `.v1.bak` backup and are protected from migration time.
After any storage failure, the running journal rejects new mutations until
recovery; a restart never silently erases history.
When the agent finds a record that was persisted as running after a restart,
it converts it to a terminal `EXECUTION_UNKNOWN` result rather than replaying
the potentially non-idempotent external side effect.

## 8. Heartbeat and failure detection

The negotiated configuration includes heartbeat interval `H` and missed-heartbeat threshold `N`. The controller changes a node from `ONLINE` to `DEGRADED` after a configurable warning threshold `D` and to `OFFLINE` after `N × H` without any valid inbound activity, where `0 < D < N`. Any valid message renews liveness, not only a heartbeat. A valid heartbeat carries an increasing agent-local sequence number; controller receipt time, rather than the agent clock, determines liveness.

Initial experiment levels: `H = 1 s, 5 s, 10 s`, with a fixed baseline `N` documented in the experiment plan. The controller records transition time, observed missed count, and reason. A disconnected node is not automatically restarted or remediated by v1.

The production session loop evaluates the next liveness transition with a timer
and preserves message order through one bounded inbound queue. Any valid heartbeat
or correlated command ACK/result updates last activity. A repeated or decreasing
heartbeat sequence ends the session as a protocol error. The default warning
threshold is two missed intervals and the offline threshold is three; transitions
and timestamps are published independently.

Replacing a node session atomically publishes its inventory and writer and closes
the previous owner. Disconnect time does not overwrite the last observed activity.
Writes have a five-second maximum including queue wait, with at most 16 admitted
writers. A failed partial write closes the stream. The controller admits at most
128 simultaneous connections by default, including handshakes.

## 9. Reconnect and synchronization

After a network failure, the agent reconnects using bounded exponential backoff with jitter. It performs a new `HELLO/WELCOME` exchange and sends full snapshots. Pending commands are reconciled by their `command_id`: the controller may resend unresolved commands; the agent returns the journal state or cached result. Old session IDs never authorize a new session.

Reconnect delay starts near one second and is capped at 30 seconds including
jitter. The delay resets only after at least one minute of synchronized session
operation. TCP connect and TLS handshake each have a ten-second limit. Runtime
actions currently have a 20-second local limit; subprocess pipe cleanup is bounded
by a further one second. On session shutdown the agent closes the socket and joins
its command reader, allowing an admitted action to finish its bounded execution.
Timeout of a Docker/Podman CLI does not prove the daemon did not perform the action;
F5 preserves this uncertainty as `EXECUTION_UNKNOWN`; F6 retains it durably and
reconciles existing command IDs without authorizing a second execution.

## 10. Security model

- TLS protects confidentiality and integrity in transit.
- mTLS binds a connection to a controller and an agent identity.
- Controller authorization is capability- and resource/action-based; an agent executes no arbitrary command string.
- Agent configuration defines the resource allow-list and adapter targets.
- Certificate files and private keys are deployment secrets and do not belong in this repository.
- Audit records include command ID, node, resource, action, timestamps, requester, and result.

Each development and HomeLab deployment uses a locally managed private CA. The controller receives its leaf certificate/key, a trust bundle for agent certificates, and a server name. Each agent receives its own leaf certificate/key and the controller trust bundle. Private keys remain outside version control.

## 11. Error model

An `ERROR` or failed `COMMAND_RESULT` contains `code`, human-readable `message`, and the relevant `request_id` or `command_id`.

V1 error codes: `UNSUPPORTED`, `INVALID_REQUEST`, `TARGET_NOT_FOUND`, `ACTION_NOT_SUPPORTED`, `PERMISSION_DENIED`, `TIMEOUT`, `EXECUTION_FAILED`, `PROTOCOL_ERROR`, `DUPLICATE_CONFLICT`.

Errors must be stable machine-readable codes; free text is diagnostic only.

## 12. Versioning and extensibility

Version 1 fixes the header format and core message semantics. Payloads may receive optional fields; recipients must ignore unknown optional fields. Incompatible header or semantic changes require a new major protocol version negotiated in `HELLO/WELCOME`.

## 13. MVP boundary

Required: controller, agent, CLI, TLS transport, identity-bound registration,
heartbeats, complete inventory/state sync and updates, command ACK/result
tracking, reconnect, durable deduplication journal, and Docker plus rootless
Podman standalone-container adapters.

Optional in v1: systemd adapter. Deferred: Docker Compose stacks, Kubernetes,
metrics collection, rich events, web UI, multi-controller high availability,
remote configuration changes, scheduling and any generic execution capability.
