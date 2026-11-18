# HLCP: controlled service management in a HomeLab

Martin Goffa — ANI-COM

18 November 2026

[Controller source at v1.1.0](https://github.com/goffamartin/homelab-control-plane/tree/v1.1.0) · [Measured agent source](https://github.com/goffamartin/homelab-control-plane/tree/720dcad18f0105f7fe65d33a1f55d4281a492a3c) ·
[Evaluation scripts and data](https://github.com/goffamartin/homelab-control-plane/tree/90b730d631fceba9b57f402b14ba6557e3ebad3b/evaluation/data/final-agent-720dcad)

## Abstract

HomeLab Control Plane (HLCP) gives an operator one interface to find a container, follow its output and invoke a permitted action. We deployed it for two applications. On a dedicated test container, median HLCP latency was 80.29 ms for live status and 165.31 ms for restart. Over an established SSH connection the same tasks took 66.69 and 159.41 ms. At one status request per second all 480 requests succeeded, and the agent used about 7% of one logical CPU.

## 1. Problem and research question

One application in our HomeLab spans three containers. A second runs in a single container on the same node. When investigating a fault, the operator can select an application tag, find the relevant container and read its logs in the web client. The CLI exposes the same inventory and allowed actions. For a restart, the controller assigns a command ID that the operator can use to look up the outcome later.

This deployment motivated two questions. How much time does the controller add to a live status check or restart compared with SSH? When an agent disconnects or a request exceeds its permissions, what does the operator actually observe? We timed the core CLI path on a dedicated test container. The deployed web client illustrates the workflow. Its response time and operator usability would require separate measurements.

## 2. Technical background

### Remote execution and connection reuse

SSH combines an authenticated encrypted connection with remote command execution.
Its performance depends partly on whether the client establishes a new connection
for each command. OpenSSH supports connection sharing through ControlMaster and
ControlPath, with ControlPersist controlling the master's lifetime [1]. The evaluation
therefore includes both fresh and multiplexed SSH. Comparing HLCP's persistent
agent connection only with fresh SSH would conflate connection reuse with application
protocol overhead.

### Transport authentication and application authorization

HLCP runs as an application protocol over mutually authenticated TLS. TLS protects
transport confidentiality and integrity and supports certificate authentication [2].
The application still needs to bind an authenticated identity to a node and decide
which resources and actions the caller may access. These checks belong to the
application policy; an encrypted connection alone does not grant permission to
restart a service. The controller's local management API has a separate credential
boundary from the controller–agent connection.

### Framing and data representation

A byte stream does not preserve application message boundaries. HLCP defines bounded
frames and typed messages for session setup, inventory, state, commands and results.
Its Go implementation uses CBOR, a binary data representation specified in RFC 8949
[3]. A compact representation can serve implementation goals, but this study does not
infer a wire-size or serialization speed advantage without a separate controlled
encoding experiment.

### Uncertain command outcomes

A caller can lose its connection after an operation starts and before the result
arrives. The missing response does not reveal whether the operation ran. HLCP assigns
a stable command identifier and persists command lifecycle information so a caller
can query the original result and duplicate requests can be recognized. An external
side effect and a journal update still have separate failure boundaries. A crash
between these steps can leave the outcome unresolved.

## 3. What we built and deployed

| Component | What the operator can do | Evidence from the HomeLab |
|---|---|---|
| Controller and agent | Discover a node and its resources, inspect allowed actions and filter containers by application tag. | The deployed controller listed three containers under one application tag and one under the other. |
| Container control | Request live status, start, stop or restart where configured. Look up the result by command ID. | The CLI returned the running state of a deployed application container. Every timed restart of the dedicated test container passed an independent inspection. |
| Logs and web client | Read recent lines, filter them or follow new output in the CLI. The web dashboard provides node and resource pages and a live log view. | In the CLI we followed new output, interrupted the reader, then retrieved the last five lines after stopping the test container. We also read logs in the deployed web client. |
| Installation and maintenance | Install Debian packages, pair a node, manage the agent service and complete commands in Bash. | After upgrading both ends to v1.1.0, the controller listed the agent online with the new version. |

### How a command runs

The Go controller accepts authenticated agents and exposes a local HTTP management
API. Agents publish configured resources and available actions. The CLI resolves a
selected node/resource/action and submits a command with a stable ID. The agent
executes the corresponding local adapter and returns a structured result. The
resource allowlist bounds the remote interface; scripts are locally configured named
actions, rather than arbitrary shell text supplied by a remote caller.

The container adapter invokes Podman for live observation and lifecycle operations.
Resource status performs `podman container inspect` on the agent. Inventory and node
status read the controller's current snapshot, so they represent a different task.
The latency comparison uses a resource status action to match direct SSH inspection.

The agent/controller persistence mechanisms support deduplication and retained-result
lookup. The implementation exposes uncertain states when it cannot establish an
outcome. This behavior matters for mutation safety even if it adds execution overhead.

## 4. Experimental method

The [evaluation protocol](README.md) and [run register](data/final-agent-720dcad/README.md) contain
the procedures and measured data.

All five experiments used controller v1.1.0 (revision `0f11951`) and agent revision `720dcad`, built with Go 1.27.3 for Linux amd64. The recovery and policy trials used disposable instances of these binaries.

| Parameter | Controller | Agent |
|---|---|---|
| Available processors (`nproc`) | 2 | 2 |
| OS-visible total memory (`free -m`) | 1906 MiB | 1906 MiB |
| Podman | Not part of controller role | 5.4.2 |

The agent also hosts ordinary application workloads. These remain outside
the experiment's mutation targets but contribute normal background load. Whole-VM
used memory is separate from the cgroup measurements. Detailed host and
storage characteristics were not captured for the final runs. Source provenance is in
[the initial inventory](environment/2026-11-03-user-inventory.json).

### Preparation and pilot

We checked both binary versions and used a dedicated container, `hlcp-bench`. Before timing anything, the pilot checked that HLCP and SSH under the same remote account could observe the running container. For restart, we also ran one pilot trial through each path before the final run.

### Experiment procedure and success criteria

| Experiment | Procedure | How we judged the outcome |
|---|---|---|
| E1 · Live status | We compared an established HLCP session, a fresh SSH connection per invocation and an established SSH master. After ten warm-ups per mode, we ran three blocks of 100 requests per mode and shuffled the order within each block. | A monotonic timer on the controller covered CLI launch through process return. We then checked command success and the live state `running`. We excluded warm-ups from the latency summary. |
| E2 · Restart | In each of three blocks, we issued ten restarts per mode. Before each trial the script waited two seconds, then used SSH to inspect the container ID, test label, state and start time. | We timed the restart command alone. An independent SSH inspection then had to find the same running container with a changed start time. |
| E3 · CPU and memory | We sampled both service cgroups once per second during three paired 120-second windows at idle and three at one live status request per second. | We divided the change in CPU time by elapsed time and reported memory per run from cgroup samples [4]. |
| E4 · Recovery | We started a disposable controller and agent on one VM over loopback. We stopped and relaunched the agent five times. In a second series, we paused and resumed the same process five times with its socket open. | Starting at interruption, we measured time to observe offline. Starting at relaunch or resume, we measured time to a new session and to the first successful live status. |
| E5 · Policy and results | On a disposable instance we tried an invalid token, missing and untrusted certificates, a disallowed restart, a conflicting command ID, result lookup and a repeated request. | We compared each API or TLS response with the expected refusal or retained result. Each case ran on three independent disposable instances. |

For each latency mode we counted successes, failures and timeouts. The median describes a typical successful invocation. The nearest-rank p95 is the time by which at least 95% of successful invocations finished. We independently recomputed the per-block and pooled status and restart summaries from the sample rows. [Run register and datasets](data/final-agent-720dcad/README.md)

## 5. Results

### Live status and restart

| Task and access path | Successful / attempted | Median (ms) | p95 (ms) |
|---|---:|---:|---:|
| Live status · HLCP | 300/300 | 80.29 | 92.54 |
| Live status · Fresh SSH | 300/300 | 179.10 | 192.59 |
| Live status · Multiplexed SSH | 300/300 | 66.69 | 74.81 |
| Restart · HLCP | 30/30 | 165.31 | 191.24 |
| Restart · Fresh SSH | 30/30 | 272.37 | 301.17 |
| Restart · Multiplexed SSH | 30/30 | 159.41 | 199.46 |

Opening a new SSH connection took the longest at the median for both tasks. With a shared SSH connection, the median was 13.60 ms below HLCP for status and 5.90 ms below HLCP for restart. All 90 restarts passed independent inspection of container identity, start time and running state. The timed command ends before that inspection. Application readiness would require an application-level probe.

### Resource use during polling

| Service | Idle CPU (%) | CPU at one request/s (%) | Loaded memory, median in each run (MiB) |
|---|---:|---:|---:|
| Controller | 0.052–0.054 | 0.47–1.01 | 11.52 / 14.48 / 15.46 |
| Agent | 3.00–3.03 | 6.90–7.11 | 11.91 / 12.83 / 12.88 |

All three runs completed 160 of 160 scheduled requests. CPU values are 120-second run averages as percentages of one logical CPU. Memory values cover the service cgroup. The highest sampled agent memory under load was 13.42 MiB.

![Service CPU at idle and under load](data/final-agent-720dcad/figures/cpu.png)

### Recovery and access policy

After a clean agent stop, the observer saw the node offline after a median of 8.26 ms. After relaunch the first successful live status took a median of 107.69 ms. When we paused the process with its socket open, the controller saw it offline after a median of 15.03 s. A new session and live status confirmed recovery in all five trials. After resume, time to the first successful status ranged from 1.05 to 15.86 s. Reconnect backoff can carry over between these successive short sessions.

TLS rejected connections with a missing client certificate and with a certificate from an unrelated authority. The API returned 401 for an invalid token, 422 for a disallowed restart and 409 for a conflicting command ID. A lookup and a repeated submission returned the same result as the original request. All three independent trials returned the expected responses.

## 6. Discussion and conclusion

HLCP exposed the permitted operations for two applications through a common interface and let the operator retrieve a completed command by ID. On this VM pair, live status took a median of 80.29 ms and restart 165.31 ms. With one live status per second the agent used about 7% of one logical CPU.

The SSH comparison distinguishes opening a connection from reusing one and measures all work between CLI launch and return. It covers one VM pair and one container. The recovery times apply to loopback and the configured heartbeat and backoff. The two-minute sampling windows describe resource use under the specified workload. Long-running and multi-node workloads would require separate experiments. [Data and verification procedure](data/final-agent-720dcad/README.md)

## References

1. OpenBSD. *ssh_config(5).* https://man.openbsd.org/ssh_config (accessed 2026-11-03).
2. Rescorla, E. *The Transport Layer Security (TLS) Protocol Version 1.3.* RFC 8446,
   2018. https://www.rfc-editor.org/rfc/rfc8446.html. Original TLS 1.3 specification.
3. Bormann, C., Hoffman, P. *Concise Binary Object Representation (CBOR).* RFC 8949,
   2020. https://www.rfc-editor.org/rfc/rfc8949.html (accessed 2026-11-03).
4. Linux kernel documentation. *Control Group v2.*
   https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html (accessed 2026-11-03).
5. Goffa, M. *HomeLab Control Plane*, v1.1.0, implementation source.
   https://github.com/goffamartin/homelab-control-plane/tree/v1.1.0.
