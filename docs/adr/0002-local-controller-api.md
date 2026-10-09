# ADR-0002: Loopback controller API with bearer authentication

- Status: Accepted
- Date: 2026-10-09

## Decision

The v1 CLI communicates with the controller over HTTP bound to an explicit
loopback IP address. The controller rejects wildcard and non-loopback API bind
addresses. Mutating and read endpoints require a bearer token loaded from a
local file; the CLI reads the same token from its local configuration or an
explicit file option.

The API applies server timeouts, bounded request bodies, strict JSON decoding,
Host/Origin checks and a stable JSON error envelope. The token and configuration
files are deployment secrets and are never committed.

## Consequences

Remote administration uses an operator-controlled tunnel or login to the
controller host. Exposing this API directly to a LAN, adding remote user
authentication or replacing it with a Unix socket requires a later decision.
