> Historical design/evidence snapshot. Not an installation guide or a current release-readiness claim. Start at [current documentation](../README.md); current validation scope is in [validation](../validation.md).

# Managed plug-and-play delivery tracker

User-authorized continuous implementation: continue in coherent tested commits
until only whole-system live verification remains. No VM mutation, deletion,
release publication, repository visibility change or production PKI rotation.
Existing imported paths remain compatible. Target contract remains P06–P12 in
16-plug-and-play-implementation-plan.md. A source component is not a completed
end-user path until runtime, CLI, package and recovery tests are integrated.

## Progress

- P06 PKI core implemented: P-256 root (10 years, pathLen 1), issuing CA (1 year,
  pathLen 0), server/client leaves (up to 90 days, capped by issuer), client CSR
  identity and extension restrictions, issuer/key load validation. Private root
  is not retained by the runtime authority object.
- Root backup: AES-256-GCM authenticated archive with fresh 32-byte salt,
  PBKDF2-HMAC-SHA256 / 600000 iterations and fresh nonce; minimum 16-character
  password. Verification confirms decryptability and root cert/key match.
- Durable bounded private store and node registry implemented, with exclusive
  OS lock, owner/mode/symlink checks, fsync/atomic snapshot publication and
  fail-closed uncertain durability. Invitations store secret digests only;
  enrollment atomically records credential/admission/result for same-request
  recovery. Revocation and explicit replacement invalidate old credentials.
- Pinned HTTPS enrollment core implemented: public-only untrusted bootstrap,
  root fingerprint check, then separate standard CA/hostname-verified TLS1.3
  connection before any invitation secret is transmitted. No redirects/proxy
  inference, bounded body/concurrency/global rate, isolated public routes.
- Managed controller runtime, isolated enrollment listener supervision and
  operator-only pairing/revocation API are integrated. Registry admission is
  checked during handshake, before session publication and command delivery;
  revocation disconnects active sessions. Expired peer sessions are closed.
- Agent and server identity renewal/hot-load are integrated into runtime.
  Renewal persists a retry ID before network I/O, retains old valid snapshots
  on failure and keeps bounded overlapping serials. Pairing generations reject
  old certificates even when a key is reused. Agent expiry requires re-pair;
  server leaves can renew locally under the valid issuer. Diagnostics expose
  expiry/renewal/issuer metadata, never keys. HTTP renewal requires mTLS.
- Managed setup is now the default controller wizard; imported setup remains
  explicit. It creates a resumable root-owned seed and encrypted root backup,
  provisions service-owned runtime state, and optionally grants the actual CLI
  user access and starts/enables only verified packaged units.
- Operator CLI pairing create/list/revoke and certificate diagnostics are wired.
  Agent join persists key/CSR/request before enrollment, resumes interrupted
  requests, and supports explicit re-pair with private bounded config/identity
  backups while retaining resources and journal. Codes are stdin/TTY/private-file
  only, never command-line arguments. Resource wizard supports script, Docker,
  Podman and systemd allowlists; discovery does not enable actions.
- Certificate lifecycle diagnostics and deduplicated journal metadata are wired.
  Explicit offline issuer renewal decrypts the root backup only for maintenance,
  retaining root, issuer key, registry and command history. This is not recovery
  from a compromised key. Root expiry still requires planned replacement.
- Debian 13 disposable package/process acceptance passed: real installed setup
  without OpenSSL/YAML/scp, repeat setup preserving CA/registry, pairing, user
  join, completed resume, duplicate refusal, script allowlist and actual uname
  action, diagnostics during runtime, active revocation and unchanged journal.
  The container has no booted systemd: service start/enable/reboot acceptance
  remains NOT RUN. Local artifacts are development-only, not a release.
- Signed release manifest/checksum support and a pinned-key installation helper
  are implemented. Exact-version Debian amd64/arm64 artifacts are authenticated
  before execution/APT; unsigned, modified, missing, wrong-architecture,
  incompatible-state and dirty-source manifests are rejected. The local signing
  key is outside Git under ignored .tools/release-signing with restricted ACL;
  only its public key is distributed. No new release has been published.
- Explicit update helper preserves conffiles, refuses downgrade, requires an
  inactive service or explicit restart consent, and takes a private bounded
  no-follow state/config snapshot before APT. It never restores command history.
  Download staging is removed after success and retained privately after failure.
- Installed signed-helper acceptance passed in disposable Debian: exact signed
  clean-SHA artifacts, verified preview and real APT installation, explicit
  non-booted offline update, private recovery archive and byte-preserved config
  and managed state. Missing/unsigned/modified/wrong-version assets and invalid
  manifest signatures failed before installation. Live service update remains
  a separate booted VM gate, not inferred from offline success.
- Imported-to-managed migration is integrated: explicit valid legacy CA/server
  overlap, exact old allowlist IDs imported as legacy nodes, immediate old
  generation revoke on replacement, preserved agent resources/config/journal,
  guarded migration finish and authenticated agent trust cleanup. Runtime refuses
  accidental imported fallback when canonical managed state exists. Empty or
  corrupted registry is never substituted for lost admission history.
- Actual installed Debian migration process acceptance passed: original pathLen:0
  root and agent still worked during overlap; explicit re-pair retained resources;
  early finish was refused; after cutover/refresh the agent reconnected; the
  same-ID result and byte-identical journal/ledger were preserved. This was a
  disposable non-booted container, NOT a deployment or VM lifecycle verdict.
- Quick Start, backup/migration/recovery boundaries and the whole-system live
  acceptance matrix are in docs/49–51. No extra web UI/MCP scope was inferred.
- Final full Windows test/vet and Linux race/test/vet passed. Clean signed final
  candidate v0.0.0-test.ready.2 from product commit 974d0ee was built twice for
  Linux amd64/arm64 and Windows amd64; all 19 files/signatures matched. Installed
  onboarding/migration/signed-install/update and package-preservation gates passed.
  [Exact local evidence](52-managed-local-acceptance-evidence.md).
- Remaining handoff boundary: user-authorized candidate publication and combined
  whole-system booted VM acceptance, not another implementation prerequisite.
- No managed release or VM/live acceptance yet. Preview.4 remains unchanged.

## Implementation acceptance

PKI core tests cover chain/EKU/SAN/path length, key matching, encrypted backup
and wrong password, issuer expiry and leaf validity clamp, invalid identity/CSR
and CA escalation refusal. These are local crypto tests, not service lifecycle
or end-to-end managed onboarding evidence.
