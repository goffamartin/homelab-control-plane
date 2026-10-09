# Implementation and evaluation roadmap

## Stage 0 — Freeze the design decisions

Confirm the payload encoding, command-journal persistence, development certificate approach, controller persistence boundary, and heartbeat thresholds. Record the choices in the HLCP v1 design before creating the wire implementation.

**Exit criterion:** a reviewed HLCP v1 document describes every MVP message and failure path.

## Stage 1 — Repository skeleton and protocol primitives

Create the Go module and separate packages for framing, encoded message payloads, and protocol validation. Add unit tests for valid framing, malformed lengths, unsupported versions, and message round-trips.

**Exit criterion:** protocol types are deterministic, bounded, and tested without networking.

**Current progress:** the module, executable entry points, binary frame implementation, CBOR codec, and unit tests have been created. `HELLO` and `WELCOME` now have bounded, validated CBOR payloads. The next message slice is inventory/state synchronization.

## Stage 2 — Secure session and registration

Implement TLS transport, `HELLO/WELCOME`, node identity validation, capability declaration, and controller-side active-session registry.

**Exit criterion:** one agent registers with a controller and an unauthenticated peer is rejected.

## Stage 3 — Inventory, state, and liveness

Implement inventory and state snapshots, regular heartbeat transmission, node state transitions, reconnect backoff, and stale-state handling after session loss.

**Exit criterion:** controller reports online/degraded/offline transitions and replaces stale state after reconnect.

**Current progress:** inventory/state synchronization, liveness tracking, a controller heartbeat loop, TCP listener, runnable agent, and in-memory node registry are implemented. The listener requires mTLS and ignores stale-session state updates after reconnect. The agent reconnects with bounded exponential backoff and jitter, performing a complete synchronization on every new session.

## Stage 4 — Controlled commands

Implement the allow-listed systemd adapter, command dispatch, ACK/result workflow, durable command journal, deduplication, and CLI read/control commands.

**Exit criterion:** a repeated command ID causes one execution and returns the same terminal result after reconnect.

**Current progress:** the agent uses a bounded, durable local command journal.
It records an accepted command before execution and returns the stored terminal
result for a duplicate command ID after agent restart. An incomplete record
recovered after restart is reported as `EXECUTION_UNKNOWN` and is never replayed.

## Stage 5 — HomeLab deployment

Package the agent as a single binary, configuration file, and systemd unit. Deploy the controller and an agent to isolated HomeLab nodes through WireGuard-only management access. Keep credentials and certificate private keys outside version control.

**Exit criterion:** documented installation works on the intended Debian VM and can control one non-critical test service.

## Stage 6 — Experiment harness

Create repeatable scenarios using Linux `tc netem`. Capture raw experiment data and the exact controller/agent configuration used in every run.

| Factor | Initial levels |
|---|---|
| Heartbeat interval | 1 s, 5 s, 10 s |
| Packet loss | 0%, 1%, 5%, 10% |
| One-way added delay | 0 ms, 50 ms, 200 ms |
| Retransmission policy | selected baseline plus one alternative |

Measure failure-detection time, false failure detections, command success rate, command completion latency, retransmissions, and bytes per node per minute. Perform repeated runs and report median plus tail percentile(s), not only the arithmetic mean.

**Exit criterion:** another student can repeat a scenario from the documented commands and obtain comparable data.

## Stage 7 — Analysis and report

Connect results back to the research question. Explain where the observed trade-off changes, which assumptions limit external validity, and which protocol choices were validated or falsified.

**Exit criterion:** the written report distinguishes measured evidence from design rationale and future work.
