# HomeLab Control Plane (HLCP)

HLCP is a lightweight application-layer control plane for a small HomeLab. It is also the implementation and evaluation subject of an ANI-COM semester project.

The project deliberately focuses on a narrow, observable protocol: an outbound-connected agent registers with a controller, reports liveness and resource state, accepts an allow-listed command, and returns a deduplicated result. It is not a general-purpose remote shell, scheduler, or orchestration platform.

## Documentation map

- [Project vision and scope](docs/00-project-vision.md)
- [HLCP v1 design draft](docs/01-hlcp-v1-design-draft.md)
- [Implementation and evaluation roadmap](docs/02-implementation-and-evaluation-roadmap.md)

## Status

The design is intentionally in **draft**. Open decisions are collected in the protocol design so that they are settled before implementation hardens them into an accidental contract.

The implementation uses Go. The first source slice establishes the versioned, bounded wire frame independently of payload encoding; CBOR remains an explicit design decision before structured messages are added.
