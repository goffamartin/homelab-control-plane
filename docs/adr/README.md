# Architecture decision records

Accepted decisions for HLCP v1:

- [ADR-0001: Agent identity from certificate URI SAN](0001-agent-certificate-identity.md)
- [ADR-0002: Loopback controller API with bearer authentication](0002-local-controller-api.md)
- [ADR-0003: Durable command lifecycle and bounded replay](0003-command-lifecycle-and-retention.md)
- [ADR-0004: Session-scoped inventory revision and observed state](0004-inventory-and-state-semantics.md)

- [ADR-0005: Debian management node and scoped API adapters](0005-management-node-and-api-adapters.md)

ADR-0005 accepts the extension architecture and F10B credential contract;
it does not claim MCP, remote log transport or web deployment is implemented.

An ADR records the contract that subsequent phases implement. Changing an
accepted decision requires a superseding ADR and coordinated documentation,
protocol and test changes.
