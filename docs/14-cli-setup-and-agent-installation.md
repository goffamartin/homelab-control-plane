# CLI setup and rootless agent installation

## Client configuration

`hlcp configure` writes a new private version-1 JSON configuration in the user's
OS configuration directory (`~/.config/hlcp/cli.json` on Linux, respecting
XDG_CONFIG_HOME; user AppData on Windows). Run as the actual CLI user, not sudo.
Windows operators must protect the directory with their user ACL.

On the controller, an authorized administrator can give their own CLI a private
copy of the existing operator credential without changing service permissions:

```sh
sudo cat /etc/hlcp/controller/api.token | hlcp configure --token-stdin
hlcp -n
```

This is intentionally the SAME operator identity/credential, not a newly issued
principal. The controller already permits it. Revocation/rotation affects both
copies; update the private client copy when rotating. No new policy/drop-in is
needed and the root-owned source remains private. Never copy a GitHub PAT.
For independently revocable access, first provision a distinct principal through
the existing policy/LoadCredential mechanism, then import that authorized token.
Configure itself does not generate or grant controller credentials.

Alternatively, use `hlcp configure --api-token-file /absolute/private/token` to
save a reference without copying. `--api` may point to a loopback SSH tunnel;
remote/public API URLs remain rejected. Tokens are never accepted as arguments
or printed. Existing configuration is never overwritten. Back it up privately
and edit deliberately for endpoint/path changes; there is no implicit rotation.

Configuration schema: `version: 1`, `api`, `api_token_file`. Relative token paths
resolve beside the config. Files are bounded, private and strictly parsed;
duplicate/unknown/case-variant keys fail closed. CLI `--config FILE` is explicit
(missing files are errors); default absent config preserves legacy defaults.
Explicit `--api` and `--api-token-file` override saved settings. Supplying both
without `--config` bypasses configuration, supporting isolated diagnostics.
Help/version do not require a working config or token.

## Debian rootless agent package

Release builds produce `hlcp-agent_VERSION_amd64.deb` and arm64 equivalent.
The package installs `/usr/bin/hlcp-agent`, a systemd user unit under
`/usr/lib/systemd/user`, an example configuration and this runbook. APT installs
Podman/uidmap/dbus-user-session dependencies. It does not create a runtime owner,
modify user homes, enable linger, generate PKI, configure resources or
start/restart/enable services. Existing user journals/keys/config are preserved.
Removal requires explicitly stopping/disabling each user's unit first; package
removal cannot safely infer all running user managers.

After downloading from the private release and verifying package/manifest hashes:

```sh
sudo apt install ./hlcp-agent_VERSION_amd64.deb
/usr/bin/hlcp-agent --version
```

Provision `~/.config/hlcp/agent.yaml`, agent key/certificate/CA (0600, directory
0700), and `~/.local/state/hlcp` (0700) as the actual container owner. Keep the
agent private key on its node; transfer CSR and public certificates only. Follow
the [PKI/network runbook](12-debian-management-deployment.md). Initially configure
`resources: []` and explicit identity matching the clientAuth URI SAN.

As that user (not sudo), after checking TLS/config and controller allowlist:

```sh
/usr/bin/hlcp-agent -config ~/.config/hlcp/agent.yaml -check-config
systemctl --user daemon-reload
systemctl --user enable --now hlcp-agent-podman.service
```

Enable linger explicitly via `sudo loginctl enable-linger USER` for boot without
login. Inspect actual rootless Podman operation and reconnect after reboot.
Upgrades never restart the user service automatically; restart explicitly in a
maintenance window after checking compatibility, keeping journal/command IDs.

### Existing manual user-unit migration

The old `~/.config/systemd/user/hlcp-agent-podman.service` overrides the packaged
unit and points at the old `~/.local/bin/hlcp-agent`. Installing a package alone
does not migrate it. Review local modifications first. As the actual owner:

```sh
systemctl --user stop hlcp-agent-podman.service
mv -i ~/.config/systemd/user/hlcp-agent-podman.service \
  ~/.config/systemd/user/hlcp-agent-podman.service.manual-backup
systemctl --user daemon-reload
systemctl --user cat hlcp-agent-podman.service
/usr/bin/hlcp-agent -config ~/.config/hlcp/agent.yaml -check-config
systemctl --user start hlcp-agent-podman.service
```

Confirm ExecStart uses `/usr/bin/hlcp-agent`, then inspect status and controller
inventory. Preserve custom hardening via reviewed drop-ins. Do not remove the
manual binary until the packaged unit is verified. On rollback, keep existing
state and restore the backed-up unit deliberately; never erase journals.

## Remaining installation simplification

See [onboarding and agent-profile design notes](15-onboarding-and-agent-profile-design-notes.md)
for the agreed managed/imported certificate modes and open script/profile design.

These are two focused steps, not a complete PKI installer. Next slices: verified
release download/update helper (private GitHub auth and checksums), non-destructive
controller/agent configuration wizard with CSR generation, and an explicit
operator-approved certificate signing/admission workflow. No curl-to-root script,
automatic CA key transfer, blind file overwrite or unattended node admission.
