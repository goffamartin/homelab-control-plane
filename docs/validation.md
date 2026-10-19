# v1.0 validation and limitations

Status date: **2026-10-19**. This page separates observed behavior from proposed
tests. The v1.0 release scope is the single-controller Debian/rootless-Podman
operational path, not completion of every item in the original research plan.

## Live HomeLab evidence

The following was observed on Debian 13, Linux amd64, with controller and user
agent on separate hosts. The latest live checks used `v0.1.0-preview.10`, source
`9ce4925d001cfd03cacbe4153f559339f99518a3`; earlier onboarding/log checks used
the corresponding preview versions. These observations are not a claim that a
new v1.0 binary has already been installed on those VMs.

| Scenario | Observed result | Evidence boundary |
|---|---|---|
| Managed installation, pairing and YAML resources | Agent online with explicit Podman inventory | Operator-run installation and command output |
| User service start/restart/stop/status | State reported correctly; stop retained boot enablement | Packaged user service |
| Bash completion | Static command/flag choices worked after loading completion | Not every dynamic candidate/error case tested live |
| Container actions | Status/restart/stop returned runtime results | Runtime success is not application health |
| Live logs | Repeated stdout records arrived; Ctrl+C ended reading | No lossless reconnect/retention claim |
| Stopped-container logs | Retained lines returned; follow ended normally | Depends on runtime log behavior |
| Historical JSON/filtering | Versioned records, literal filter, bounded page and next cursor returned | Following that cursor was not observed live |
| Agent reboot/reconnect | Offline then online during the operator-run reboot test | Reboot/absence of login not independently verified with boot IDs |
| Controller reboot | Changed boot ID, service active, agent reconnected | One reboot scenario, not a crash/power-loss campaign |
| Durable result lookup | Pre-reboot result remained completed after controller reboot | One recorded operation |
| Same-ID restart resubmission | Same result; retained operation showed one attempt | No independent side-effect execution counter |
| Same ID, different payload | `COMMAND_CONFLICT` | No second operation admitted |
| Management API binding | Listener observed at `127.0.0.1:9444` | Host-local socket inspection |

## Automated and local integration checks

The source has full Go tests/vet on Windows and Linux, Linux race tests, bounded
protocol/parser/journal fuzz campaigns, reproducible cross-target builds and
package-content checks. Local integration tests exercise real CLI/mTLS processes,
recovery boundaries, strict parsing, scoped authorization and bounded output.
CI results are attached to the exact source revision in GitHub Actions.

Disposable Debian process/container checks cover signed packages and onboarding
without a booted systemd manager. Fake-host service tests cover preflight,
stale plans and partial outcomes; they do not replace VM tests. Docker log
reading also has a local runtime smoke check, not equivalent live Debian VM
qualification. See [development](development.md) for reproducible checks.

## Deferred for this release

These gaps are accepted release limitations, not passed tests:

- Full stopped-backup restoration on an isolated VM and complete rollback rehearsal.
- Live leaf renewal/expiry, issuer maintenance, revoked identity and deliberate
  replacement/re-pairing through the complete certificate lifecycle.
- Extended network partition/packet-loss, power-loss, load and long-duration soak testing.
- Equivalent booted-VM qualification for Docker, system-scope/mixed scripts and
  systemd adapters; Linux arm64 runtime qualification.
- Full live JSON streaming/pagination error matrix and every installer failure path.

Linux arm64 packages and Windows binaries are built, but building is not runtime
qualification. Debian 13 rootless Podman is the reference deployment. Windows
artifacts support development/CLI usage, not a Windows service deployment promise.

## Product limits

One controller, explicitly configured resources, bounded command retention and
bounded log reads. No unlimited deduplication, automatic rollback, lossless log
replay, HA, interactive shell/console, scheduler or container orchestration.
Inspect uncertain outcomes rather than retrying with a new command ID.

The [original acceptance matrix](https://github.com/goffamartin/homelab-control-plane/blob/v1.0.0/docs/archive/05-v1-acceptance.md) also required broader
deployment/recovery qualification and repeatable netem research measurements.
Those original criteria are **not all complete**. The research deliverable and
its open experiments remain separate from this narrower v1.0 release decision.
