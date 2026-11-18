# E1 pilot runbook

Prepared 2026-11-03 from user-provided inventory. Controller `CONTROLLER_IP`, agent
`AGENT_IP`, node `NODE_ID`, normal user `USER`; both run core v1.1.0 revision
`0f119514b1739652d709aee7b07db742d44dc0a0`. Recheck if the environment changes.

Substitute your verified controller/agent addresses, node ID and account for all uppercase placeholders below. Actual host/account inventory is kept outside the public repository.

## 1. Dedicated target on the agent

On the **agent**, as the configured user without sudo, create a separate container from the already
available image. Pin its image ID before starting; do not pull or replace an existing
container. The shell trap provides a responsive PID 1 for a later restart experiment.
The idle command exits after one day; keep a complete measurement series within that
period or declare/recreate a new initial state before another series.

```sh
(
set -eu
image_id=$(podman image inspect --format '{{.Id}}' docker.io/library/alpine:3.24.2)
podman run --pull=never -d --name hlcp-bench \
  --network=none --label hlcp.test=semester-evaluation \
  "$image_id" sh -c 'trap "exit 0" TERM INT; sleep 86400 & wait'
podman container inspect --format \
  '{{.Id}} {{.Image}} {{.State.Status}} {{json .Config.Cmd}}' hlcp-bench
)
```

Name collision is an error; do not add `--replace` or delete an unknown existing
target. Record the returned image/container IDs and command in the run metadata.
The target has no published ports, volumes or application data.

Back up the existing private YAML, then open it:

```sh
umask 077
cp -p ~/.config/hlcp/agent.yaml \
  ~/.config/hlcp/agent.yaml.before-semester-$(date -u +%Y%m%dT%H%M%SZ)
hlcp-agent configure
```

Append this item to the **existing** `resources:` list; do not replace the file or
create a second resources key. Existing resource.control/podman capabilities suffice.

```yaml
  - id: hlcp-bench
    type: container
    adapter: podman
    target: hlcp-bench
    tags: [benchmark]
    actions: [status, restart]
```

Validate before restarting only the agent service to load the new resource:

```sh
hlcp-agent check-config && hlcp-agent restart
```

This briefly reconnects the agent; it does not restart the managed containers. Do
not use a controller/VM restart or edit the existing production resource entries.

## 2. Verify both observation paths from the controller

On the **controller**, as the configured user:

```sh
hlcp -n NODE_ID -r hlcp-bench -a status --dry-run --json
hlcp -n NODE_ID -r hlcp-bench -a status --json
ssh -F /dev/null -o BatchMode=yes -o StrictHostKeyChecking=yes \
  -o ConnectTimeout=5 USER@AGENT_IP 'id -un'
```

The first output must show a fresh read-only resolution for hlcp-bench. The second
must show terminal `success: true`, `code: OK`, `message: running`. SSH must return
the configured username with no password prompt. This checks the host/account, not yet the container;
the pilot's preflight separately checks the SSH live container state.

If SSH fails, stop here and establish authorized access before timing anything.
Do not disable host-key checking. Inspect the agent's public host-key fingerprint
in its already trusted terminal:

```sh
ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub
```

Compare that fingerprint when establishing trust from the controller. If an explicit
key is needed, store it outside the repository and pass its path with --ssh-identity.
No private key or password belongs in experiment data or the chat.

## 3. Download the evaluation branch on the controller

Keep this checkout separate from the installed binaries and previous installer
directory. If ~/hlcp-study already exists, inspect it instead of overwriting it.

```sh
git clone --branch codex/semester-evaluation --single-branch \
  https://github.com/goffamartin/homelab-control-plane.git ~/hlcp-study
cd ~/hlcp-study
git rev-parse HEAD
python3 -m unittest discover -s evaluation/scripts -p 'test_*.py'
```

These synthetic tests contact neither VM. Record the printed source commit.

## 4. Run a pilot, not the final series

Close web dashboards and live log streams. Avoid overlapping this pilot with a
deployment, backup or another measurement. Do not disable unrelated production
workloads. The run creates 12 fresh HLCP status IDs and performs 24 SSH observations.

```sh
cd ~/hlcp-study
python3 evaluation/scripts/measure_status.py \
  --node NODE_ID --ssh-target USER@AGENT_IP \
  --blocks 1 --samples 10 --warmups 2 \
  --out evaluation/data/e1-pilot-01
python3 evaluation/scripts/analyze_status.py \
  evaluation/data/e1-pilot-01/samples.csv \
  > evaluation/data/e1-pilot-01/summary.csv
cat evaluation/data/e1-pilot-01/metadata.json
cat evaluation/data/e1-pilot-01/summary.csv
```

Add --ssh-identity /absolute/path/to/key if that was required by preflight. If the
output directory already exists, retain it and use e1-pilot-02; do not erase samples.
Check that metadata status is complete. Expect 36 CSV rows plus the header: 30
measured observations and 6 labeled warm-ups. Failures remain part of the data.

Return metadata.json, summary.csv and samples.csv for review. Only after reviewing
the pilot, freeze the final protocol and run a new final series. Keep raw files and
explicit source provenance; no conclusions should depend on a single ten-sample pilot.
