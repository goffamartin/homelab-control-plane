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
- Pending: setup/storage integration, durable registry and revocation, pairing
  and pinned enrollment, resumable join/resources wizard, managed renewal/TLS
  reload, signed artifact verification/update, migration/acceptance quick start.
- No managed release or VM/live acceptance yet. Preview.4 remains unchanged.

## Implementation acceptance

PKI core tests cover chain/EKU/SAN/path length, key matching, encrypted backup
and wrong password, issuer expiry and leaf validity clamp, invalid identity/CSR
and CA escalation refusal. These are local crypto tests, not service lifecycle
or end-to-end managed onboarding evidence.
