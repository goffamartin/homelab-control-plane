# Debian management deployment — F10D

Prepared 2026-10-12. This is an operator runbook, not an automatic installer.
F10D packages and tests profiles locally; actual installation, reboot, upgrade,
certificate rotation and rootless Podman evidence remain F10E gates.
MCP has no binary or runnable service yet; its separate unprivileged profile
will be added in M1. Nothing in this phase deploys to HomeLab.

## Roles and prerequisites

| Profile | Identity | Writable state | Runtime privilege |
| --- | --- | --- | --- |
| Controller, system unit | hlcp-controller | /var/lib/hlcp-controller, mode 0700 | None; API loopback only |
| Agent, system unit | hlcp | /var/lib/hlcp, mode 0700 | None by default; Docker drop-in is explicit |
| Podman, user unit | Existing container owner | Owner's ~/.local/state/hlcp | Rootless runtime of that user |
| Future MCP | Separate account | No ledger/journal access | Observer API credential only |

Baseline: Debian 12 or newer, systemd 252 or newer, native Linux release binary
for the correct architecture. Static binaries do not require Go on the VM.
Controller and agent can run on different hosts. Do not install both agent
profiles for the same node/journal. Verify real VM memory, CPU, disk and log
usage during F10E; no unmeasured resource budget is promised.

Docker group access is root-equivalent; install its drop-in only on an approved
Docker node. Removing the drop-in does not remove pre-existing supplementary
group membership. Review `id hlcp` and remove legacy Docker membership if no
longer intended. Hardening does not sandbox a client with the Docker socket.
[Docker security guidance](https://docs.docker.com/engine/install/linux-postinstall/).

Rootless Podman must run as the same user that owns the containers, with
subuid/subgid mappings and newuidmap/newgidmap helpers. Its unit deliberately
does not use NoNewPrivileges or a system-service ProtectHome sandbox.
[Podman prerequisites](https://docs.podman.io/en/latest/markdown/podman.1.html).

## Release and configuration preparation

Use a clean trusted source revision and the pinned builder from
[build guide](07-build-and-release.md). Release output contains nine binaries,
`hlcp-deployment.tar.gz`, a manifest and SHA256SUMS. The archive uses an explicit
file allowlist and contains templates/runbooks only, not binaries or secrets.
On the target, verify `sha256sum -c SHA256SUMS` before extracting into a fresh
private staging directory. Checksums detect corruption, not authenticity:
obtain the manifest and binaries through a trusted channel/revision.

Create production configuration outside Git. Do not install placeholder
192.0.2.10 or example.internal addresses unchanged. Review node IDs, certificate
SANs, resources and allowed actions; initially enable status before mutations.
The template observer has all-node visibility including global metrics/audit;
reduce its scope if that is not intended. Scoped policies are documented in
[credentials contract](10-scoped-api-credentials.md).

System unit source layout, owned by root (directories 0700, files 0600):

- /etc/hlcp/controller/: controller.env, controller.crt, controller.key, ca.crt,
  api-policy.json, nodes.json, api.token and observer.token.
- /etc/hlcp/agent/: agent.yaml, agent.crt, agent.key and ca.crt.

Generate two distinct random bearer tokens outside the repository, for example
`openssl rand -hex 32` into each private token file under umask 077.
Do not paste tokens into unit files, environment variables, process arguments or
logs. Provision keys via your trusted PKI; the CA signing key stays offline.

systemd LoadCredential copies source files to a private service credential
directory; %d resolves that directory in ExecStart. The policy's relative token
paths resolve beside its loaded file. Agent YAML uses the fixed
/run/credentials/hlcp-agent.service paths: do not rename the unit without updating
those paths. These are plaintext protected credentials, not disk encryption.
Updates to source credentials require a service restart. SIGHUP reload of this
profile reads the old loaded snapshot; direct-file development mode has different
reload semantics. [systemd 252 reference](https://raw.githubusercontent.com/systemd/systemd/v252/man/systemd.exec.xml).

## System controller / Docker agent installation

Execute only the profile needed on that host, with administrator authorization:

```sh
sudo useradd --system --user-group --home-dir /nonexistent --shell /usr/sbin/nologin hlcp-controller
sudo useradd --system --user-group --home-dir /nonexistent --shell /usr/sbin/nologin hlcp
# If accounts already exist, inspect them instead of recreating/changing them.
sudo install -o root -g root -m 0755 hlcp-controller-linux-amd64 /usr/local/bin/hlcp-controller
sudo install -o root -g root -m 0755 hlcp-agent-linux-amd64 /usr/local/bin/hlcp-agent
sudo install -o root -g root -m 0644 deploy/systemd/hlcp-controller.service /etc/systemd/system/hlcp-controller.service
sudo install -o root -g root -m 0644 deploy/systemd/hlcp-agent.service /etc/systemd/system/hlcp-agent.service
# Docker only, after explicit privilege review:
sudo install -d -m 0755 /etc/systemd/system/hlcp-agent.service.d
sudo install -m 0644 deploy/systemd/hlcp-agent-docker.conf /etc/systemd/system/hlcp-agent.service.d/20-docker.conf
sudo systemctl daemon-reload
sudo systemd-analyze verify /etc/systemd/system/hlcp-controller.service /etc/systemd/system/hlcp-agent.service
# Start only the selected, fully provisioned profile:
sudo systemctl enable --now hlcp-controller.service
# On a Docker agent host: sudo systemctl enable --now hlcp-agent.service
sudo systemctl status hlcp-controller.service
sudo journalctl -u hlcp-controller.service --since '-10 min'
```

Create private configuration directories and install the reviewed files before
starting; the archive intentionally does not generate certificates or tokens.
Use `sudo install -d -o root -g root -m 0700 /etc/hlcp/controller` (or
/etc/hlcp/agent on an agent host), then `sudo install -o root -g root -m 0600`
for each reviewed source file at the exact layout above. controller.env and
nodes.json are required, not optional defaults.
ExecStartPre validates configuration. Controller --check-config reads TLS,
policy and allowlist but does not bind sockets or open/modify/lock the ledger.
It does not prove port availability, certificate deployment correctness or
ledger compatibility. Agent --check-config checks YAML semantics, not runtime
or TLS readability; startup checks TLS. Missing/invalid credentials fail startup.

StateDirectory replaces tmpfiles for new system installations. Preserve the
agent's existing /var/lib/hlcp journal on upgrade; the legacy tmpfiles template
now agrees on 0700. If migrating a legacy controller ledger from /var/lib/hlcp,
stop the old service, copy its ledger into the new controller-owned directory,
set owner hlcp-controller and mode 0600, and retain the private original backup.
Never silently start with an empty new path.

Optional log namespace/retention drop-ins are in [telemetry guide](09-telemetry-and-audit.md).
They apply to system services only, not the rootless user unit. Journal storage
limits are shared within the namespace; measure disk use and rate-limit drops.

## Rootless Podman installation

As the actual container owner, install the binary under ~/.local/bin/hlcp-agent
(0755), reviewed agent-podman.yaml as ~/.config/hlcp/agent.yaml (0600) and TLS
files beside it (0600). Replace every example home path with the real home.
Create ~/.config/hlcp and ~/.local/state/hlcp with mode 0700. Preserve an existing
journal when migrating. Install the user unit at
~/.config/systemd/user/hlcp-agent-podman.service and run:

```sh
systemd-analyze --user verify ~/.config/systemd/user/hlcp-agent-podman.service
systemctl --user daemon-reload
systemctl --user enable --now hlcp-agent-podman.service
journalctl --user -u hlcp-agent-podman.service --since '-10 min'
```

Enable `loginctl enable-linger ACTUAL_USER` only with operator approval when
the service must start at boot without login. Confirm XDG_RUNTIME_DIR/user
manager and Podman work after reboot. User-service packaging is not proof of
live rootless operation; the existing isolated F7 Podman smoke remains open.

## Network and PKI lifecycle

Bind the agent listener to the VM management IP via controller.env. Firewall
TCP 9443 to approved agent management/VPN addresses only; agents dial outbound.
API stays 127.0.0.1:9444; do not publish it or terminate TLS into public access.
Remote CLI can use an authenticated SSH tunnel:
`ssh -N -L 19444:127.0.0.1:9444 USER@MANAGEMENT_VM`, then configure CLI API
http://127.0.0.1:19444 with its private token file. Check CLI --help for selectors.
Future MCP requires a separately reviewed private transport/authentication;
this phase opens no MCP endpoint.

Use a trusted private CA, controller DNS/IP SAN matching agent server_name and
explicit serverAuth EKU; agent clientAuth EKU and one URI SAN
urn:hlcp:node:IDENTITY matching node_id. TLS 1.3 verifies peers; CN is not an
identity fallback. Monitor leaf and CA expiry out-of-band, for example
`openssl x509 -in LEAF.crt -noout -dates` and `-checkend 2592000`.
No hot TLS reload, CRL or OCSP implementation is claimed.

For routine leaf renewal, issue correct SAN/EKU, validate chain/key/expiry,
stage private files, restart the affected service and confirm reconnect/state.
For CA rotation, deploy overlapping trusted old/new bundles on both sides,
restart to load them, replace all leaves and verify peers, then remove old CA
trust and restart again. Existing connections are closed by restart.

nodes.json is a strict JSON array (max 64 KiB, 1024 unique IDs);
[] explicitly denies all nodes. The production unit always loads it. To remove
a node, atomically replace the root-owned file without that ID and restart the
controller. This closes all active sessions; removed IDs cannot register on
new connections even with a CA-valid cert. Other nodes reconnect; containers
continue running and ledger survives. Invalid allowlist fails startup.
Without the flag, development compatibility still trusts any valid node under
the CA. The allowlist is startup-only, copied into an immutable server snapshot.

This revokes a node identity, not one leaf serial. For a compromised node, remove
the ID immediately, investigate pending operations and provision a new identity
and credentials; do not re-allow the same ID while its compromised leaf remains
trusted. If a CA is compromised, replace trust and restart all peers. Coordinate
API policy scopes separately; API principal revocation does not revoke agent TLS.

## Update, backup, restore and rollback

Record current binary --version, artifact hashes, service/config paths and
pending operation IDs. Stop selected services before replacing executables.
Back up ledger/journal and configuration/credentials consistently into a private
encrypted/off-host store; do not back up only the binary. Do not unlink a running
process's persistent .lock file. OS locks are released by stop/crash; paths and
ownership must stay stable. Review disk capacity and JOURNAL_FULL before upgrade.

Install verified binaries, compatible configuration and units, daemon-reload,
start and check status, journal, authenticated node/resource GETs and reconnection.
Record exact revision and results. Reboot acceptance is a separate F10E test.
For an approved F10E maintenance window, record healthy state, reboot the host,
then verify enabled system services (or the lingering user service), fresh
node/resource observations, unchanged ledger/journal and the same binary hashes.
Do not use a production resource mutation as the initial reboot smoke test.
Keep old binaries and unit/config backups for rollback. Restore secrets only
from trusted private backups with original owners/modes, not into Git.

Agent journal format is v3 with v1/v2 migration backups; controller ledger is v1.
Older binaries may not understand newer formats. Roll back binaries only when
state readers/wire formats are compatible. Restoring an old journal/ledger can
forget executed actions; migration backups are not safe current operational
history. Do not blindly replace current state with them. Stop, preserve both
copies and reconcile runtime state and original command IDs. Unknown outcomes
must be queried/reconciled, never repeated automatically with a fresh ID.

## Local preparation evidence / remaining gates

F10D checks cover strict allowlist loading/admission, copied configuration,
non-mutating controller configuration validation, Windows tests/vet, Linux race
tests, deterministic deployment archive and release checksums. systemd 252.39 parser
checks run in a disposable Debian container, without installing host services.
Parser success does not prove credential loading, account permissions, user
linger or hardening on a booted VM. F10E must record actual install, missing
credential failure, reboot, expiry/rotation, node removal with active sessions,
upgrade/backup/compatible rollback, private networking and live rootless Podman.
Keep evidence per host: Debian/systemd/runtime versions, binary revision,
effective unit/drop-ins, sanitized configuration, timestamps, expected/actual
result and recovery notes. Never include token text or private keys in evidence.
