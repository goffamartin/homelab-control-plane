> Historical design/evidence snapshot. Not an installation guide or a current release-readiness claim. Start at [current documentation](../README.md); current validation scope is in [validation](../validation.md).

# Recovery test evidence and remaining integration gates

This is evidence for the recovery and stress/fuzz F9 slices, not a claim that v1
or the whole v1 is complete. Build/CI status is tracked in the completion plan.
The reference deployment remains Linux/Debian; live Podman acceptance in F7 is
still open. No live Docker/Podman resource is modified by the tests below.

## Real CLI and mTLS end-to-end test

`internal/agent/e2e_test.go:TestE2ECLIWithMTLSAndDurableRecovery` builds and runs
the actual `cmd/hlcp` binary. It uses the production controller HTTP handler,
TLS listener, agent `Run`, sessions, retry policy, dispatcher, node-bound journal
and controller ledger. A disposable ECDSA CA issues server-auth and client-auth
leaves; the client has the expected HLCP URI SAN and both peers enforce ALPN.
Only the external Docker command runner is fake. Read-only observations do not
increment its side-effect counter; `restart catalog` increments it exactly once.

The test checks:

- authenticated registration and a fresh typed observation;
- CLI dry-run leaves controller ledger and agent journal byte-for-byte unchanged;
- CLI resource execution returns the real terminal result;
- durable ACK and one attempt are observed before disconnect injection;
- controller shutdown drops TLS after the external effect but before RESULT;
- the agent durably finishes without a controller and the original CLI does not
  falsely report success;
- both controller and agent lifecycles are stopped/joined and recreated with the
  same persistence files (fresh registry/session, new listener);
- reconciliation recovers the successful result with the original attempt count;
- CLI read-only lookup, four concurrent retained-result callers and a conflicting
  same-ID payload never add another side effect.

Restart here means recreation of the production service lifecycles in one test
process, not killing two OS processes. The CLI is a separate real process.
Abrupt-exit persistence is separately covered by controller ledger and agent
journal subprocess tests. The test does not claim universal exactly-once behavior
across an unrecoverable crash between external effect and durable completion.

All subprocesses have deadlines, services join on cleanup, and files/keys use
test-owned temporary directories. On Windows the journal completion observer
uses file metadata before reading the replaced file: opening a destination during
`MoveFileEx` can itself cause persistence failure. This avoids injecting an
unintended storage fault into the network-loss scenario. Actual durable
completion failures are now logged and remain fail-closed/unknown.

Run from the repository root with an installed Go toolchain:

```text
go test -run TestE2ECLIWithMTLSAndDurableRecovery -count=3 -timeout=120s ./internal/agent
go test -count=1 -timeout=120s ./...
go vet ./...
go test -race -count=1 -timeout=120s ./...
```

The CLI build is offline (`GOPROXY=off`, `GOSUMDB=off`, local toolchain), so module
dependencies must already be cached. The race command requires a supported CGO
toolchain; the project is validated in an isolated Linux Go container.

Validation for this slice: three consecutive Windows E2E runs passed, followed
by full Windows tests and vet and full Linux `go test -race` (all packages).

## Existing targeted failure evidence

| Boundary | Automated tests |
|---|---|
| ACK ordering, reconnect deduplication, queue saturation | `internal/session/agent_commands_test.go`, `internal/controller/retry_test.go` |
| Disk failure and incomplete journal recovery | `internal/command/journal_safety_test.go`, `internal/command/lifecycle_test.go` |
| Abrupt OS-process exit and ownership locks | `internal/command/journal_safety_test.go`, `internal/controller/ledger_test.go` |
| Late results, caller cancellation, query-only restart, same-ID conflict | `internal/controller/ledger_test.go` |
| Foreign node/stale writer/session/revision rejection | `internal/controller/sessions_test.go`, `internal/controller/resource_state_test.go`, `internal/session/session_test.go` |
| CLI streams, exit codes, selectors and integer-safe JSON | `cmd/hlcp/main_test.go`, `cmd/hlcp/output_test.go`, `internal/cli/parser_test.go` |

## Sustained pressure and reconnect lifecycle

`TestAgentFullQueueStillAcknowledgesDuplicatesAndRejectsExcess` fills the 16
admitted-job/result slots with a blocked executor. It then exchanges 128 duplicate
commands and 128 excess commands: duplicates remain `in_progress`, every excess
command receives a correlated `QUEUE_FULL` result, and no excess ID is admitted
to the journal. Socket deadlines bound stalls. This is sustained saturation,
not a benchmark or a claim of unlimited throughput.

`TestRepeatedReconnectJoinsCommandWaitersAndInboundReaders` reconnects 64 times
around one pending command. Each connection joins its agent reader/result waiters
and controller inbound reader and releases its writer reservations before the
next iteration. The final session gets the successful result, with exactly one
executor invocation. Explicit joins test lifecycle ownership without relying on
noisy process-wide goroutine counts; they do not prove absence of every possible
memory leak or cover 64 live TLS handshakes.

## Bounded fuzz campaigns

The checked-in seed corpus runs with ordinary `go test`. Mutation campaigns use
two workers and a configured five-second budget per target. On 2026-10-12:

| Target | Environment | Executions | Result |
|---|---|---:|---|
| `FuzzFrameDecode` | Windows | 250819 | Passed |
| `FuzzCommandPayloadDecode` | Windows | 242795 | Passed |
| `FuzzProgressiveParser` | Windows | 324906 | Passed |
| `FuzzJournalRecovery` | Linux tmpfs | 38279 | Passed |

Frame and payload fuzzing check accepted-input round trips; CLI fuzzing checks
determinism and selected-parent/read-only lookup invariants. Journal fuzzing uses
the actual file loader: rejected input must remain unchanged and accepted input
must survive close/reopen. The Windows journal campaign also passed but managed
only nine executions due to file-system overhead, hence the Linux repetition.
These short campaigns found no failing input; they are not exhaustive validation
or a security audit. They cap mutation-input sizes and run offline.

Example (change package and target for the other campaigns):

```text
go test ./internal/protocol -run '^$' -fuzz '^FuzzFrameDecode$' -fuzztime 5s -parallel 2 -timeout 60s
```

The new stress and seed tests passed three repeated Windows runs. Full Windows
tests/vet and full Linux race validation are the regression gate for this slice.

## Build gate and remaining runtime acceptance

- Build, CI, version metadata, checksums and dependency/security review are now
  implemented; see `07-build-and-release.md` and the completion plan for hosted
  verification status.
- Keep live Docker/Podman runtime acceptance separate; fake runners do not close
  the missing live Podman gate.
