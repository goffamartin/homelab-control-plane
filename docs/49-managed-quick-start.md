# Managed Quick Start — Debian

Status: implemented in source and tested with installed packages/processes in
disposable Debian 13 containers. **Booted systemd/reboot acceptance is NOT RUN.**
Preview.4 does not contain this workflow. Use the next explicitly published,
signed candidate, or the exact-SHA local candidate for isolated acceptance.
Preview.6 fixes prompt timing; preview.7 adds early CLI-path preflight,
explicit primary-group trust and resumable setup settings (see docs/54).
This document does not authorize touching or cleaning existing HomeLab VMs.

## What the normal user does

| Machine | User action | HLCP handles |
|---|---|---|
| Controller | Install package, run setup wizard, retain encrypted backup | CA, issuer, server certificate, tokens, private paths and service configuration |
| Controller | `hlcp pairing create --node debian-01` | Expiring one-use invitation, pinned CA fingerprint, durable admission |
| Agent | Install package, run join wizard, paste code privately | Local private key/CSR, authenticated enrollment, certificate, empty resource config |
| Agent | Select local resources/actions in wizard | Validated allowlist, private backup, optional explicit service restart |
| Controller | Discover resources and invoke an explicitly enabled action | mTLS, command IDs, deduplication and recorded results |

No OpenSSL, YAML editing, copying private keys, extra CA daemon, Docker/Podman
dependency for script-only agents, or separate operator ACL configuration is
required for this path. LAN/WireGuard reachability, correct time and DNS are
still prerequisites; HLCP does not configure the network or VPN.

## 1. Obtain a trusted installer

Debian prerequisites: `ca-certificates`, `openssl`, `git` and `gh` for this private
GitHub repository; `curl` is used only with the later `--public` download path.
Run GitHub authentication as the actual user, not with sudo. No PAT in argv,
config snippets, screenshots or this repository. A public repository is not
required and is not created by the helper.

The release build produces `hlcp-install.sh`, binaries, .deb packages,
`build-manifest.json`, `SHA256SUMS` and detached Ed25519 signatures for both
metadata files. Choose one exact signed release version; never `latest`.
After setting `VERSION` to an actually available signed candidate:

```sh
gh release download "$VERSION" --repo goffamartin/homelab-control-plane \
  --pattern hlcp-install.sh
less hlcp-install.sh
```

Bootstrap trust is explicit: obtain/review the helper from a trusted exact source
commit, or compare its SHA256 to that reviewed source. Do not execute a helper
from an untrusted attachment merely because it contains a public key. Never
curl-pipe a downloaded script to root. The embedded public-key **DER SHA256** is
`ff63eb800f0279e5c4b5c5281845955232eb73b07835e1fd5e3c5280dd144275`.
Downloaded releases cannot replace this trust key. Key rotation requires a
separate reviewed helper/source update and a new trusted bootstrap.

## 2. Controller

Run the helper as yourself. It downloads with your GitHub login, verifies signed
metadata/digests, then asks sudo only for package/service operations:

```sh
sh hlcp-install.sh controller --version "$VERSION"
sh hlcp-install.sh controller --version "$VERSION" --configure --apply --yes
```

The first command only verifies the download. The second installs the package
and opens `hlcp-controller setup`. The wizard asks for the controller's private
IP:9443, DNS/IP, actual existing CLI account, start/boot consent and a repeated
hidden backup password (at least 16 characters). HTTPS enrollment defaults to
the same management IP:9445. API 9444 remains loopback-only.

Setup creates a new root CA, online issuing CA and certificates automatically.
Keep `/root/hlcp-root-backup.json` and its password **off this VM**; setup asks
for acknowledgement. The root private key exists only in the encrypted export,
not as a runtime credential. The online issuer key must remain accessible to
the dedicated controller process. Root compromise is outside this protection.

Selecting the explicit CLI user provisions its private profile; thereafter run
`hlcp -n` as that user without sudo or repeated token arguments. Successful setup
and service startup do not themselves prove agent registration.

The controller itself runs as `hlcp-controller`; the selected CLI account is a
separate existing human account, not the service identity. Before CA creation,
setup checks the service account, private destinations and the CLI profile path.
If a user-owned parent such as `.config` is writable by that user's primary group,
the wizard names the path/group and asks whether you trust its members. Accepting
does NOT chmod `.config`: only the new HLCP directory/files are private 0700/0600.
World-write, foreign ownership/groups, links and unsafe private profiles remain
refused. Declining trust can skip CLI setup without blocking controller setup.
Noninteractive consent is `--trust-user-group`; the standalone
`configure-client` command accepts the same explicit flag.

Setup retains selected settings privately in `managed-setup-request.json`.
After interruption, rerun `sudo hlcp-controller setup`; review the saved settings
and confirm completion. A configured controller keeps its CA, registry and history
and does not ask for another root-backup password. Preview.6 installations without
saved settings recover their existing network identity from bootstrap metadata
and ask only for choices that were not previously saved. `--cli-user ACCOUNT`
overrides the saved account; `--skip-client` completes without exporting another
profile (it does not revoke existing tokens). A late CLI error retains the full
controller state and shows the specific cause and completion instructions.

## 3. Agent — choose execution account, not a containerized HLCP service

HLCP is a native system process or systemd user process. Profiles do not put
HLCP inside Docker/Podman. For rootless Podman, run the agent as the real owner
of the containers (for example `martin`). Do not create another account simply
to satisfy a profile. Docker/Podman access is neither installed nor granted
silently; Docker socket membership can be equivalent to root access.

Install the agent (default path shown here is user scope):

```sh
sh hlcp-install.sh agent --version "$VERSION" --scope user --apply --yes
sudo hlcp-agent prepare-user --user "$(id -un)" --apply --yes
```

`prepare-user` explicitly enables the existing user's manager at boot; it does
not create keys/containers or impersonate the user. `dbus-user-session` and a
working `systemctl --user` session are needed. Rootless Podman additionally needs
Podman and valid subordinate UID/GID mappings, provisioned separately by the
administrator. Choose script-only if no container runtime is needed.

On the controller, as the configured CLI user:

```sh
hlcp pairing create --node debian-01
```

This prints a secret code once, valid for ten minutes. Do not record it in logs
or screenshots. On the agent, as the actual container/resource owner:

```sh
hlcp-agent join
```

Paste the code into its hidden prompt; confirm pairing and optional start/boot.
The private key is generated on the agent and is not transmitted. No resources
are enabled initially. After a lost response/interruption, keep the private
state and run `hlcp-agent join --resume --apply --yes`. Do not delete it and
create another key. If the invitation/retry window expired, use explicit new
controller pairing with `--replace --yes`, and explicit agent `--re-pair`.

For unattended operation use `--pairing-stdin` or `--pairing-file` pointing to a
private, caller-owned mode-0600 file in a private directory, never a code in argv.
Noninteractive calls preview by default and require `--apply --yes` for changes.
For a dedicated system agent use root `hlcp-agent join`; its fixed service
account/config paths are provisioned after consent. No container socket/root
privilege is granted by joining.

## 4. Explicit resources/actions

As the agent's execution/configuration owner:

```sh
hlcp-agent resources
```

The wizard supports `script`, `podman`, `docker` and `systemd`. It never enables
all discovered resources automatically. Script input is an absolute executable,
literal argument list, working directory, named action and bounded timeout, not
a shell string sent from the controller. Script actions are treated as mutating
even when named `status`. Each update retains one private previous config.

Examples without YAML editing:

```sh
hlcp-agent resources discover --adapter podman
hlcp-agent resources configure --id example-catalog --adapter podman \
  --target example-catalog --actions status,restart --restart --apply --yes
hlcp-agent resources configure --id kernel --adapter script --actions status \
  --executable /usr/bin/uname --arg=-s --restart --apply --yes
```

For Docker change only `--adapter docker`. `discover` runs bounded local runtime
metadata listing as the actual non-root owner, never actions or implicit imports.
For service resources choose `--adapter systemd --target example.service`.
System-scope actions still need the existing sandbox-readiness gate and actual
OS permissions; failures are not solved by granting root or disabling sandboxing.
Use `--replace` to replace an existing resource explicitly, or `resources remove
--id NAME --apply --yes`. Without `--restart`, saved changes take effect only at
the next explicitly requested restart.

On the controller:

```sh
hlcp -n
hlcp -n debian-01 -r
hlcp -n debian-01 -r example-catalog -a
hlcp -n debian-01 -r example-catalog -a status
```

Check online registration and expected inventory before any mutating action.
Retain a command ID for result lookup after a timeout; never issue a new ID just
because a prior result was lost.

## Certificates and updates

Managed leaves last up to 90 days and renew after about 30 days while the valid
agent/controller can communicate; renewal is atomic and hot-loaded. A machine
offline past certificate expiry needs explicit re-pairing. Issuer (one year)
maintenance needs the encrypted offline root backup and password; the 10-year
root itself eventually needs planned replacement. There is no promise that a
certificate can never expire. `hlcp certificates` and `hlcp-agent certificates`
show current managed identities, renewal/retry/expiry metadata. Issuer warnings
start 90 days before expiry; leaf warnings use 30/14/7/1 days. Lifecycle changes
also emit bounded, deduplicated journal metadata. Imported PKI remains externally
renewed; the original file-oriented doctor is not a live managed-health verdict.

For update, choose another exact signed `VERSION`, preview first, then explicitly
apply. Controller first, then agents:

```sh
sh hlcp-install.sh controller --version "$VERSION" --update
sh hlcp-install.sh controller --version "$VERSION" --update --restart --apply --yes
sh hlcp-install.sh agent --version "$VERSION" --scope user --update --restart --apply --yes
```

The helper preserves conffiles, refuses downgrade and snapshots stopped-service
config/state privately before APT. A running service is not stopped/restarted
without `--restart`; otherwise stop it yourself first. Failed installation/start
does not restore older journals. Recheck registration/inventory after update.
User compatibility profiles can select `--unit hlcp-agent-podman.service`; this
does not migrate/disable another unit silently. Offline image maintenance alone
can use `--offline-stopped`, never as a bypass on a booted VM.

Compatibility metadata distinguishes protocol 1, managed-state schema 1, command
journal schema 3 and operation-ledger schema 1; it does not pretend all persisted
files share one schema number. Unsupported future formats are refused.

Successful download staging is removed. Failed staging is retained privately for
inspection. Update recovery snapshots are created only per explicit update, not
per heartbeat; retain/prune them under the administrator's backup policy. Managed
registry/serials and previous identity/resource backups have fixed bounds.

See [migration/recovery](50-managed-migration-and-recovery.md) and the
[whole-system live gate](51-managed-live-acceptance.md) before touching existing VMs.
