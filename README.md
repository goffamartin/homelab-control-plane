# HomeLab Control Plane

HLCP is a small controller–agent system for operating explicitly allowed HomeLab
resources. Agents connect outward over mutual TLS; the controller exposes a
local authenticated API used by the `hlcp` CLI. The application protocol uses
Go and CBOR, with durable command tracking and explicit uncertain outcomes.

## What it does

- Lists connected nodes, resources and enabled actions.
- Controls standalone Podman/Docker containers: status, start, stop and restart.
- Reads retained container logs and streams new output, with bounded filtering.
- Runs named, locally configured script actions; never accepts remote shell text.
- Provides managed pairing, certificate inspection, Debian service management,
  JSON output and Bash completion.

The reference deployment is Debian 13: one controller and a non-root user agent
running as the owner of rootless Podman containers. Other adapters and profiles
have different readiness and validation limits; see [validation](docs/validation.md).

## Start here

Follow [Getting started](docs/getting-started.md) to install signed packages,
pair an agent and configure your first resource. Installation does not create
containers or grant access to all resources automatically.

Once configured, run on the controller:

```sh
hlcp -n
hlcp -n example-node -r
hlcp -n example-node -r app -a status
hlcp -n example-node -r app -a logs --follow --tail 20
```

Only actions explicitly enabled in the agent YAML are available. Ctrl+C stops
log reading, not the container. Use `--json` for machine consumers.

## Web dashboard

[HLCP Web](https://github.com/goffamartin/hlcp-web) is a separate browser client
for a controller you already run. Use it to browse nodes, filter resources by
tag, check a container's state, and view or follow its logs. The resource page
also lets you run actions allowed by the controller and see their results.

The web client connects to the controller API from the server. Its deployment
guide covers local HTTPS access and the credentials it needs. [HLCP Web deployment guide](https://github.com/goffamartin/hlcp-web/blob/main/docs/lan-deployment.md)

## Documentation

| Guide | Purpose |
|---|---|
| [Getting started](docs/getting-started.md) | Install, pair, configure and verify |
| [Configuration](docs/configuration.md) | YAML resources, adapters and local scripts |
| [CLI](docs/cli.md) | Everyday commands, JSON and Bash completion |
| [Operations](docs/operations.md) | Updates, troubleshooting, backups and certificates |
| [Security](docs/security.md) | Accounts, trust boundaries and access |
| [Validation and limits](docs/validation.md) | What was tested and what remains unverified |
| [Development and releases](docs/development.md) | Build, test and reproduce signed artifacts |

[Documentation index](docs/README.md) also links advanced references and
historical design notes. Historical plans are not current installation instructions.

## Scope

HLCP provides the controller API and CLI for managing configured resources.
There is no arbitrary remote command execution, interactive container console,
Docker Compose/Kubernetes management, scheduler or multi-controller HA.
Interrupted actions may have unknown outcomes; a timeout is not a rollback.
Read [release validation](docs/validation.md) before relying on recovery behavior.
