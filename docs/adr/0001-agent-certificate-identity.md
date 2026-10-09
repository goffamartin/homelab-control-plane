# ADR-0001: Agent identity from certificate URI SAN

- Status: Accepted
- Date: 2026-10-09

## Decision

Every production agent certificate contains exactly one identity URI in the
form `urn:hlcp:node:<node-id>`. The controller accepts a registration only when
this URI exactly matches `HELLO.node_id`. The certificate must chain to the
configured agent CA, be valid for TLS client authentication and negotiate the
`hlcp/1` ALPN protocol.

The controller certificate uses a DNS or IP SAN matching the agent's configured
`server_name` and is valid for TLS server authentication. Common Name is not an
identity fallback. Development and production use separate private CAs.

## Consequences

Certificate rotation can keep the stable node ID. Possession of any certificate
from the agent CA no longer permits impersonating another node. Certificate
issuance and revocation remain deployment responsibilities; automated PKI is
outside v1.
