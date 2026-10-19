> Historical design/evidence snapshot. Not an installation guide or a current release-readiness claim. Start at [current documentation](../README.md); current validation scope is in [validation](../validation.md).

# Managed plug-and-play — local acceptance evidence

Date: 2026-10-15. Product implementation commit:
`974d0eefca5141c4562c9460aeaf439a3f9c7065`.
Later evidence/documentation-only commits do not change this tested product code.

Local candidate: **v0.0.0-test.ready.2**, Go 1.27.2, clean exact SHA above.
Two independent output directories: `.cache/managed-live-candidate-a` and `-b`.
All **19 files are byte-identical**, including both detached Ed25519 signatures:
nine native binaries (Linux amd64/arm64, Windows amd64), four .deb packages,
deployment archive, standalone installer and authenticated metadata/signatures.
ARM64 and Windows outputs are build/package evidence, not Debian runtime evidence.

| Public metadata | SHA256 |
|---|---|
| build-manifest.json | abb6c7ef2fc63668d4e7e350d0af42bf89d9dfca51d7a8e910f7953ce971164e |
| SHA256SUMS | 58ca9bbf00542160e401cf5df919f7d1b1fdf11a997060be6ecccd8ccacd044d |

Manifest compatibility: protocol 1; managed state 1; command journal 3; operation
ledger 1. Signing uses the pinned public key documented in the Quick Start;
the real private signing key is ignored/restricted and absent from artifacts.
This is a **local fresh-test candidate, not a published release or an approved
downgrade over the installed 0.1.0 previews**.

## Verified locally

| Check | Result / scope |
|---|---|
| Complete Windows `go test -count=1 -timeout=120s ./...` and `go vet ./...` | PASS, Go 1.27.2 |
| Complete Linux `go test -race -count=1 -timeout=120s ./...` and `go vet ./...` | PASS, cached Go 1.27.2 container, no external network |
| Exact-SHA double build and all-file/signature comparison | PASS, final product commit above |
| Installed managed setup / one-use pairing / user join | PASS, native binaries from final .deb packages in disposable Debian 13 |
| Normal CLI without sudo/token flags | PASS, actual root-to-user private profile handoff and non-root CLI request |
| Script allowlist / actual action / live revoke | PASS, uname action through real controller-agent mTLS; revoked session offline; journal unchanged |
| Enrollment/renewal security and recovery | PASS, crypto/store/network tests including pin/hostname/MITM refusal, retry, hot-load, issuer maintenance and revocation |
| Imported pathLen:0 CA migration | PASS, old agent during overlap, guarded re-pair/cutover/refresh; original resource retained |
| Same-ID history preservation through migration | PASS, byte-identical journal/ledger, original result retained, no resubmission |
| Actual running agent version after upgrade | PASS, installed binary version advertised despite old provisioning label |
| Signed installer preview / real APT installation / offline update | PASS, final signed candidate; private backup and unchanged state/config |
| Installer refusal paths | PASS, unsigned/modified/missing/wrong-version assets, invalid manifest signature and actual installed-higher-version downgrade refusal; schema/architecture/traversal/duplicates covered by verifier tests |
| Controller package lifecycle | PASS, upgrade from exact b59b9c9 P05y artifact to final candidate; controlled raw-package downgrade/remove/purge; private state retained |
| Generic agent package lifecycle | PASS, no container runtime dependency; mask refusal and install/reinstall/remove/purge preserve private state |
| Historical Debian conffiles | PASS on preceding d5dbde3 candidate; packaging behavior unchanged by the final activation-only fix |
| Documentation links and deployment allowlist | PASS; no private key/token or local config is an artifact source |

Shell process fixtures are explicitly guarded for a fresh disposable non-booted
Docker container. They are NOT VM setup scripts. They do not execute on actual
HomeLab nodes, mount a production container socket or expose host ports.

## Remaining human/live gate

Booted systemd start/enable/reboot without login, actual rootless Podman and
Docker permissions/actions, live network/update/expiry/recovery scenarios and
measured resource use remain **NOT RUN**. Actual private GitHub release download
also needs a published signed candidate and the user's own login. No release/tag,
repository visibility change, VM cleanup, SSH command or production PKI rotation
was performed by these tests. Web UI/MCP development is not part of this
plug-and-play delivery scope.

See [Quick Start](49-managed-quick-start.md), [migration/recovery](50-managed-migration-and-recovery.md)
and [whole-system live acceptance](51-managed-live-acceptance.md). Implementation
has reached the requested handoff boundary: publish a user-authorized private
test candidate and perform the combined live gate; fixes then use the explicit
signed update flow rather than rebuilding the installation manually.
