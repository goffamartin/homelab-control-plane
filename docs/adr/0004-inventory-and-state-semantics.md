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

F7 replaces YAML `state` with runtime observation. Old configuration using that
field is rejected rather than silently trusted; remove it when upgrading. Initial
snapshots contain `unknown/NOT_OBSERVED` with a zero timestamp, always stale.
Observation starts asynchronously after synchronization, so a slow runtime cannot
block registration or heartbeats. The default `observation_interval` is 10 seconds
(configurable 1s–5m). Refresh requests after actions coalesce in one slot; there
is no growing backlog. Four concurrent observers share a bounded result channel,
each with a three-second context plus the subprocess's one-second pipe cleanup.
Rounds do not overlap, and all workers join on session shutdown.

The interval schedules rounds; it is not a guaranteed per-resource latency when
a round is slow. For N resources the runtime-query bound is ceil(N/4) * 4 seconds,
in addition to network sending. A resource is refreshed on the next round, not
optimistically assigned a desired state after a command. Choose freshness limits
appropriate to resource count/runtime latency; stale remains visible if a refresh
cannot finish in time.

`STATE_UPDATE` includes authenticated session ID, inventory revision, an increasing
per-session update revision, and one observation (state, observed_at, code,
diagnostic up to 4096 bytes). Known resources only; older/repeated revisions and
old sessions cannot overwrite a new session. Reconnect resets revisions and
unobserved state. Controller `--resource-state-max-age` defaults to one minute
(1s–24h). Freshness uses controller receipt time, not an agent wall clock; all
non-online observations are stale. The last known state is retained, not erased.

Container scope is one standalone container, never an implicit Compose or
Kubernetes stack. `container inspect` prevents an image with the same name being
mistaken for the resource. Running maps to running; created/configured/initialized/
exited/stopped map to stopped. Paused/transitional/unrecognized states are unknown,
and dead is error. Systemd observes LoadState and ActiveState, distinguishing an
inactive unit from a missing unit. Read-only status succeeds for stopped resources.
Missing target, executable/daemon, permissions and timeout report bounded errors;
runtime diagnostic-text classification is best effort with a generic safe fallback.

Runtime contracts: [Docker container inspect](https://docs.docker.com/reference/cli/docker/container/inspect/)
and [Podman container inspect](https://docs.podman.io/en/stable/markdown/podman-container-inspect.1.html).
Live Docker start/restart/stop/status and external-stop smoke is verified locally;
Podman mapping/argument/error tests are fake-runner verified, but live Podman
acceptance remains outstanding because no Podman executable is available here.
