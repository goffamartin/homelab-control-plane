# Validation and limits

The reference setup uses one controller and a Debian 13 agent that runs
rootless Podman containers. The controller and agent normally run on separate
machines. Use an application-specific health check when you need to know that
a service can handle requests.

## What we checked

| Area | Observed result |
|---|---|
| Installation and agent service | The packaged agent joined the controller, published its configured resources and responded to start, stop and status checks. |
| Inventory and actions | The live HomeLab listed resources by tag. An independent Podman check confirmed a new start time and a running container after all 90 measured restarts across HLCP and both SSH baselines. |
| Container logs | The CLI read retained lines and followed new output. The deployed web client also read container logs over HTTPS. |
| Recovery | On a single-machine test setup, the controller detected both orderly stops and silent pauses. The agent reconnected and a live status check passed in all ten trials. |
| Access and result lookup | The test controller rejected untrusted certificates, invalid credentials and a restart outside the allowed actions. Repeating a request with the same ID returned the saved result. Changing the payload with the same ID returned a conflict. |

The [evaluation report](https://github.com/goffamartin/homelab-control-plane/blob/b9a6d0406a1fdf66de3d13a7d093eda14bd29137/evaluation/report.md)
gives the methods, test scope and links to the data. GitHub Actions records
automated checks against each tested revision. See [development](development.md)
for the commands.

## Limits

- The experiments cover one VM pair for command latency and short resource
  measurements. Recovery and access tests ran on a disposable setup on one VM.
- HLCP retains a bounded command history. If a result is uncertain, inspect the
  service and look up the original command ID before issuing a new action.
- Container log history depends on the runtime. Follow sessions have limits and
  can report gaps. See the [container log API](reference/56-container-logs.md).
- Full backup restoration, certificate lifecycle, power loss, long network
  partitions and extended load testing require separate qualification.
- Equivalent live qualification for Docker, system-scope agents, systemd and
  script adapters, and Linux arm64 remains open.
