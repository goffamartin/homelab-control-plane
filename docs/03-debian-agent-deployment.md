# Debian agent deployment

New installation and upgrade procedures are maintained in the
[F10D management / Docker / rootless Podman runbook](12-debian-management-deployment.md).
It supersedes the original direct-file unit, unconditional Docker membership
and manual build instructions. Do not install current units using legacy
/etc/hlcp/agent.yaml paths: they now load /etc/hlcp/agent/ credentials.
Preserve /var/lib/hlcp/command-journal.json and its node identity during upgrade.

The runtime recovery constraints and historical live-smoke evidence below
still apply. Production keys and API tokens must never enter this repository.

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
