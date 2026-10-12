# ADR-0002: Loopback controller API with bearer authentication

- Status: Accepted
- Date: 2026-10-09

## Decision

The v1 CLI communicates with the controller over HTTP bound to an explicit
loopback IP address. The controller rejects wildcard and non-loopback API bind
addresses. Mutating and read endpoints require a bearer token loaded from a
local file; the CLI reads the same token from its local configuration or an
explicit file option.

The credential model is refined by accepted [ADR-0005](0005-management-node-and-api-adapters.md)
and the [F10B permission contract](../10-scoped-api-credentials.md): policy mode
supports separate scoped principals; legacy token mode remains an explicit
unrestricted operator profile. Loopback networking and browser protections remain.

The API applies server timeouts, bounded request bodies, strict JSON decoding,
Host/Origin checks and a stable JSON error envelope. The token and configuration
files are deployment secrets and are never committed.

## Consequences

Remote administration uses an operator-controlled tunnel or login to the
controller host. Exposing this API directly to a LAN, adding remote user
authentication or replacing it with a Unix socket requires a later decision.
