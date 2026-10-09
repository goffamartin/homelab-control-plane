# Debian agent deployment

This procedure deploys only the HLCP agent. It does not create a certificate
authority and it deliberately never copies private keys into the repository.
Run the commands on the target Debian node as an administrator.

## 1. Build and install the binary

Build a static-ish Linux binary from a trusted checkout or release artifact:

```text
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o hlcp-agent ./cmd/hlcp-agent
sudo install -o root -g root -m 0755 hlcp-agent /usr/local/bin/hlcp-agent
```

Create the dedicated service account and persistent journal directory. Docker
resources need access to the local Docker socket, so only enable the `docker`
supplementary group on nodes where that is intended:

```text
sudo useradd --system --user-group --home-dir /nonexistent --shell /usr/sbin/nologin hlcp
sudo install -d -o hlcp -g hlcp -m 0750 /var/lib/hlcp
sudo usermod -aG docker hlcp
```

Membership in `docker` is effectively privileged on typical Docker hosts. Do
not use it merely for future convenience. For Podman, configure access to the
specific rootless runtime instead of adding this group blindly.

## 2. Install configuration and certificates

Create `/etc/hlcp/agent.yaml` from [`examples/agent.yaml`](../examples/agent.yaml).
Its `node_id`, controller name and resource mapping must identify this node;
adapter targets remain agent-local. Set `journal_file` to
`/var/lib/hlcp/command-journal.json`.

Place the agent certificate, private key and CA bundle in `/etc/hlcp`. The
agent certificate must include a client-auth EKU and URI SAN
`urn:hlcp:node:debian-01` matching this agent's `node_id`; certificates without
that identity are rejected. The controller certificate must include server-auth
EKU and the configured server DNS/IP SAN.

The private key must be readable only by root and the `hlcp` group:

```text
sudo install -d -o root -g hlcp -m 0750 /etc/hlcp
sudo install -o root -g hlcp -m 0640 agent.key /etc/hlcp/agent.key
sudo install -o root -g hlcp -m 0644 agent.crt /etc/hlcp/agent.crt
sudo install -o root -g hlcp -m 0644 ca.crt /etc/hlcp/ca.crt
sudo install -o root -g hlcp -m 0640 agent.yaml /etc/hlcp/agent.yaml
sudo -u hlcp /usr/local/bin/hlcp-agent -config /etc/hlcp/agent.yaml -check-config
```

Do not commit any of these files. The sample paths already match this layout.

## 3. Enable the service

```text
sudo install -o root -g root -m 0644 deploy/systemd/hlcp-agent.service /etc/systemd/system/hlcp-agent.service
sudo install -o root -g root -m 0644 deploy/systemd/hlcp-agent.tmpfiles.conf /etc/tmpfiles.d/hlcp-agent.conf
sudo systemd-tmpfiles --create /etc/tmpfiles.d/hlcp-agent.conf
sudo systemctl daemon-reload
sudo systemctl enable --now hlcp-agent
sudo systemctl status hlcp-agent
sudo journalctl -u hlcp-agent -f
```

The controller must be reachable through the intended management network. The
agent opens the outbound mTLS connection itself; the node need not expose an
agent TCP listener.

## 4. Smoke check and rollback

On the controller host, confirm registration and inspect configured resources:

Prepare a random local API bearer token outside this repository and install it
on the controller host with mode 0600, owned by the controller service account.
The CLI user needs read access through a private local copy or the same account.
Pass `--api-token-file <private-path>` to controller and CLI if the default
`/etc/hlcp/api.token` is not suitable. This token is unrelated to agent certificates.

```text
hlcp -n
hlcp -n debian-01 -r
hlcp -n debian-01 -r example-catalog -a
```

Use `--dry-run` first, then only a non-critical resource action:

```text
hlcp -n debian-01 -r example-catalog -a restart --dry-run
hlcp -n debian-01 -r example-catalog -a restart
```

To roll back the service while preserving the journal for diagnosis:

```text
sudo systemctl disable --now hlcp-agent
sudo rm /etc/systemd/system/hlcp-agent.service
sudo systemctl daemon-reload
```

Do not delete `/var/lib/hlcp/command-journal.json` during an incident unless
you explicitly accept losing the command deduplication history.
