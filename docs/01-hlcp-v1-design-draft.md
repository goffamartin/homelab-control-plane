# HLCP v1 design draft

**Status:** Draft — decisions marked “open” require confirmation before the wire contract is frozen.

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
  - id: minecraft
    type: service
    adapter: systemd
    target: minecraft.service
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
| Command | `COMMAND`, `COMMAND_ACK`, `COMMAND_RESULT` | Request, acceptance, and terminal result. |
| Observation | `EVENT`, `METRIC` | Optional asynchronous observations. |

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

- a new command is recorded before execution;
- a duplicate while running receives `COMMAND_ACK` with `in_progress`;
- a duplicate after completion receives the cached terminal result;
- a command outside the registered resource/action allow-list is rejected without execution.

This produces **at-least-once delivery attempt with at-most-once execution within the retained journal window**. It does not claim global exactly-once behaviour: an unrecoverable agent crash between external side effect and durable journal update remains a fundamental ambiguity. V1 therefore limits actions to operationally idempotent resource lifecycle actions and documents the remaining risk.

**Open:** select the journal persistence strategy for the MVP. Recommended: a small local durable store on the agent, with an explicit retention period; an in-memory journal is unsuitable for reboot/reconnect testing.

## 8. Heartbeat and failure detection

The negotiated configuration includes heartbeat interval `H` and missed-heartbeat threshold `N`. The controller changes a node from `ONLINE` to `DEGRADED` after a configurable warning threshold and to `OFFLINE` after `N × H` without any valid inbound activity. Any valid message renews liveness, not only a heartbeat.

Initial experiment levels: `H = 1 s, 5 s, 10 s`, with a fixed baseline `N` documented in the experiment plan. The controller records transition time, observed missed count, and reason. A disconnected node is not automatically restarted or remediated by v1.

## 9. Reconnect and synchronization

After a network failure, the agent reconnects using bounded exponential backoff with jitter. It performs a new `HELLO/WELCOME` exchange and sends full snapshots. Pending commands are reconciled by their `command_id`: the controller may resend unresolved commands; the agent returns the journal state or cached result. Old session IDs never authorize a new session.

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

Required: controller, agent, CLI, TLS transport, registration, heartbeats, complete inventory/state sync, command ACK/result tracking, reconnect, durable deduplication journal, and one systemd adapter.

Deferred: Docker adapter, metrics collection, rich events, web UI, multi-controller high availability, remote configuration changes, and any generic execution capability.
