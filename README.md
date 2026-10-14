# HomeLab Control Plane (HLCP)

HLCP is a lightweight application-layer control plane for a small HomeLab. It is also the implementation and evaluation subject of an ANI-COM semester project.

The project deliberately focuses on a narrow, observable protocol: an outbound-connected agent registers with a controller, reports liveness and resource state, accepts an allow-listed command, and returns a deduplicated result. It is not a general-purpose remote shell, scheduler, or orchestration platform.

## Documentation map

- [Project vision and scope](docs/00-project-vision.md)
- [HLCP v1 design draft](docs/01-hlcp-v1-design-draft.md)
- [Implementation and evaluation roadmap](docs/02-implementation-and-evaluation-roadmap.md)
- [Debian agent deployment](docs/03-debian-agent-deployment.md)
- [Review findings and phased v1 completion plan](docs/04-review-and-completion-plan.md)
- [HLCP v1 acceptance contract](docs/05-v1-acceptance.md)
- [Recovery test evidence and remaining integration gates](docs/06-recovery-test-evidence.md)
- [Build, CI and dependency review](docs/07-build-and-release.md)
- [Debian management node and MCP implementation plan](docs/08-management-node-and-mcp-plan.md)
- [Bounded telemetry and audit](docs/09-telemetry-and-audit.md)
- [Scoped API credentials and rotation](docs/10-scoped-api-credentials.md)
- [Bounded controller diagnostics API](docs/11-bounded-diagnostics-api.md)
- [Debian management deployment and recovery](docs/12-debian-management-deployment.md)
- [Native Debian controller package](docs/13-debian-controller-package.md)
- [CLI setup and rootless agent installation](docs/14-cli-setup-and-agent-installation.md)
- [Onboarding and agent-profile design notes](docs/15-onboarding-and-agent-profile-design-notes.md)
- [Plug-and-play onboarding implementation plan](docs/16-plug-and-play-implementation-plan.md)
- [Generic agent execution profiles and migration](docs/17-generic-agent-profiles.md)
- [Local script resources and bounded Linux execution](docs/18-script-resource-contract.md)
- [Checkpoint A / preview.4 release evidence and open VM gates](docs/19-checkpoint-a-release-evidence.md)
- [Read-only setup planning core (P04a, internal foundation)](docs/20-setup-planning-core.md)
- [Confirmed local setup transactions and recovery (P04b)](docs/21-setup-transactions.md)
- [Offline read-only doctor (first P04c block)](docs/22-offline-doctor.md)
- [Imported TLS validation and generic agent setup planning (P05a)](docs/23-imported-agent-setup-core.md)
- [Imported agent setup CLI (P05b, preview and confirmed apply)](docs/24-imported-agent-setup-cli.md)
- [Confirmed private config-directory provisioning (P05c)](docs/25-setup-directory-provisioning.md)
- [Imported controller setup planning (P05d)](docs/26-imported-controller-setup-core.md)
- [Imported controller setup CLI and deferred credentials (P05e)](docs/27-imported-controller-setup-cli.md)
- [Controller service activation core and partial outcomes (P05f)](docs/28-controller-activation-core.md)
- [Native Debian controller activation CLI (P05g)](docs/29-controller-activation-cli.md)
- [Private non-root client profile provisioning core (P05h)](docs/30-client-provisioning-core.md)
- [Confirmed controller-to-user CLI access handoff (P05i)](docs/31-client-access-handoff.md)
- [Fresh-user .config/hlcp bootstrap (P05j)](docs/32-client-config-parent-bootstrap.md)
- [Agent service activation core and system credential paths (P05k)](docs/33-agent-service-activation-core.md)
- [Native agent activation CLI for initial imported setup (P05l)](docs/34-native-agent-activation-cli.md)
- [Resource readiness rules and native integration gate (P05m)](docs/35-resource-readiness-core.md)
- [Native Linux user resource metadata preflight (P05n)](docs/36-native-resource-readiness.md)
- [Fresh Debian controller package and imported setup acceptance (P05o)](docs/37-controller-package-setup.md)
- [System resource sandbox constraints and controlled refusal (P05p)](docs/38-system-resource-sandbox-constraints.md)
- [Explicit-consent system sandbox probe lifecycle core (P05q)](docs/39-system-sandbox-probe-core.md)
- [Fixed sandbox metadata inspector and strict private IPC (P05r)](docs/40-fixed-sandbox-metadata-inspector.md)
- [Bounded native sandbox launcher and verified cleanup contract (P05s)](docs/41-native-sandbox-launcher.md)
- [Native source-bound resource probe CLI (P05t)](docs/42-native-resource-probe-cli.md)
- [One-shot sandbox readiness revalidation core (P05u)](docs/43-sandbox-readiness-revalidation.md)
- [Architecture decisions](docs/adr/README.md)

## Status

Build/test toolchain is pinned to Go 1.27.2 (`.go-version`, `go.mod`). All three
binaries support `--version` without TLS/config/API setup; `hlcp --version --json`
returns machine-readable metadata. From a clean checkout with cached modules,
`go run ./tools/release -version v0.1.0 -out dist/v0.1.0` creates Linux amd64/arm64
and Windows amd64 binaries, native controller and runtime-independent agent .deb packages for Linux,
a deterministic deployment archive, manifest and `SHA256SUMS`.
This does not publish a GitHub release or install any service. See the build guide
for local validation, reproducibility checks, artifact permissions and CI scope.

The v1 implementation currently includes:

- TLS 1.3 mutual authentication between controller and agent;
- bounded binary framing and canonical CBOR protocol payloads;
- inventory/state synchronization and heartbeat-based liveness;
- live typed resource observations with session-scoped updates and freshness;
- allow-listed `systemd`, Docker, and Podman resource actions;
- controller-side command correlation and agent-side command-result deduplication;
- ACK-aware bounded retries, a durable controller ledger and read-only recovery;
- a loopback-only HTTP API consumed by the `hlcp` CLI;
- bounded structured audit events and fixed-cardinality operational telemetry;
- scoped, bounded controller-audit queries and authorized global metric snapshots.

The CLI navigates the discovered inventory progressively:

```text
hlcp -n
hlcp -n debian-01 -a status
hlcp -n debian-01 -r
hlcp -n debian-01 -r example-catalog -a
hlcp -n debian-01 -r example-catalog -a restart
hlcp -n debian-01 -r example-catalog -a restart --command-id op-1 --json
hlcp -n debian-01 --result op-1 --json
```

Start the controller API on its default local address (`127.0.0.1:9444`) with:

```text
hlcp-controller -listen :9443 -api-listen 127.0.0.1:9444 -api-token-file /etc/hlcp/api.token -ledger-file /var/lib/hlcp/controller-operations.json -cert controller.pem -key controller-key.pem -ca ca.pem
```

`hlcp` targets that local address by default. Use `hlcp --help` for the explicit long-form selectors and `--api` override.

The token-file mode is a compatibility operator profile. For separate scoped
observer/operator identities, use controller `-api-policy-file` instead; see the
credential guide and `examples/api-policy.json`. Policy mode never adds an
implicit legacy admin token. Observers read cached GET snapshots and cannot call
POST commands, including resource `status`. The internal API remains loopback-only.

Short selectors `-n`, `-r`, `-a` have optional values; long singular selectors
(`--node`, `--resource`, `--action`) require values and plural selectors list
choices. Repeated/conflicting selectors and missing selected parents are errors.
Default output is readable text; `--json` preserves the API response on stdout.
Diagnostics go to stderr. Resource execution prints its ID and lookup hint to
stderr before submission. `--command-id <id>` supplies a stable ID; `--result <id>`
with a selected node only reads the retained controller operation, never resends.
Lookup cannot be combined with resource/action/dry-run. Keep the original payload
if deliberately submitting the same ID again; a changed payload is a conflict.

Exit codes: `0` success, `2` invalid/rejected request, `3` definitive failed action,
`4` connection/API/response error, `5` timeout or unresolved operation (including
pending/unknown/expired lookup). A failed or unresolved result remains on stdout;
API rejection and diagnostics are on stderr. After timeout, look up the original
ID using the same `--api`/`--api-token-file`; do not assume non-execution or create
a new ID to repeat the action. Lookup availability follows the ledger retention
window, not an unlimited history.

Controller retry defaults are `--command-ack-timeout 5s`,
`--command-result-timeout 30s` and `--command-max-attempts 6`. Acknowledged
commands are not resent for execution; ACK/result timeout is an unresolved
outcome, not proof of non-execution. `--ledger-file` is required and defaults to
`/var/lib/hlcp/controller-operations.json`. A restart reconciles existing IDs using
read-only journal queries, never automatic re-execution. Retrieve the operation
and any late result through authenticated `GET /v1/nodes/{node}/commands/{id}`.

`--dry-run` now calls authenticated `POST /v1/nodes/{node}/resolve` with the same
command payload used for execution. The server checks inventory, actions, online
state and advertised capabilities without admitting a ledger record or sending a
command. The plan is a snapshot, not a guarantee against later session changes.
Known same-ID operations resolve as existing read-only lookups even without current
inventory; a different payload is a conflict. Node-only `status` reads cached
metadata and remains available offline; unsupported node actions are errors.
`hlcp -n debian-01 -r example-catalog` returns real resource detail/observation.
Agents with resources must advertise `resource.control` and each configured adapter
(`docker`, `podman`, `systemd`, or `script`); invalid declarations fail validation.
Script/task resources execute fixed agent-local commands on Linux without a
container runtime. Task actions are never assumed read-only by their names;
they are not polled automatically. See the script runbook for limits and recovery.
The ledger is bounded to 1024 operations / 8 MiB; definitive results have at least
24-hour retention, and unresolved unknown history is never automatically evicted.
Upgrade controller and agents together for the pre-release query/state extension.

Agent YAML no longer accepts resource `state`; remove it and optionally set
`observation_interval: 10s` (default). Runtime queries refresh at connect, after
actions and in periodic bounded rounds. Node detail/status exposes `observed_at`,
diagnostics and `stale`; offline data is never presented as fresh. Controller
`--resource-state-max-age` defaults to 1m. Upgrade both binaries together for the
pre-release typed-state/STATE_UPDATE contract. Docker live lifecycle smoke is
verified; live Podman validation is still required.

The controller and CLI read a shared local bearer token from `/etc/hlcp/api.token`
by default. Override the file with `--api-token-file`. On Unix the token file must
have private permissions (0600). Use a randomly generated token of at least 32
characters; never commit it. Browser-origin requests and non-loopback API binds
are rejected.

Agent certificates require an explicit client-auth EKU and exactly one HLCP URI
SAN, for example `urn:hlcp:node:debian-01`, matching the YAML `node_id`. The
controller certificate requires server-auth EKU and the configured server name.
Both peers must negotiate `hlcp/1` ALPN.

The design document remains the source of truth for unfinished production hardening and evaluation scenarios.
