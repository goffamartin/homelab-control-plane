# HLCP: controlled service management in a HomeLab

Martin Goffa — ANI-COM

**Working draft, 3 November 2026. Experimental results and conclusions are pending.**

Code repository: https://github.com/goffamartin/homelab-control-plane

Before submission, replace this general link with a permalink to the commit containing
the final evaluation scripts and sanitized raw data. This local draft is not yet published.

## Abstract

HLCP is a controller–agent application protocol for managing explicitly configured
HomeLab resources. The implementation provides resource discovery and named actions
through a central interface, with authenticated transport and persistent command
identifiers. This report examines its execution overhead relative to SSH and its
behavior during connection failures and rejected requests. **Complete this abstract
with measured findings after the experiments; no performance conclusion is available yet.**

## 1. Problem and research question

A HomeLab can contain several services with different management commands and access
paths. SSH provides a flexible remote interface, but the operator still needs to know
the relevant host, runtime and command. HLCP exposes configured resources and their
allowed actions through a consistent controller interface. The implementation also
has a separate web client; the evaluation focuses on the core controller and agent.

The research question is: **What execution overhead does HLCP introduce compared
with direct SSH management, and how does it behave when connections fail or requests
violate its access policy?**

The study uses a real HomeLab with a controller VM and a Debian agent managing a
dedicated rootless Podman test container. It evaluates one topology and implementation
version. It does not establish a general usability advantage or universal security
guarantee.

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
side effect and a journal update still have separate failure boundaries. The report
must describe tested recovery cases and preserve unknown outcomes rather than
claiming unrestricted exactly-once execution.

## 3. Implementation

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

The complete planned protocol is in [evaluation/README.md](README.md). Before final
submission, replace prospective statements here with the exact method actually used.

The operator supplied an initial environment inventory on 3 November 2026. Both
binaries report v1.1.0 revision `0f119514b1739652d709aee7b07db742d44dc0a0`, built with
Go 1.27.3 for Linux amd64. This is preparation evidence, not a performance result.

| Parameter | Controller | Agent |
|---|---|---|
| Available processors (`nproc`) | 2 | 2 |
| OS-visible total memory (`free -m`) | 1906 MiB | 1906 MiB |
| Podman | Not part of controller role | 5.4.2 |

The agent also hosts ordinary application workloads. These remain outside
the experiment's mutation targets but contribute normal background load. Whole-VM
used memory is not a measurement of HLCP memory consumption. Exact kernel/CPU
details, SSH configuration and the pinned test-container image remain to be recorded
before a final series. Source provenance and limitations are in
[the initial inventory](environment/2026-11-03-user-inventory.json).

The primary comparison measures CLI invocation to a validated terminal result on the
controller using a monotonic clock. The three paths are HLCP with an established agent
session, fresh SSH and SSH over an established master connection. All inspect the same
running test container under the same agent-side user. Warm-up samples are marked
separately. Mode order changes between repeated blocks with a recorded random seed.

The report will include every attempted request, report failures separately, and
summarize successful latencies using the median and p95 with the sample count. The
measurement includes process startup, local API work, persistence, transport and
runtime work. It does not isolate network-only latency.

Service CPU and memory measurements cover idle and specified request loads, with
separate controller and agent accounting. Cgroup CPU time and memory are defined by
the Linux accounting interface [4]; charged cgroup memory must not be mislabeled RSS.
Recovery experiments distinguish clean disconnects from silent communication loss.
Policy experiments compare expected rejection with observed behavior and independent
evidence of whether an action actually executed.

## 5. Results

**Pending experiments.** Include the final environment/version table, latency
distributions with success/failure counts, service CPU/memory series, a recovery
timeline and the policy test matrix. Link every figure to its raw input and analysis
command. Implementation test counts are not substitutes for these measurements.

## 6. Discussion and limitations

**Complete after observing results.** Discuss the measured trade-off between execution
cost and the bounded management interface. Account for connection reuse, background
load, Podman subprocesses, polling uncertainty, durability settings and differences
between command completion and readiness. One VM pair does not demonstrate fleet
scalability. Sequential samples can share scheduling and cache effects. Functional
rejection tests do not constitute a complete security assessment.

## 7. Conclusion

**Pending results.** Answer the research question with measured quantities and state
which reliability/policy behaviors the experiments actually demonstrate. Include
negative findings and unresolved outcomes. Separate the implemented system, observed
evidence and future extensions.

## References

1. OpenBSD. *ssh_config(5).* https://man.openbsd.org/ssh_config (accessed 2026-11-03).
2. Rescorla, E. *The Transport Layer Security (TLS) Protocol Version 1.3.* RFC 8446,
   2018. https://www.rfc-editor.org/rfc/rfc8446.html. Original specification; verify
   current updates when finalizing the bibliography.
3. Bormann, C., Hoffman, P. *Concise Binary Object Representation (CBOR).* RFC 8949,
   2020. https://www.rfc-editor.org/rfc/rfc8949.html (accessed 2026-11-03).
4. Linux kernel documentation. *Control Group v2.*
   https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html (accessed 2026-11-03).
5. Goffa, M. *HomeLab Control Plane*, v1.1.0, implementation source.
   https://github.com/goffamartin/homelab-control-plane/tree/v1.1.0.
