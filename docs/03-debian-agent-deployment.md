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

## Journal recovery and capacity

The journal uses a persistent `.lock` file with an OS lock; do not unlink it while
an agent is running. Closing or crashing the process releases the OS lock. Only
one process can use the same journal at a time.

An upgrade from the original journal format creates `command-journal.json.v1.bak`
before writing the new node-bound version. Keep that backup for upgrade diagnosis.
If migration fails and the backup already exists, stop the agent and inspect both
files before deciding how to recover; the agent never overwrites the backup.
Current agent journals use version 3; migration from version 2 keeps `.v2.bak`.
Older binaries do not understand the current format, so a binary rollback requires a
compatible journal reader. Restoring the old backup blindly can forget actions
performed after migration and is not a safe recovery procedure.

`JOURNAL_FULL` means that capacity contains records still protected by the
24-hour replay window or unresolved outcomes. Completed expired records are
removed on later admission. Increase `journal_retention` within the byte limit
or wait for completed entries to age out. Unknown results require an explicit
operator reconciliation; they are not silently discarded. A storage error blocks
further actions until the storage problem is resolved and the journal is reopened.

## Runtime observations and F7 validation

Remove `state` from resource YAML before upgrading; it is now rejected rather
than used as current state. Optionally configure `observation_interval: 10s`
(default; valid 1s–5m). Initial state is unobserved/stale until asynchronous
runtime inspection completes. Updates follow connection, action completion and
periodic rounds, with four bounded observers and no accumulating refresh queue.
Controller `--resource-state-max-age 1m` controls freshness by receipt time.
When configuring many slow resources, allow for the full round duration in this
age limit. Upgrade controller and agent together for the typed state/update wire
contract. Only standalone containers are managed, not whole Compose/Kubernetes stacks.

Docker Engine 28.3.2 live smoke passed locally on an isolated labelled container:
start, restart, stop, status and an external stop. No host mounts, exposed ports,
or network were used; the temporary test container was removed afterward.
Live Podman smoke remains outstanding because Podman is unavailable here.

An opt-in test is provided for either runtime. Create a fresh stopped container
named `hlcp-f7-smoke-<unique>` with label `hlcp.test=f7` and a long-lived harmless
command; use no host mounts/network and a short stop timeout. Then run on its host:

```sh
HLCP_LIVE_RUNTIME=docker HLCP_LIVE_CONTAINER=hlcp-f7-smoke-YOUR_UNIQUE_SUFFIX \
  go test -v -count=1 -timeout=45s ./internal/adapter -run '^TestLiveStandaloneContainerLifecycle$'
```

Use `HLCP_LIVE_RUNTIME=podman` for Podman verification. The test checks the name
prefix, label and initial stopped state before making changes. Remove only that
verified test container afterward; never point this smoke test at a user workload.
