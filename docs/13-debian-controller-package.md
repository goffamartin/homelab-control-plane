# Native Debian controller package

The release builder now produces a local `hlcp-controller_VERSION_ARCH.deb`
for each selected Linux target (amd64/arm64). This is a private homelab package,
not a package submitted to Debian. Building does not upload anything to GitHub,
create a tag, install a host service or provision production credentials.
The [rootless agent package and CLI setup](14-cli-setup-and-agent-installation.md)
extend this workflow. MCP and web packages are not implemented yet.

## Contents and lifecycle

- /usr/bin/hlcp-controller and /usr/bin/hlcp (local CLI).
- /usr/lib/systemd/system/hlcp-controller.service, with /usr/bin executable paths.
- Private /etc/hlcp/controller directory (0700), initially without live files.
- Public examples in /usr/share/hlcp-controller: controller.env, nodes.json and
  api-policy.json (0644). These are not active configuration or credentials.
  The listener example is loopback and its node allowlist is [] (deny all).
- Confirmed hlcp-controller setup provisions live configuration (0600) and
  imported TLS; default preview does not write or generate tokens. See
  [fresh package setup acceptance](37-controller-package-setup.md).
- Installation creates a non-root hlcp-controller system account when absent.
  Existing accounts are not repurposed; inspect any existing account's rights.
- No service auto-enable/start/restart, no CA/leaf keys or bearer tokens,
  no firewall changes, no downloads in maintainer scripts.
- Removal stops the systemd service on a booted host. Removal/purge preserve
  runtime ledger, unmanaged credentials and the service account. Purge removes
  legacy dpkg-owned configuration files, including obsolete conffiles retained
  from previous versions; back them up first. Fresh wizard-created configuration
  is not dpkg-owned and is retained. Neither operation is a
  secure secret wipe or a safe substitute for credential revocation.

Installation/update require operator authorization and root via sudo.
Maintainer scripts run under dpkg, not as arbitrary downloaded shell installers.
Format and conffile behaviour follow
[Debian package format](https://manpages.debian.org/trixie/dpkg-dev/deb.5.en.html)
and [Debian maintainer script lifecycle](https://www.debian.org/doc/debian-policy/ch-maintainerscripts.html).

## Build and verify

Run the pinned builder in a clean checkout:

```sh
go run ./tools/release -version v0.1.0 -targets linux/amd64 -out dist/package-preview
```

This is a version example, not a published release. Numeric labels (optionally
prefixed v) map to the same Debian version, with prerelease hyphens mapped to ~.
For example v0.1.0-preview.1 becomes 0.1.0~preview.1. Named builds map to
0~LABEL+gitREVISION, with hyphens normalized to ~ (label) / + (revision).
Hashes identify snapshots, not chronological ordering; use increasing numeric
release versions for operational upgrades. -allow-dirty snapshots retain their
dirty marker and must not be represented as committed releases.
Package bytes use deterministic ar/tar/gzip metadata and normalized text LF;
no dpkg, Docker or native Linux builder is needed to generate them on Windows.
Package hashes join the existing manifest/SHA256SUMS, including both architectures.
Debian '~' version separators are represented as '-' in artifact filenames so
GitHub does not rewrite them. For example the file is
hlcp-controller_0.1.0-preview.1_amd64.deb, while its internal Debian version is
0.1.0~preview.1. Downloaded filenames match SHA256SUMS without manual renaming.

Before installation, verify the selected artifact hash against the trusted
SHA256SUMS/manifest. If downloading only selected artifacts:

```sh
sha256sum --ignore-missing -c SHA256SUMS
dpkg-deb --info ./hlcp-controller_VERSION_amd64.deb
dpkg-deb --contents ./hlcp-controller_VERSION_amd64.deb
sudo apt install ./hlcp-controller_VERSION_amd64.deb
/usr/bin/hlcp-controller --version
```

Check that the exact selected .deb and manifest appear as OK; missing artifacts
are deliberately ignored, not proven present. Checksums establish integrity,
not authenticity of an untrusted source. Use a private readable staging directory
and grant APT its needed read access if it warns about its download sandbox.
Do not install an arm64 package on an amd64 VM.

## Private GitHub downloads

GitHub Releases are the intended distribution channel; none is published by this
implementation. Once the operator approves a version/tag and release publication,
upload the .deb, manifest and SHA256SUMS from that exact clean revision.
The private repository needs authenticated download. Prefer the operator's
existing GitHub CLI login or an authenticated browser on their own computer:

```sh
gh release download TAG --repo goffamartin/homelab-control-plane \
  --pattern 'hlcp-controller_*_amd64.deb' \
  --pattern SHA256SUMS --pattern build-manifest.json --dir NEW_DIRECTORY
```

This command works only after a matching release exists. Do not copy a personal
PAT to the VM. If avoiding GitHub authentication on the VM, transfer the selected
package and integrity metadata from the PC; packaging simplifies installation but
does not remove private-repository authentication. There is no custom APT repo or
automatic update mechanism.

## Provision and start

Follow the PKI/configuration sections of
[deployment runbook](12-debian-management-deployment.md).
Replace listener address with the VM's actual reserved IP (currently
192.168.0.15), provision server SAN for the chosen DNS/IP and list actual agent
certificate identities. Provision root-owned 0600 certificate/key/CA files and
distinct API tokens at the existing LoadCredential paths. Never generate or
store the CA signing key inside the package or repository.

```sh
sudo systemd-analyze verify hlcp-controller.service
sudo systemctl enable --now hlcp-controller.service
sudo systemctl status hlcp-controller.service
sudo journalctl -u hlcp-controller.service --since '-10 min'
```

Startup uses ExecStartPre -check-config; missing credentials/configuration fail.
Reboot/real credential permissions, rotation, management networking and live
Podman remain F10E gates, not proven by package-format/install smoke alone.
The VM operator runs all commands manually; development does not connect to it.

### systemd credential ACL compatibility

Preview.1 incorrectly rejects systemd's protected root:root 0440 ACL copies of
API policy/tokens. Preview.2 validates the exact per-service-UID read ACL instead
of interpreting its mask as broad group access. Ordinary group-readable files
remain rejected; no credential regeneration or permission workaround is needed.
See the [private-file contract](10-scoped-api-credentials.md). Linux regression
tests load real root-owned ACL policy/tokens under a non-root UID and reject
additional users, owning-group access, writable masks and non-root ownership.
This reproduces credential access, not a booted-systemd startup/reboot test.
Upgrade the verified package, then explicitly reset-failed/start and inspect the
service on the VM. Keep the existing configuration, CA, leaf key and API tokens.

## Update, existing manual installs and rollback

Back up consistent state/configuration first; preserve original command IDs.
Install a verified newer package with sudo apt install ./NEW_PACKAGE.deb.
Current packages do not deliver live files into /etc/hlcp/controller and leave
existing configuration unchanged. Legacy conffiles remain in dpkg's database as
obsolete after upgrade: they survive upgrade but may be removed on purge.
A rollback to an older package delivering conffiles can still prompt; review
differences, do not blindly choose maintainer versions or force overwrite.
The running service is not automatically restarted; after reviewing compatibility
and provisioning, explicitly restart it in a maintenance window and verify
/usr/bin/hlcp-controller --version and systemctl status.

An old manually installed /etc/systemd/system/hlcp-controller.service would
override the packaged unit, so preinst refuses installation until the operator
stops the service and moves that exact file to a private backup.
Review existing /etc/systemd/system/hlcp-controller.service.d overrides separately;
they remain in effect. Do not delete credentials, ledger or persistent .lock files.
If the old ledger lives under /var/lib/hlcp, migrate it while stopped into the
controller-owned /var/lib/hlcp-controller path per the runbook before starting.
Old /usr/local/bin binaries are not removed: shell PATH may prefer them, so use
/usr/bin/hlcp and /usr/bin/hlcp-controller or retire them explicitly after review.

For a compatible binary rollback, explicitly install the previously verified
package (APT may require --allow-downgrades in noninteractive mode), then restart
manually. Never restore an old ledger/journal merely to make old binaries start;
that can forget already executed actions. A full booted-VM upgrade/rollback test
remains separate from isolated package lifecycle tests.

## Verification evidence

Portable Go tests cover ar member layout/padding, tar ownership/permissions,
deterministic bytes, version mapping, an empty live configuration namespace,
public examples, source allowlisting and output
overwrite refusal. Native tests/vet and Linux race remain part of the release gate.
The guarded deploy/debian/test-controller-package.sh runs only in an explicitly
marked disposable Docker container, never on the management VM. It covers manual
unit/mask conflict refusal, APT install/upgrade, explicit dpkg downgrade,
configuration/ledger/credential preservation, removal/purge and disabled startup.
Debian 13 amd64 package lifecycle and systemd parser are locally verified; arm64
metadata/archive generation is checked, not live arm64 execution or reboot.
