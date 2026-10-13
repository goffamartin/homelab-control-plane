# Generic agent package and execution profiles — P01

One agent binary supports optional adapters. The core Debian package now depends
only on systemd (>=252), suggests dbus-user-session and installs no Docker,
Podman or uidmap. Runtime dependencies belong to the explicitly chosen resource
profile, not every node. P03 now adds [bounded Linux script execution](18-script-resource-contract.md).

## Installed files and safe lifecycle

- /usr/bin/hlcp-agent; no bundled CLI that conflicts with hlcp-controller.
- /usr/lib/systemd/user/hlcp-agent.service: recommended generic user unit.
- /usr/lib/systemd/user/hlcp-agent-podman.service: unchanged compatibility unit.
- /usr/lib/systemd/system/hlcp-agent.service: opt-in system account profile.
- Examples under /usr/share/hlcp-agent: agent-user/system/docker/podman/script.yaml and
  explicit Docker group drop-in. Templates are not active configuration.

Installation does not create any account, install config/keys into /etc or a
user home, enable linger or enable/start/restart either service. Removal also
requires explicitly stopping/disabling the selected system or user service
beforehand: the package does not discover/stop arbitrary user managers.
StateDirectory and actual credentials are provisioned only for a selected profile.
APT never removes an existing runtime simply because it is no longer a dependency.
Do not blindly apt autoremove after this upgrade; review packages still in use.

A manual /etc/systemd/system/hlcp-agent.service (including a mask) overrides the
packaged system unit. preinst refuses it without modifying the file. Stop the
manual service, review it and move that exact unit to a private backup explicitly
before installing. Existing system/user drop-ins need separate review.

Choose ONLY one lifecycle per node/journal. Generic and legacy user units conflict
in the same user manager, avoiding two writers during a mistaken dual start.
This does not coordinate different user managers or the system manager; operator
must not run system and user agents with the same identity in parallel. The
existing journal lock remains an additional guard, not automatic identity migration.

## Generic user profile (no runtime required)

Run under the existing user owning the resources. Review /usr/share/hlcp-agent/
agent-user.yaml, replacing the example home, endpoint and node identity. Provision
~/.config/hlcp mode0700 with configuration/key/cert/CA mode0600 and
~/.local/state/hlcp mode0700. Initially resources/capabilities are empty arrays.
The current runtime-independent configuration can connect, but no actions are
implicitly available. Certificate provisioning still follows the PKI runbook.

If user D-Bus/session support is missing, explicitly install dbus-user-session.
As the actual owner (not sudo):

```sh
/usr/bin/hlcp-agent -config ~/.config/hlcp/agent.yaml -check-config
systemctl --user daemon-reload
systemctl --user enable --now hlcp-agent.service
systemctl --user status hlcp-agent.service --no-pager -l
```

Enable linger via sudo loginctl enable-linger ACTUAL_USER only after consent.
User unit deliberately does not force NoNewPrivileges: rootless runtimes may
require privileged UID/GID mapping helpers. This profile is not a script sandbox.

## Optional adapters and local rights

- Rootless Podman: explicitly install podman uidmap dbus-user-session, verify
  subordinate IDs, user manager and runtime operation under the actual container
  owner. Reuse generic user service; no separate Podman agent binary is needed.
- Docker: explicitly install/configure Docker separately. Give the selected
  account socket access only after reviewing its root-equivalent privilege.
  For system profile, the packaged drop-in example lives in /usr/share/hlcp-agent;
  explicitly install it under /etc/systemd/system/hlcp-agent.service.d if approved.
- systemd: current adapter targets SYSTEM units via systemctl, even from a user
  service. Running as a user does not grant stop/restart rights for those units.
  Grant only intended local rights separately; no broad sudoers setup is provided.
  User-unit targeting via systemctl --user is not implemented by this slice.
- Scripts (Linux): configure named local executable/argv actions explicitly,
  with `script` + `resource.control` capabilities and type `task`. No container
  runtime or implicit sudo is needed. Follow the [script runbook](18-script-resource-contract.md).

## Opt-in system profile

Explicitly create/review a dedicated non-root hlcp account and group. If absent:

```sh
sudo adduser --system --group --no-create-home --home /nonexistent hlcp
```

If already present, inspect ownership/membership instead of repurposing it.
Provision /etc/hlcp/agent mode0700 root-owned with agent.yaml/agent.key/agent.crt/
ca.crt mode0600. Adapt agent-system.yaml; its credential paths use the exact
hlcp-agent.service name and its journal is /var/lib/hlcp/command-journal.json.
Preserve an existing journal when migrating. systemd creates the private state
directory on start. NoNewPrivileges/ProtectHome/ProtectSystem are enabled;
runtime/script access must respect that sandbox. Rootless Podman uses the user
profile instead, not broad relaxations of the system unit.

After validation and explicit service-start authorization:

```sh
sudo systemd-analyze verify hlcp-agent.service
sudo systemctl enable --now hlcp-agent.service
sudo systemctl status hlcp-agent.service --no-pager -l
```

This system unit and user unit share a name but belong to different managers.
Always distinguish systemctl vs systemctl --user. Do not enable both for one node.

## Existing preview.3 Podman migration

Installing the newer package leaves hlcp-agent-podman.service intact, along with
its enabled symlink, binary/config paths, credentials and journal. No immediate
unit migration is required. The old name is retained during the compatibility
window. Both user units use the SAME ~/.config/hlcp and ~/.local/state/hlcp paths.

To opt into the generic name, as the actual runtime owner:

1. Review systemctl --user cat hlcp-agent-podman.service and any local overrides;
   keep a private backup before moving/reapplying custom files. Do not blindly
   copy the old unit or drop-ins into the new name: they can restore an old binary.
2. Check the installed /usr/bin/hlcp-agent version and validate existing config.
3. Execute the explicit lifecycle switch below. No resources/keys/journal change.

```sh
systemctl --user disable --now hlcp-agent-podman.service
systemctl --user daemon-reload
systemctl --user cat hlcp-agent.service --no-pager
/usr/bin/hlcp-agent -config ~/.config/hlcp/agent.yaml -check-config
# Only after successful validation and checking ExecStart=/usr/bin/hlcp-agent:
systemctl --user enable --now hlcp-agent.service
systemctl --user status hlcp-agent.service --no-pager -l
systemctl --user is-enabled hlcp-agent-podman.service
```

The legacy unit must be disabled/inactive; generic unit enabled/active. Inspect
controller node version/state and explicit test-resource status. Verify reboot
without login separately. Existing manual files can remain backed up; the new
name must not have an unnoticed user override. On compatible rollback, disable
the generic unit first, then explicitly re-enable the retained legacy unit;
never restore an old journal/ledger to make old binaries run.

## Evidence boundary

Go tests validate templates, no-runtime dependencies, deterministic archive/modes,
compatibility unit retention, system hardening and explicit source allowlisting.
The guarded test-agent-package.sh covers fresh non-runtime install, optional
preview.3 upgrade with enabled legacy symlink preservation, manual system mask
refusal, parser checks, removal/purge and preservation of private fixtures.
It does not prove a booted system/user service or live runtime privileges.
