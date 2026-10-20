# Operations

## Check the right layer

| Check | What success means |
|---|---|
| `hlcp-agent check-config` | YAML syntax/schema valid; no runtime/TLS proof |
| `hlcp-agent status` | Packaged service state inspected |
| `hlcp -n` on controller | Current node connection/liveness is visible |
| Resource `status` action | Local adapter returned the observed runtime state |
| Application-specific check | Application health; outside generic container state |

An empty inventory is not a failed installation: resources must be enabled in
YAML. A stopped container is not an offline agent. An active service is not proof
of controller connectivity.

## Update an existing managed installation

Use a trusted helper downloaded for the intended release, as described in
[Getting started](getting-started.md). Set `VERSION=v1.0.0` for this release.
Public downloads need `curl` and the explicit `--public` flag, not a GitHub
account or token. Keep a consistent private backup first. Update the controller
before agents. Do not use fresh-install `--configure` on an existing installation.

On the controller, preview, then explicitly allow the service restart:

```sh
sh hlcp-install.sh controller --version "$VERSION" --public --update
sh hlcp-install.sh controller --version "$VERSION" --public --update --restart --apply --yes
```

On the user agent, as its owner:

```sh
sh hlcp-install.sh agent --version "$VERSION" --public --scope user --update
sh hlcp-install.sh agent --version "$VERSION" --public --scope user --update --restart --apply --yes
```

The helper refuses a running service unless a restart was explicitly requested;
alternatively stop it first. Follow any reported user-manager reload instruction
after package/unit replacement. Do not rerun fresh setup or delete state during
an ordinary update. Retain identity, resources, journal and controller ledger.
Automatic reset, history rollback and implicit resource privilege grants are not
part of installation. Inspect any retained private staging/recovery path before
retrying an incomplete update.

Verify installed versions on both hosts, agent service state, controller service
state and `hlcp -n`, then query a known resource. Existing resources do not need
re-pairing merely because binaries were upgraded.

## Common problems

| Symptom | Safe next step |
|---|---|
| YAML/schema error | `hlcp-agent configure`, then `check-config`; fix the named check without exposing secrets |
| Saved configuration, startup incomplete | Inspect the reported completed steps; repair readiness, then `hlcp-agent start` |
| Manager reload required | On user agent: `systemctl --user daemon-reload`, then retry the intended service command |
| User manager unavailable or no linger | Review `hlcp-agent prepare-user --help`; run preparation with explicit sudo consent |
| Client profile refused | Inspect home/config ownership and shared write permissions; review trust before changing permissions |
| Agent active but offline | Check controller address/name, clock, certificates, reachability and sanitized service logs |
| Logs end immediately | Check container status and runtime log retention; stopped containers can finish normally |
| Bash Tab gives nothing | Enable `bash-completion` or source the installed binary's Bash script; dynamic names require API access |
| Unknown/uncertain action outcome | Look up the original command ID and inspect resource state; do not dispatch with a new ID blindly |

Read service logs locally when needed:

```sh
systemctl status hlcp-controller.service --no-pager
journalctl -u hlcp-controller.service --since today --no-pager
hlcp-agent status --json
journalctl --user -u hlcp-agent.service --since today --no-pager
```

Avoid sharing raw configuration, keys, tokens, pairing codes or script output.
`hlcp-agent doctor --help` describes optional read-only checks; an explicit network
probe can produce remote logs. Doctor is not an automatic repair command.

For interrupted managed setup, follow the specific reported next step. An
interrupted join supports `hlcp-agent join --resume --apply --yes`; a fully
paired but stopped agent instead needs `start`, not another join. Deliberate
identity replacement/re-pairing is a separate administrative action, not a
generic retry strategy.

## Backups and recovery boundaries

The encrypted root backup protects CA recovery material; it is **not a complete
controller backup**. Keep it off-host with a separately retained password.
A consistent controller backup also needs its private configuration/PKI,
registry, pending enrollment state, API policy/credentials and operation ledger.
Agent backups need identity/TLS/configuration, managed state and command journal.
Preserve ownership and modes; backups contain sensitive material.

Stop the relevant services before taking a filesystem-level recovery copy.
Do not restore an old journal/ledger while retaining newer side effects and
assume replay protection still holds. A package backup is not an application
state backup, and reinstalling binaries is not a recovery plan. Test restoration
on an isolated clone before relying on it; complete live disaster restoration
was deferred for v1.0.0. Never overwrite live state merely to try a restore.

## Certificates

Read-only inspection:

```sh
sudo hlcp-controller certificates
hlcp-agent certificates
```

Managed leaves are short-lived (90 days) with automatic renewal attempts;
the issuer/root have longer finite lifetimes. Renewal needs a working managed
enrollment path and valid identity/trust, and can fail. Check diagnostics and
clock synchronization rather than assuming certificates are evergreen.

Issuer maintenance, imported-CA migration, trust refresh and deliberate re-pairing
are advanced maintenance operations. Inspect the exact command's help and
prepare backups/isolated tests before applying them. The historical
[migration design](https://github.com/goffamartin/homelab-control-plane/blob/v1.0.0/docs/archive/50-managed-migration-and-recovery.md) explains the
implementation background, not a completed live recovery qualification.
See [validation](validation.md) for untested lifecycle scenarios.
