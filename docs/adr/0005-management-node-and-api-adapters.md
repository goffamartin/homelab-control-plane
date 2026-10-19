# ADR-0005: Debian management node and scoped API adapters

Status: **Accepted**. Date: 2026-10-12.

## Context

The chosen deployment direction is one lightweight, continuously running Debian
management VM, a hosted MCP server and a later web UI. ADR-0002 currently uses
one privileged local bearer credential. Sharing that credential with a read-only
adapter would not enforce read-only access at the controller boundary.

## Decision

- Keep one controller and outbound mTLS agents with an explicitly configured,
  stable controller address. No LAN discovery or multi-controller HA.
- Keep the controller HTTP API loopback-only with its existing Host/Origin and
  bounded-request protections. CLI, MCP and a future web backend consume this API;
  adapters never call agents or container runtimes directly.
- Refine ADR-0002's credential model with separate authenticated principals and
  server-enforced permissions, including a read-only observer for MCP. Preserve
  an explicit migration path for the existing privileged CLI credential.
- Run controller and MCP as separate least-privilege systemd services. MCP does
  not own the ledger, agent journals or Docker socket. Its inbound authentication
  is separate from its controller API credential.
- Initial MCP is read-only with bounded diagnostic queries. Remote container log
  reads require a typed capability and independent bounded read lifecycle.
- Public network exposure, mutation approvals and web login require explicit
  access-profile decisions and verification. No implicit arbitrary shell or
  autonomous mutation capability is introduced by hosting MCP.

## Consequences and adoption gate

This ADR accepts the architecture, not completion of all adapters/deployment.
F10B implements the [scoped credential contract](../reference/10-scoped-api-credentials.md):
observer/operator roles, explicit node/resource scope, strict private policy,
atomic reload and authorization serialized with durable admission. It refines
ADR-0002's single-credential model; its networking contract is unchanged.
Log transport, MCP and web remain subsequent implementation phases.
Loopback networking remains unchanged. MCP transport/auth interoperability must
be tested with the chosen real client; the initial endpoint stays private.

The [implementation plan](../archive/08-management-node-and-mcp-plan.md) separates core
F10/F11 acceptance from later HomeLab extensions. Web UI and mutating MCP remain
outside the original semester MVP; deployment needs operator-selected hosts.
