> Historical design/evidence snapshot. Not an installation guide or a current release-readiness claim. Start at [current documentation](../README.md); current validation scope is in [validation](../validation.md).

# Project vision and scope

## Working title

**HomeLab Control Plane: Design and Evaluation of a Lightweight Application-Layer Protocol for Infrastructure Management**

Český pracovní název: **Návrh a vyhodnocení lightweight aplikačního protokolu pro správu domácí infrastruktury**.

## Problem statement

Small HomeLabs often contain independently operated hosts and services. Existing products can solve this problem, but they may introduce a large runtime footprint, a broad operational model, or a protocol whose reliability behaviour is hidden from the operator. HLCP is a deliberately narrow reference system that makes the control-plane communication explicit and measurable.

## Research question

> How do heartbeat interval and retransmission strategy affect failure-detection time, command reliability, and communication overhead in a lightweight HomeLab control plane?

The implementation is not the research result by itself. The result is a reasoned protocol design together with reproducible measurements of the stated trade-offs.

## Goals

1. Specify an extensible application-layer protocol for a controller and outbound-connected agents.
2. Implement a controller, an agent, and a CLI in Go.
3. Let agents report registered resources and run only explicitly supported actions on them.
4. Reliably correlate commands, acknowledgements, and terminal results across loss and reconnects.
5. Detect unavailable nodes through configurable liveness monitoring.
6. Secure controller-agent transport with TLS; make mutual authentication part of the planned production-shaped deployment.
7. Evaluate the system under controlled loss, delay, and heartbeat configurations.

## Non-goals

- Kubernetes, Ansible, a scheduler, service mesh, or general monitoring platform.
- Arbitrary remote command execution or interactive shell access.
- HomeLab-specific message types such as `START_MINECRAFT`.
- A web UI in the minimum viable scope.
- Exactly-once execution in the distributed-systems sense.
- Inventing cryptography or a bespoke TLS replacement.

## Architectural boundary

HLCP provides general concepts: node, resource, action, state, capability, command, event, metric, and error. Names such as `minecraft`, `lego-catalog`, IP addresses, and adapter targets belong only in deployment configuration.

## Success criteria

The product MVP is successful when a configured agent can establish an identity-bound mutually authenticated session, observe a Docker or rootless Podman container, maintain liveness, acknowledge an allow-listed action before execution completes, return a durable terminal or explicit unknown result, and reconcile the same command ID after a transient connection loss. The systemd adapter is an optional deployment profile. The experiment must then quantify the selected liveness and retry policies under emulated network impairment.

## HomeLab management deployment and later extensions

The selected target is one lightweight Debian management VM running the controller
continuously, a separate read-only MCP adapter and, later, a lightweight web UI.
Adapters reuse the loopback controller API with separate server-enforced scopes;
they do not directly control agents or gain arbitrary shell access. Remote log
queries require a separate bounded capability, not unrestricted log streaming.
The [management plan](08-management-node-and-mcp-plan.md) separates implementation,
operator-authorized deployment and live acceptance. MCP/web extensions do not
replace the original protocol evaluation or make a web UI part of the semester MVP.
