# Read-only container logs

Docker and Podman resources support bounded historical reads and live log
streaming through the controller. This is **not** stdin, terminal attachment,
remote shell or container exec. It cannot change the container. Native logs use
an ephemeral read channel, not durable command dispatch or result lookup.

## Enable explicitly

On the agent, edit the existing YAML with `hlcp-agent configure`:

```yaml
capabilities: [resource.control, podman]
resources:
  - id: hlcp-smoke
    type: container
    adapter: podman
    target: hlcp-smoke
    actions: [status, restart, logs]
```

For Docker, use `docker` for the adapter and runtime capability. Keep the rest of
your existing identity, TLS and journal settings. `logs` needs no `commands`
entry and no `script` capability. It is reserved on Docker/Podman, so a script
cannot override it. Explicit script/systemd commands named `logs` retain their
old ordinary-action semantics when no native-log flags are supplied.

Run `hlcp-agent check-config`, then use the normal explicit activation/restart
workflow. Validation alone neither applies configuration nor verifies runtime
access. After reconnect, check the resource inventory on the controller.
Only Linux agents with an enabled native log resource advertise
`resource.logs.v1` automatically; it need not be added to YAML manually.

## CLI

Run on the controller with your existing local API credential:

```sh
# Last 100 retained runtime lines, first page (up to 100 returned records).
hlcp -n debian-01 -r hlcp-smoke -a logs

# A larger capture, filtered literally, with an explicit UTC time window.
hlcp -n debian-01 -r hlcp-smoke -a logs --tail 500 \
  --since 2026-10-16T08:00:00Z --until 2026-10-16T09:00:00Z \
  --contains error --stream stderr --limit 50 --json

# Follow new output; tail 0 skips the initial history. Ctrl+C stops reading.
hlcp -n debian-01 -r hlcp-smoke -a logs --follow --tail 0

# Same live stream as versioned newline-delimited JSON.
hlcp -n debian-01 -r hlcp-smoke -a logs --follow --json

# Reuse next_cursor from the first history page, without repeating filters.
hlcp -n debian-01 -r hlcp-smoke -a logs --cursor "$CURSOR" --limit 50 --json
```

`--tail` is 0..1000 (default 100), `--limit` is 1..200 (default 100),
`--stream` is `both`, `stdout` or `stderr`. `--contains` is a case-sensitive
literal substring, at most 256 UTF-8 bytes, not a regex or shell expression.
Time filters require RFC3339/RFC3339Nano timestamps with a timezone; relative
values such as `10m` are not accepted. `since` must precede `until` when both
are supplied. Runtime tail/time selection happens before local text/pipe
filtering, so filtering may return fewer records than the requested tail.

History is a captured window, not an unbounded traversal of runtime storage.
The next page reads the same snapshot without querying the agent again. Cursors
are opaque, authenticated, credential/node/resource-bound and expire within
two minutes of capture (or sooner on eviction/controller restart). Only
`--cursor` and optional `--limit` may be combined for subsequent pages.
Follow cannot be combined with a cursor or page limit. `--dry-run` previews
the typed query without reading logs or testing runtime access.

Human output strips terminal control characters. JSON preserves log text as
escaped data; consumers must render it as text, never HTML or terminal escape
sequences. Logs may contain application secrets: scoped readers should be
trusted to see them. Messages and warnings go to stderr, JSON data to stdout.

Invalid CLI syntax/options exit 2; `--json` returns a safe `INVALID_OPTIONS`
error without echoing possibly sensitive input. History exits 0 for a completed/bounded window (`OK` or `BYTE_LIMIT`, inspect
`gap`/`truncated`), 5 for incomplete reads, and 4 for API/response errors.
Live interruption/limits exit 5; an intentional Ctrl+C exits 0. A successful
read says nothing about the application's health or completeness of retention.

## HTTP and machine output

Authenticated loopback endpoints:

```text
GET /v1/nodes/{node}/resources/{resource}/logs
GET /v1/nodes/{node}/resources/{resource}/logs/stream
```

History accepts `tail`, `since`, `until`, `contains`, `stream`, `limit` and
`cursor`. The stream accepts the same filters except `cursor`/`limit`.
Unknown, duplicate, empty or invalid query parameters are refused before
contacting the agent. No filesystem paths, executable names or arbitrary argv
are accepted. Existing Host/Origin, bearer, node/resource scope checks apply.
Both observer and operator roles can read within their scope; mutation rights
are not required. The agent independently checks its local resource allowlist.
Policy reload/credential revocation invalidates existing reads; streams recheck
permissions on records and at least every three seconds when idle.

History JSON has `schema_version: 1`, `stream_semantics: "runtime-pipes"`,
`captured_at`, `source_session`, `records`, optional `next_cursor`, `code`,
`gap` and `truncated`. Records have `timestamp` when available, `stream`,
`text` and `truncated`. Live output is `application/x-ndjson`, with
`schema_version: 1`, `stream_semantics`, `request_id`, `sequence`, `kind`
(`start`, `record`, `keepalive`, `end`), and applicable `record`, `code`, `gap`.
Agent events have contiguous positive sequence numbers; controller keepalives
and locally generated interruption/end events use sequence 0. They are not
durable command IDs or resumable runtime offsets. CLI-generated failure/end
events also use sequence 0; setup/HTTP errors use `kind: "error"` with a code.
Do not parse human wording. Inspect codes and flags; a closed connection without
a valid end means interrupted, never successful completion.

## Limits and honest continuity

| Boundary | Limit / behavior |
|---|---|
| Agent | 2 simultaneous log jobs/processes; history 15s, follow 10min |
| Source output | 8 MiB total stdout + stderr bytes, including filtered-out data |
| Record | 4096-byte captured line prefix; excess discarded and marked truncated |
| Transfer | About 100 records/s per job; bounded queues, not unlimited buffering |
| Controller | 16 reads total, 2 per node and 2 per credential |
| History capture | 1000 records or 512 KiB of encoded records |
| History page | Up to 200 records and 64 KiB of encoded records, plus JSON envelope |
| History cache | At most 8 snapshots, 2-minute TTL; memory only |
| Live response | 4 MiB of encoded agent events; 5s slow-writer deadline |

`BYTE_LIMIT`, `TIME_LIMIT`, `BACKPRESSURE`, `SESSION_LOST`, `FORBIDDEN` and
`INVALID_SEQUENCE` report why delivery stopped; `RUNTIME_FAILED` means the
runtime client failed, possibly after partial output. A non-OK terminal result
has `gap: true`. A long record can be `truncated` even when the overall result
is `OK`. Cache expiry and capacity errors are separate from runtime failures.
No automatic reconnect, replay guarantee, lossless buffering or exactly-once
delivery is promised. Start a new read explicitly after an interruption; it
may overlap or omit prior output. Cancellation kills only the log-reading CLI
client/process group, never the container.

The source is the runtime's retained logs and configured logging driver, not an
HLCP archive. See the official [Docker logs reference](https://docs.docker.com/reference/cli/docker/container/logs/)
and [Podman logs reference](https://docs.podman.io/en/stable/markdown/podman-logs.1.html).
The `stream` label identifies runtime-client stdout/stderr pipes, **not a
guaranteed original application stream**: TTY containers may merge output and
runtime diagnostic messages may appear on stderr. Timestamp-less continuation
lines are allowed. Literal filtering operates on the bounded captured prefix.

Log bodies never enter the command journal, operation ledger or HLCP telemetry.
Historical snapshots live briefly in bounded controller memory for pagination;
live data is only buffered in bounded transport queues. No new on-disk log store
is created. CLI/web clients may separately save the data under their own policy.

## Compatibility and verification

The existing CBOR protocol v1 gains feature-gated LOG_REQUEST, LOG_CANCEL and
LOG_EVENT messages (18..20). Command persistence formats do not change.
Upgrade the controller before agents. A new controller refuses native logs on
an older agent with `LOGS_UNSUPPORTED` and does not send it unknown frames.
An older controller never requests the new channel.

Automated coverage includes typed parsing/bounds, process output/cancellation,
pagination and credential-bound cursors, nonblocking backpressure, mTLS routing,
live policy revocation, explicit session loss, CLI JSON/NDJSON and isolation
from command persistence and telemetry. mTLS integration uses an injected log
backend; Linux subprocess tests exercise real pipe/process lifecycle. A separate
disposable Docker container smoke check verified timestamps, history and follow
arguments against the real Docker CLI. The container was auto-removed on exit.
These checks do not replace acceptance against real Docker/Podman containers on booted Debian
VMs. That live gate remains to be performed before claiming runtime acceptance.
