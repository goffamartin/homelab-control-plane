# Documentation

These guides describe the v1.0 operator interface. Start with the first guide;
advanced internals are optional.

1. [Getting started](getting-started.md): signed installation and first resource.
2. [Configuration](configuration.md): YAML, container actions and local scripts.
3. [CLI](cli.md): selectors, service commands, machine output and completion.
4. [Operations](operations.md): updates, failures, backups and certificates.
5. [Security](security.md): service accounts, permissions and API access.
6. [Validation](validation.md): evidence, deployment scope and remaining risks.
7. [Development](development.md): tests, reproducible builds and publication.

## Detailed references

- [Script execution contract](reference/18-script-resource-contract.md)
- [Container logs and streaming API](reference/56-container-logs.md)
- [Administrative JSON output](reference/55-administrative-output.md)
- [Agent service and Bash completion contracts](reference/57-agent-service-and-bash-completion.md)
- [Scoped API credentials](reference/10-scoped-api-credentials.md)
- [Diagnostics API](reference/11-bounded-diagnostics-api.md)
- [Telemetry and audit](reference/09-telemetry-and-audit.md)

## Project history

[Archived plans and evidence](https://github.com/goffamartin/homelab-control-plane/blob/v1.0.0/docs/archive/README.md) preserve the implementation
history. Their preview commands, acceptance statuses and proposals are snapshots,
not current guidance. [Architecture decision records](https://github.com/goffamartin/homelab-control-plane/blob/v1.0.0/docs/adr/README.md) record
individual decisions; they are not an onboarding tutorial.
