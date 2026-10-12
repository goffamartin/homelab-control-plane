# HLCP v1 acceptance contract

This matrix defines what must be demonstrated before v1 is considered complete.
Unit coverage alone does not satisfy a scenario that names a real process,
runtime, filesystem or network failure.

| ID | Required v1 behaviour | Acceptance evidence | Phase |
|---|---|---|---|
| A01 | Agent identity is bound to its mTLS certificate | Valid node registers; foreign node ID, CA, EKU, expiry and ALPN fail | F2 |
| A02 | Local API is accessible only to an authorized local CLI | Loopback/token positive and negative integration tests | F2 |
| A03 | Session replacement is atomic | Reconnect closes the old owner; stale messages cannot mutate current state | F3 |
| A04 | Liveness follows valid activity and ordered heartbeats | Online/degraded/offline and repeated-sequence tests on the production loop | F3 |
| A05 | Network and process shutdown are bounded | Blackhole, full queue, cancellation and graceful-stop tests leave no blocked worker | F3 |
| A06 | Journal survives supported crash/restart scenarios | Corruption, storage-failure, lock, migration and restart tests on Linux | F4 |
| A07 | Retention preserves the declared replay window | Capacity/time tests evict only safe entries and otherwise return `JOURNAL_FULL` | F4 |
| A08 | ACK precedes an external side effect result | Slow fake executor proves ACK arrives while action is still running | F5 |
| A09 | Duplicate command executes once | Fault injection before/after ACK, side effect and result keeps executor count at one | F5–F6 |
| A10 | Command outcome survives client/controller interruption | Lookup by command ID returns completed or explicit unknown after reconnect/restart | F6 |
| A11 | Docker and Podman state is observed, not configured | External stop/start appears in controller and CLI within the freshness bound | F7 |
| A12 | Approved CLI grammar is complete and unambiguous | Table-driven parser/API/E2E tests cover short, long, list, detail, dry-run and errors | F8 |
| A13 | Build quality gate covers concurrency and malformed input | Linux race tests, focused fuzzing, vet and reproducible binary builds pass | F9 |
| A14 | Debian deployment is repeatable | Clean install, reboot, certificate rotation, upgrade and rollback are recorded | F10 |
| A15 | Research question is answered by repeatable measurement | Another operator reproduces netem scenarios and comparable raw results | F11 |

## Product boundary

Docker and rootless Podman standalone containers are the primary v1 deployment
scenario. Supported actions are `status`, `start`, `stop` and `restart`, when
enabled per resource. The systemd adapter may remain supported, but it is not a
prerequisite for the first product acceptance run.

Minecraft-specific operations, arbitrary commands, Docker Compose stacks,
Kubernetes, a web UI, remote configuration, scheduling and multi-controller HA
are outside v1.

## Management-node extension acceptance

The [management and MCP plan](08-management-node-and-mcp-plan.md) expands the F10
operational preparation and separately defines read-only MCP, bounded remote log
queries and later mutation/web extensions. Existing acceptance criteria are not
evidence that these extensions already exist. Scoped controller authorization must
be verified before an adapter receives credentials; hosting an endpoint is not
proof of actual client connectivity. A14 still needs real Debian deployment and
A11 still needs live rootless Podman evidence. A15 does not depend on deferred
mutating MCP or web UI.
