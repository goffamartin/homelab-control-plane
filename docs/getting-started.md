# Getting started

This guide installs a controller and a rootless Podman user agent on separate
Debian 13 hosts. Run ordinary commands as your normal user; use `sudo` only where
shown. Replace `192.168.0.15`, `debian-01` and `app` with your values.

## 1. Prepare both hosts

You need systemd, an existing non-root account, sudo access for package setup,
and network reachability from the agent to the controller:

| Controller port | Purpose | Access |
|---|---|---|
| TCP 9443 | Agent mTLS connection | Agent hosts |
| TCP 9445 | Managed HTTPS enrollment | Hosts being paired |
| TCP 9444 | Authenticated management API | Controller loopback only |

Keep 9444 on `127.0.0.1`; do not publish it as an unauthenticated remote API.
The controller address/DNS name must remain reachable and match its certificate.
The installer does not configure your firewall, DNS or clock synchronization.

On both hosts:

```sh
sudo apt update
sudo apt install ca-certificates curl openssl bash-completion
```

On the agent, install and verify rootless Podman separately:

```sh
sudo apt install podman uidmap dbus-user-session
podman info
podman ps -a
```

Run Podman as the account that will run the agent, without sudo. Existing
containers under another user/root are not automatically accessible. Fix runtime
setup, including subordinate UID/GID mappings if needed, before configuring HLCP.

## 2. Obtain the installer

On each host, in a new working directory:

```sh
VERSION=v1.0.0
HLCP_DOWNLOAD_DIR=$(mktemp -d "$HOME/hlcp-download.XXXXXXXX")
cd "$HLCP_DOWNLOAD_DIR"
curl --disable --proto '=https' --proto-redir '=https' --tlsv1.2 \
  --fail --show-error --location --max-redirs 3 \
  --connect-timeout 10 --max-time 120 --output hlcp-install.sh \
  "https://github.com/goffamartin/homelab-control-plane/releases/download/$VERSION/hlcp-install.sh"
```

The repository and release assets are public: no GitHub account, token or `gh`
installation is required. The new directory avoids overwriting an older helper.
Keep the same shell open for the following steps so `VERSION` remains set.
Use `--public` on every installer invocation below: the helper retains its
authenticated download mode by default for compatibility with private repositories.

Bootstrap trust matters: the helper must come from a repository/release you
trust. Review its source and compare its SHA-256 with the release's trusted
announcement before executing it. A checksum downloaded from the same untrusted
source is not independent authentication. For this release's helper:

```sh
printf '%s\n' '91bb55f96c8afbad6ae51595e6f054f0eac121df4d4169ef8d679661cbb2529e  hlcp-install.sh' \
  | sha256sum -c -
```

Continue only if it reports `OK`; stop on mismatch. The helper then verifies the signed
manifest, exact version, architecture and package checksums against its pinned
release key before installation. It does not run a download piped into a shell.

## 3. Install and set up the controller

On the controller, preview first, then apply:

```sh
sh hlcp-install.sh controller --version "$VERSION" --public
sh hlcp-install.sh controller --version "$VERSION" --public --configure --apply --yes
```

The wizard asks for:

- Controller endpoint, for example `192.168.0.15:9443`.
- Certificate DNS/IP name, for example `192.168.0.15`.
- An existing non-root user to receive local CLI access.
- Whether to start the controller and enable startup at boot.
- Confirmation and a root-backup password of at least 16 characters, entered twice.

The encrypted root backup defaults to `/root/hlcp-root-backup.json`. Retain it
and its password **off this VM**, preferably separately. Confirm the backup
warning only after understanding this obligation. Do not send passwords, tokens
or pairing codes in chat or put them in command history.

The controller service runs as `hlcp-controller`, not as the selected CLI user.
The selected user receives a private local API profile. Setup does not run every
agent under that account. See [accounts and permissions](security.md).

In a new Bash session as the selected CLI user:

```sh
hlcp-controller --version
systemctl is-active hlcp-controller.service
hlcp -n
```

`(none)` is expected before any node has paired. If setup stops partway through,
follow its reported next step; do not delete existing identity/history to retry.

## 4. Install and pair the agent

On the agent, as the actual Podman owner:

```sh
sh hlcp-install.sh agent --version "$VERSION" --public --scope user --apply --yes
sudo hlcp-agent prepare-user --user "$(id -un)" --apply --yes
systemctl --user is-system-running
loginctl show-user "$(id -un)" -p Linger
```

User preparation enables the required user-manager/linger setup with explicit
consent. It does not grant container privileges or install a container runtime.
Linger allows the user service to run after logout and at boot.

On the controller:

```sh
hlcp pairing create --node debian-01
```

On the agent:

```sh
hlcp-agent join
```

Paste the single-use code at the hidden prompt. Choose start/boot startup when
asked. Pairing creates identity/configuration, not resources or containers.
Check from the controller that `hlcp -n` reports `debian-01` online.

## 5. Enable one existing container

On the agent:

```sh
hlcp-agent resources discover --adapter podman
hlcp-agent configure
```

The editor opens the generated YAML. Keep its identity, TLS, managed-directory
and journal fields. Replace the `capabilities` and `resources` sections with:

```yaml
capabilities: [resource.control, podman]
resources:
  - id: app
    type: container
    adapter: podman
    target: app
    actions: [status, start, stop, restart, logs]
```

Use the actual existing container name for `target`; `id` is its HLCP name.
Choose only actions you want to authorize. Then:

```sh
hlcp-agent check-config
hlcp-agent restart
hlcp-agent status
```

Schema validation alone does not test runtime access. A successful restart
confirms service state, not controller connectivity; verify from the controller:

```sh
hlcp -n debian-01 -r
hlcp -n debian-01 -r app -a status
hlcp -n debian-01 -r app -a logs --tail 20
hlcp -n debian-01 -r app -a logs --follow --tail 0
```

A stopped container or one that produces no output may return no new lines;
follow can end when the runtime's log stream ends. Ctrl+C stops reading only.
Do not start/restart a container merely to test logs unless that mutation is intended.

## Next steps

[Configuration](configuration.md) covers custom actions and other profiles;
[CLI](cli.md) covers completion/JSON; [operations](operations.md) covers updates
and troubleshooting. Read [validation and limitations](validation.md) before
depending on recovery or certificate maintenance scenarios.
