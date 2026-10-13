# Onboarding, certificate management and agent profiles — discussion notes

Recorded 2026-10-13. This captures agreed direction and open design questions,
not a complete implementation plan or implemented command contract. Do not run
proposed commands or treat illustrative configuration as accepted by preview.3.

## Agreed certificate-management direction

- First-time setup offers two modes: HLCP-managed certificates (recommended
  homelab default), or externally supplied certificates/keys and CA trust bundles.
- Managed setup aims to hide OpenSSL/file-transfer steps behind controller setup
  and single-use, expiring, node-bound pairing invitations. Invitations include
  a trust fingerprint; agents authenticate the controller before sending secrets.
- Agent private keys are generated and retained on their node. The controller
  chooses certificate identity/EKU from admission policy, not arbitrary CSR fields.
- Target managed PKI separates a backed-up/offline encrypted root key from an
  online issuing authority on the controller. Final issuer implementation,
  unattended key protection and existing-CA migration remain design work.
- Managed mode needs automated agent/controller renewal with retry, expiry
  visibility and warnings. Exact lifetimes/renewal thresholds are not settled
  (30/90-day leaf examples were discussion options, not configuration defaults).
- Automatic renewal is not an expiry guarantee. Long-offline/expired agents
  re-pair; managed containers keep running independently of HLCP connectivity.
- Imported certificates do not require the external CA private key. Their owner
  or existing PKI handles issuance/renewal unless an explicit issuer integration
  is added; HLCP should validate imports and expose expiry/reload diagnostics.
- Removing a node must stop renewal and disconnect/reject its runtime identity,
  not merely wait for expiry. Signing-key compromise needs a trust-rotation plan.
- Inspect current CA basicConstraints before migration: a pathlen:0 root cannot
  support an intermediate. Preserve overlapping trust during a planned migration.
- Existing manual PKI remains supported; setup must never silently replace CA,
  identities, keys, journals or controller ledger. Backups require off-host storage.

## Agent profiles: current implementation and discussion scope

Current Go agent supports allow-listed systemd, Docker and Podman adapters, not a
generic script adapter. Rootless Podman is verified on the user's Debian homelab
including status/restart and migration to the preview.3 package. This is not
evidence for every Docker/rootful Podman deployment or every packaged boot path.

The standalone binary does not inherently require a container runtime; however,
the current hlcp-agent Debian package explicitly depends on Podman/uidmap/
dbus-user-session and installs a Podman user unit. Supporting a public generic
agent requires revisiting package dependencies/default service profiles as well
as adding adapters. Docker deployment templates already exist.

User request: support Docker, Podman, and nodes with neither runtime, including
agent-local Bash scripts invoked as named actions through the controller.
The script execution/configuration contract is still being discussed; do not
implement it until the complete change plan is agreed.

Proposed distinction:

- Execution profile determines service account, user/system systemd lifecycle,
  runtime access and filesystem permissions; it is not an additional HLCP role.
- Resource adapter determines whether an action targets a container, service or
  an explicitly configured local script. One agent may have multiple adapters
  when the chosen account has the required access.
- A generic core package should not force Docker or Podman installation. Runtime
  helpers/profiles may offer optional dependency installation with consent.
- Proposed script actions map resource/action IDs to administrator-configured
  local executable/argv. Controller sends IDs, not arbitrary shell strings,
  script uploads, paths or interpolated command text. Bash can be an explicit
  interpreter for a local trusted script; no implicit bash -c remote execution.
- Scripts run with the agent's existing account privileges. No automatic sudo
  or assumption that local scripts are harmless/read-only.

Open script-design questions for the implementation plan:

1. Named custom actions vs existing status/start/stop/restart validation.
2. Fixed argv first vs typed bounded parameters; never shell interpolation.
3. Working directory/environment, file ownership and dependency validation.
4. Deadlines, bounded stdout/stderr, process-tree termination and secret handling.
5. Exit/result mapping and whether a status script is an observation provider.
6. Crash/reconnect outcomes and existing durable command-ID deduplication;
   scripts cannot promise exactly-once side effects or automatic safe replay.
7. Discoverability and explicit resource opt-in; runtime discovery grants nothing.
8. Packaging/service-account profiles, including mixed runtime/script nodes.

## Next design deliverable

The [concrete phased implementation plan](16-plug-and-play-implementation-plan.md)
now specifies scope, dependencies, acceptance gates and commit slices. These
notes remain the discussion record; implementation status belongs to that plan.

Finish agent-profile/script discussion, then write a unified phased implementation
plan for verified download/update, non-destructive setup, PKI/pairing/renewal,
runtime-independent packaging and resource configuration. Keep coherent testable
commit slices, and distinguish current functionality from target commands.
