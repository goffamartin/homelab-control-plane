# HomeLab Control Plane (HLCP)

HLCP is a lightweight application-layer control plane for a small HomeLab. It is also the implementation and evaluation subject of an ANI-COM semester project.

The project deliberately focuses on a narrow, observable protocol: an outbound-connected agent registers with a controller, reports liveness and resource state, accepts an allow-listed command, and returns a deduplicated result. It is not a general-purpose remote shell, scheduler, or orchestration platform.

## Documentation map

- [Project vision and scope](docs/00-project-vision.md)
- [HLCP v1 design draft](docs/01-hlcp-v1-design-draft.md)
- [Implementation and evaluation roadmap](docs/02-implementation-and-evaluation-roadmap.md)
- [Debian agent deployment](docs/03-debian-agent-deployment.md)
- [Review findings and phased v1 completion plan](docs/04-review-and-completion-plan.md)
- [HLCP v1 acceptance contract](docs/05-v1-acceptance.md)
- [Architecture decisions](docs/adr/README.md)

## Status

The v1 implementation currently includes:

- TLS 1.3 mutual authentication between controller and agent;
- bounded binary framing and canonical CBOR protocol payloads;
- inventory/state synchronization and heartbeat-based liveness;
- allow-listed `systemd`, Docker, and Podman resource actions;
- controller-side command correlation and agent-side command-result deduplication;
- a loopback-only HTTP API consumed by the `hlcp` CLI.

The CLI navigates the discovered inventory progressively:

```text
hlcp -n
hlcp -n debian-01 -a status
hlcp -n debian-01 -r
hlcp -n debian-01 -r example-catalog -a
hlcp -n debian-01 -r example-catalog -a restart
```

Start the controller API on its default local address (`127.0.0.1:9444`) with:

```text
hlcp-controller -listen :9443 -api-listen 127.0.0.1:9444 -api-token-file /etc/hlcp/api.token -cert controller.pem -key controller-key.pem -ca ca.pem
```

`hlcp` targets that local address by default. Use `hlcp --help` for the explicit long-form selectors and `--api` override.

The controller and CLI read a shared local bearer token from `/etc/hlcp/api.token`
by default. Override the file with `--api-token-file`. On Unix the token file must
have private permissions (0600). Use a randomly generated token of at least 32
characters; never commit it. Browser-origin requests and non-loopback API binds
are rejected.

Agent certificates require an explicit client-auth EKU and exactly one HLCP URI
SAN, for example `urn:hlcp:node:debian-01`, matching the YAML `node_id`. The
controller certificate requires server-auth EKU and the configured server name.
Both peers must negotiate `hlcp/1` ALPN.

The design document remains the source of truth for unfinished production hardening and evaluation scenarios.
