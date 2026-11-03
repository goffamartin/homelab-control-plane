# Semester evaluation

Status: protocol prepared on 2026-11-03; **no HomeLab measurements have been collected**.
The report and presentation must distinguish implementation evidence, automated tests
and actual experiments. No numbers in this directory represent HomeLab results yet.

Deliverables: a concise English report, code and raw data in this Git repository,
and a 10–15 minute presentation. The original proposal's evaluation slide commits
to latency against SSH, idle/load CPU and memory, recovery, and access rejection.

## Research question

**What execution overhead does HLCP introduce compared with direct SSH management,
and how does it behave when connections fail or requests violate its access policy?**

This question evaluates an implementation on one HomeLab topology. It does not
assume that HLCP will be faster, prove cryptographic security, or measure usability
without a user study. The web UI demonstrates the implementation; exclude its
rendering and polling from the protocol comparison.

## Experiment boundaries

- Driver: controller VM, using its CLI profile; agent: Debian VM under its actual
  rootless Podman owner. Record current versions and VM allocations before testing.
- Dedicated target: `hlcp-bench`. Never restart Kostka, Medaile or Cloudflared for
  this study. Preserve their configuration and do not clear journals or ledgers.
- Pin the container image by digest and record its image ID, command and stop timeout.
  A small idle container is sufficient; image pulls and installation are outside timing.
- Fault injection and untrusted certificate tests require a disposable controller/agent
  instance or a dedicated VM. Do not apply netem, stop the production agent, or replace
  production credentials as part of unattended tests.
- Freeze the core version and configuration for a complete experiment series. The
  inspected source is v1.1.0, commit `0f119514b1739652d709aee7b07db742d44dc0a0`;
  verify the running binaries rather than assuming they match.
- Close web dashboards and log streams during the primary comparison. Record normal
  application background load, Podman timers and any deployments during the run.
  Do not disable unrelated production services to improve results.

## Experiments

| ID | Question and conditions | Repetitions | Recorded evidence |
|---|---|---|---|
| E1 | Live container status: HLCP vs fresh SSH vs multiplexed SSH | Pilot: 10/mode; final: 3 blocks × 100/mode, after 10 warm-ups/mode | End-to-end latency, terminal result, exit code, failures/timeouts, run/block/order, command ID |
| E2 | Same container restart via the three paths | After E1; 3 blocks × 10/mode, with readiness/reset checks | Command completion separately from independently verified running/readiness state; no retries after ambiguous mutations |
| E3 | HLCP idle and controlled status load | 3 × 120 s idle; 3 × 120 s at 1 request/s; optionally 5/s after pilot | Per-service CPU time and cgroup memory, completed operations/s, failures and queue delay |
| E4 | Orderly disconnect and unannounced communication loss, then reconnect | 5 runs per fault, on disposable instances | Injection marker, last valid activity, observed state changes, session replacement, time to fresh inventory and a successful status |
| E5 | Trust, allowlist and duplicate handling | At least 3 independent runs/case | Expected and observed result, command ID, agent-side execution counter or equivalent evidence |

Final counts are a predeclared feasible target, not a claim of statistical power.
Pilot runs diagnose configuration and timing only; keep them separate from final data.

### E1: equal work and fair SSH baselines

Compare `hlcp -n NODE -r hlcp-bench -a status --json` with
`podman container inspect --format '{{.State.Status}}' hlcp-bench` over SSH as the
same agent-side user. Confirm the YAML resource maps to that exact container.

Use the **resource action** status, which executes an observation on the agent.
`hlcp -n NODE`, resource inventory, and node status read cached controller data and
cannot serve as the live SSH comparison.

1. HLCP uses the existing controller–agent session; each sample launches a new CLI.
2. Fresh SSH disables connection multiplexing. DNS/key selection/authentication
   and remote shell startup remain part of this operator workflow.
3. Multiplexed SSH first establishes a private master connection outside timing.
   Each timed invocation requests a new channel on that master. Force `ControlMaster=no`
   on clients and `ProxyCommand=false` as a fail-closed fallback if the socket is lost.
   Confirm the master before/after a block; never silently count a fresh connection
   as a multiplexed sample. Close only the master created by this experiment.

The Python driver measures with a monotonic clock on the controller from process
launch until exit, parses output, and requires the expected terminal running state.
It uses a fixed seed to shuffle mode order within each block, logs warm-ups separately,
and never automatically resubmits an ID. A timeout is a recorded failure, not a slow
successful sample. No password prompts or host-key bypasses are allowed in timing.

This measures **operator-visible command latency**, including CLI/process startup,
HTTP loopback, dispatch, persistence, Podman and SSH overhead. It does not isolate
CBOR serialization or network-only round-trip time. Configuration differs between
HLCP's bounded action interface and an SSH shell; discuss this functional difference.

### E2: command completion and application readiness

A successful restart means the runtime command completed. It does not prove the
application is ready. Measure these endpoints separately and use the same readiness
probe/interval across all paths. An idle test container only supports a running-state
check; do not label that an application health check. Allow stabilization between
samples, preserve the same initial state and record StartedAt/PID before and after.

This first driver intentionally supports **status only**. Implement the restart
experiment after the E1 pilot confirms configuration. Do not replace status with
restart inside an unattended timing loop.

### E3: resource use

Collect controller and agent service cgroups separately at 1 s intervals. Derive
CPU% = 100 × delta usage_usec / delta elapsed_usec, where 100% is one CPU core.
Also report allocated vCPUs. Record `memory.current` in MiB and its sampled maximum;
this is charged cgroup memory, not process RSS. Read `/proc/PID/status` separately
if the report needs VmRSS, and label it accordingly.

Include subprocesses that remain in the service cgroup; document any runtime processes
placed elsewhere by Podman. Do not present whole-host memory as agent memory or
claim comparable SSH resource costs from daemon-only measurements. E3's minimum
scope is HLCP's service footprint. A cross-tool CPU comparison needs equivalent
accounting of CLI, SSH server/session and runtime processes on both machines.

Use monotonic timestamps per host. Fix offered load before the run, record actual
submission/completion times, concurrency and missed scheduling deadlines. Sequential
latency samples are not a fixed-rate load generator. Include scheduled delay so an
overloaded driver cannot hide long waits by submitting fewer requests.

### E4 and E5: reliability and policy

Treat a clean agent stop and silent packet loss as different conditions. A clean
socket close need not pass through the same degraded/offline sequence as missed
heartbeats. Record the actual negotiated heartbeat policy and poll interval. Report
state transition timestamps as observed intervals, not exact internal detection times.

After restoration, require a new valid session, fresh inventory and a successful
status before declaring recovery. Collect all timestamps for each trial on one
observer host; state the uncertainty from polling and injection acknowledgment.

E5 cases: certificate signed by an untrusted CA; missing client certificate; denied
node/resource scope; action absent from the allowlist; same ID and same payload;
same ID and conflicting payload; retained-result lookup after a reconnect. Use a
dedicated task with an agent-side counter to verify actual execution count. A repeated
response alone does not demonstrate deduplication. Never claim universal exactly-once
execution through a crash between an external side effect and durable result storage.
These are functional controls tests, not a penetration test or cryptographic proof.

## Analysis rules

Retain every scheduled/measured sample, including failures, in append-only raw files.
Report attempts, successful samples, failures/timeouts and success rate together with
median and nearest-rank p95 latency of successful samples. Provide per-block summaries
as well as pooled values; sequential samples can be correlated. Do not count all
requests as independent experimental replications or infer causality from one host pair.
Do not remove outliers because they make a chart less attractive. Explain invalid runs
and retain them with an exclusion reason. Freeze a new protocol version if the method changes.

Store CPU/RAM series and recovery event timelines separately from command samples.
Useful plots: E1 latency distributions with failures alongside; E3 CPU/memory time
series; E4 event timeline; E5 expected/observed table. A finding may favor SSH.

## Artifacts and execution order

- [Pilot runbook](pilot-runbook.md): setup and commands with placeholders for private
  host/account details, including a preflight stop if SSH is not ready.
- `report.md`: English working draft; results and conclusions explicitly pending.
- `presentation-outline.md`: 12-minute results talk, extended by discussion to 15 minutes.
- `scripts/collect-environment.sh`: read-only environment inventory, no config/token dumps.
- `scripts/measure_status.py`: E1 pilot/final runner, Python standard library only.
- `scripts/analyze_status.py`: descriptive E1 summary, preserves failure counts.
- `data/`: raw files and metadata; currently contains no experiment results.

1. Run environment collection on both VMs and confirm topology/version/resource scope.
2. Provision `hlcp-bench` and authorized SSH access; keep keys outside the repository.
3. Run the E1 pilot and inspect raw data before approving the final series.
4. Collect E1 and E3; implement E2 and disposable E4/E5 scenarios as separate scripts.
5. Freeze raw datasets, verify checksums and commit sanitized data/code to this repository.
6. Fill the report with measured findings, then create charts and the results deck.

### First commands on the VMs

Run these after copying this evaluation checkout to the VM. No SSH keys are
generated or installed by the scripts, and no configuration is changed.

On the controller, as the existing HLCP CLI user:

```sh
umask 077
sh evaluation/scripts/collect-environment.sh controller > controller-environment.txt
```

On the agent, as the user owning the rootless containers:

```sh
umask 077
sh evaluation/scripts/collect-environment.sh agent > agent-environment.txt
```

Inspect and sanitize these inventories before publishing them. Save the original
private files separately and describe any redaction in the public run metadata.

After confirming a running `hlcp-bench` container, the matching HLCP resource and
noninteractive authorized SSH from controller to agent, run the pilot on the controller:

```sh
python3 evaluation/scripts/measure_status.py \
  --node debian-01 --ssh-target martin@AGENT_IP \
  --blocks 1 --samples 10 --warmups 2 \
  --out evaluation/data/e1-pilot-01
python3 evaluation/scripts/analyze_status.py \
  evaluation/data/e1-pilot-01/samples.csv \
  > evaluation/data/e1-pilot-01/summary.csv
```

Replace `AGENT_IP` with the verified address. If needed, add `--ssh-identity /path/to/key`
or `--hlcp-config /path/to/cli.json`; these are paths, never credential contents.
The runner ignores SSH config aliases (`-F /dev/null`), requires a known host key,
and refuses existing run directories. The local user's normal SSH agent/default keys
remain available unless an explicit identity is selected. Verify that the matching
public key is authorized on the agent before collecting data.

Read `metadata.json` before interpreting results: an interrupted run remains
`incomplete` and must not silently count as a completed experiment. The CSV stores
measurements and outcome validation, not raw diagnostics or token values. Five failed
samples stop the series. The analysis intentionally reports conditional latency for
successful samples with failures alongside; timeouts are not included in its p95.

After a successful pilot, use `--blocks 3 --samples 100 --warmups 10` and a **new**
output directory for the final E1 series. The final workload is 900 measured requests
across three paths plus 30 warm-ups. It creates 310 new HLCP status IDs, which consume
journal/ledger capacity; check remaining capacity without clearing production records.
Record actual test-container image, user and resource-to-target mapping alongside
the generated metadata; the controller's public resource descriptor does not expose
the local YAML target.

Synthetic checks, without contacting either VM:

```sh
python3 -m unittest discover -s evaluation/scripts -p 'test_*.py'
```

Published data must exclude tokens, private keys, authentication cookies and secret
application output. Record redactions in metadata; do not edit timing values. Link the
final report to a specific public repository commit containing code and sanitized data.
Versioned data samples must not be empty stand-ins presented as measurements.

## Technical sources

- [OpenSSH configuration](https://man.openbsd.org/ssh_config): connection multiplexing.
- [Linux cgroup v2](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html): CPU/memory accounting.
- [CBOR, RFC 8949](https://www.rfc-editor.org/rfc/rfc8949.html): data representation.
- [TLS 1.3, RFC 8446](https://www.rfc-editor.org/rfc/rfc8446.html): original TLS 1.3 specification;
  check current updates/status when completing the bibliography.
- HLCP source: `internal/adapter/container.go`, `internal/adapter/observation.go`,
  `internal/controller/http.go`, `internal/liveness/tracker.go`, `internal/protocol/`.
