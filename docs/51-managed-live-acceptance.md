# Managed plug-and-play — whole-system live gate

Live acceptance has started. The signed preview.5 download/verification passed;
the first interactive controller setup attempt failed (see the
[timeout regression](53-interactive-timeout-regression.md)). Other rows
remain NOT RUN until actually observed. Local tests do not complete this gate.
Local package/process tests and Windows/Linux race tests are not booted Debian
systemd, rootless Podman, Docker runtime or reboot evidence. Preview.5 was published
privately from `756aa9f0b8ce80e6cec46f83df2c99f6d8fbecb0`; the user, not the agent,
performs the VM installation steps. Implementation tests use disposable containers.

Use two isolated disposable Debian 13 VM clones/snapshots with separate test
identities. Reusing the physical resources of the earlier two VMs is possible
only after preserving their real config/CA/history off-host and explicitly
agreeing to a test maintenance/clean-install boundary. Do not run disposable
container fixture scripts directly on VMs: they intentionally refuse that use.
For this run the user explicitly chose a fresh HLCP identity/history boundary,
created snapshots of both existing VMs and declined an additional file archive
before removing the old HLCP state. Podman/container state was left in place.
Snapshots are not independent off-host backups. Do not restore one side's old
operational history into an active new deployment or use it as binary rollback.

## Exact candidate and verification

Build from one clean full SHA with pinned Go 1.27.2 and a private signing key
matching `deploy/install/release.pub`. Generate two NEW output directories using
the same numeric test version, targets and key. Compare all SHA256SUMS, manifest
signatures and archive bytes. A candidate version below the installed preview
must not be forced as a production downgrade. The manifest records exact SHA,
protocol 1/managed state 1/journal 3/ledger 1; `-dirty` candidates are rejected by
the signed installer. Matching labels alone do not prove an older reader handles
all fields; deliberate rollback needs actual reader/fixture validation as well.

The `.tools/release-signing/release.key` on the development machine is ignored
by Git, restricted to the current Windows account and never included in artifacts.
Back it up securely before relying on signed distribution. Never upload it as a
release asset. CI currently produces unsigned build artifacts, not a signed
release. Creating/publishing a release or configuring a CI signing secret requires
separate explicit user authorization; no workflow publishes automatically.

## Live matrix

| Scenario | Pass evidence | Status |
|---|---|---|
| Verified private download | Actual non-root gh login, exact signed version; no token/secret in logs | PASS preview.5: helper checksum and signed controller preview verified on controller VM |
| Fresh managed controller | Wizard creates PKI/backup/profile; service active; CLI without sudo; no remote API 9444 | FAIL preview.5 timeout, fixed in preview.6; preview.6 reached CA backup then CLI refusal due to group-writable .config. Removing group-write allowed preview; full activation still unverified. Preview.7 early checks/trust/resume need live retry. |
| Script-only system agent | No container runtime installed; join, explicit script resource, allowed action and sandbox startup | NOT RUN |
| Generic user agent | Actual owner join/start/enable; no implicit sudo/account impersonation | NOT RUN |
| Rootless Podman | Actual container owner/subuid/subgid/linger; selected container status/restart only | NOT RUN |
| Docker profile | Actual socket permissions documented; no implicit group/root grants; opt-in action | NOT RUN |
| Reboot without login | Controller + user/system agent automatically register with same identities/history | NOT RUN |
| Network interruption | Agent reconnects; unknown operations remain lookup-only; same-ID no replay | NOT RUN |
| Active revoke/re-pair | Active session disconnected; old credentials cannot reconnect/renew; explicit generation replacement | NOT RUN |
| Renewal/expiry | Short-lived isolated fixture renews/hot-loads; retained valid identity on failure; expired agent re-pairs | NOT RUN |
| Imported migration | Legacy/new overlap, resource IDs retained, cutover/refresh and unchanged journals/ledger | NOT RUN |
| Signed live update | Stopped snapshot, exact APT version, chosen unit restart, registration/inventory verified | NOT RUN |
| Disaster restore / compatible rollback | Consistent private restore on fresh clone; current history never rolled back for binary downgrade | NOT RUN |
| Load / diagnostics | Bounded output/flood does not starve heartbeats; memory/disk/startup measured | NOT RUN |

Run the Quick Start literally on the fresh clones: no OpenSSL, YAML edits or
private-key scp steps. Record human commands/prompts, elapsed setup time and
resource use. Rootless Podman and Docker may be separate resettable agent-clone
passes; do not imply both were exercised because adapters compile. ARM64 artifacts
are compile/package checks until separate actual ARM64 execution is performed.

For each row retain exact SHA/version/artifact digests, UTC time, boot IDs,
selected unit/FragmentPath and safe outcome metadata. No passwords, invitations,
tokens, complete secret-bearing configs, private backups or private keys in Git.
Collector outputs alone are not a PASS verdict; registration/action and reboot
must be observed. Mark a failure with the actual phase, preserve state and fix it
in a normal coherent commit; then install a new exact signed candidate and rerun
the failed row plus relevant regression tests.

## Local evidence already established

- Complete Windows test/vet and Linux race/test/vet suites (final rerun recorded
  in docs/48 and the implementation handoff).
- Native Debian package/process setup, automatic PKI, pairing, user join, script
  action, diagnostics during runtime, duplicate refusal and active revoke.
- Imported pathLen:0 CA -> durable legacy admission -> managed join -> trust
  cutover -> explicit trust refresh, preserving resources and byte-identical
  journal/ledger across the same-ID result lookup.
- Signed metadata/helper preview/real APT install/offline update/private backup;
  unsigned/modified/missing/wrong-version/incompatible manifests refused.
- TLS renewal/hot-load, MITM/hostname/pin checks, one-use enrollment retry,
  concurrent admission/revocation and bounded store/registry unit/integration tests.

A local Go clock fixture is not evidence of changing production VM clocks. Never
disable NTP or alter the real HomeLab certificate periods merely to exercise
renewal. Short-lived test fixtures must be separate and clearly identified.
