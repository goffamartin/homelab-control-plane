# Development and releases

HLCP uses Go and CBOR. The pinned compiler is recorded in `.go-version` and
`go.mod` (Go 1.27.2 for this release). YAML and CBOR dependencies are pinned in
`go.mod`/`go.sum`; YAML v4 is currently a release-candidate dependency, not a
claim of a stable upstream v4. Review dependency changes with the full suite.

## Local checks

From the repository root with the pinned toolchain:

```sh
go mod download
go mod verify
go vet ./...
go test -count=1 -timeout=120s ./...
go test -race -count=1 -timeout=120s ./...
```

Race checks require a supported CGO toolchain; Linux is the reference runner.
CI additionally checks formatting/tidy, bounded fuzz campaigns and reachable
vulnerabilities, then builds twice and verifies checksums. Vulnerability results
describe the consulted database snapshot, not an independent security audit.
See the exact workflow in `.github/workflows/ci.yml`.

## Architecture at a glance

- `cmd/hlcp`: local CLI; authenticated loopback HTTP API client.
- `cmd/hlcp-controller`: registration, inventory, admission, durable operation
  tracking, scoped local API and managed enrollment.
- `cmd/hlcp-agent`: outbound mTLS session, local allowlist/adapters, journal,
  observations and bounded log readers.
- `internal/protocol`: bounded framed CBOR protocol over TLS (`hlcp/1`).
- `deploy/`: packaged systemd profiles, examples and verified installer.
- `tools/release`: explicit artifact allowlists, deterministic binaries/packages
  and signed manifests. No directory-wide secret collection.

Durable commands and ephemeral log reads have separate lifecycle contracts.
Command ACK is not execution success. Journals/ledgers preserve bounded history
and explicit unknown outcomes; external effects are not transactionally rolled back.
[Design history](https://github.com/goffamartin/homelab-control-plane/blob/v1.0.0/docs/archive/01-hlcp-v1-design-draft.md) and [ADRs](https://github.com/goffamartin/homelab-control-plane/blob/v1.0.0/docs/adr/README.md)
provide background, not a substitute for implementation/tests.

## Reproduce release artifacts

Use one clean committed revision, cached modules, the pinned compiler and two
new output directories. The signing key must match `deploy/install/release.pub`;
never generate a replacement key as a build fallback or publish the private key.

```sh
go run ./tools/release -version v1.0.0 -out dist/first \
  -signing-key /private/release.key
go run ./tools/release -version v1.0.0 -out dist/second \
  -signing-key /private/release.key
diff -u dist/first/SHA256SUMS dist/second/SHA256SUMS
(cd dist/first && sha256sum -c SHA256SUMS)
```

Compare every artifact and both detached signatures as well. Builds fix compiler
settings, version/revision metadata and archive ordering; they run child builds
offline and recheck source cleanliness/revision afterward. Do not edit sources
concurrently. Reproducibility requires the same source, version, dependencies,
targets and compiler/build host assumptions; it is not compiler-independent.

Default targets are Linux amd64/arm64 and Windows amd64. The signed set contains
nine binaries, four Debian packages (controller/agent × two architectures),
deployment archive, installer, manifest/checksums and their two signatures:
19 files. Controller packages also include the local `hlcp` CLI. Documentation
and completion scripts are included; no production config, CA key or token is.

Without `-signing-key`, output is for development/CI and the installer refuses
it as an installable signed release. `-allow-dirty` is only for local diagnostics,
not publishable release provenance. Existing output directories are never reused.

## Publication checklist

1. Review documentation, compatibility and [validation limitations](validation.md).
2. Commit the coherent changes and pass CI for that exact revision.
3. Build twice, compare all artifacts and verify signatures/checksums.
4. Run `hlcp verify-release --help` and verify both roles/architectures using an
   absolute release directory. Inspect Debian metadata/scripts and test amd64
   installation in a disposable matching host/container.
5. Create a draft release targeting the exact source revision; upload only the
   complete verified set. Include upgrade steps and known limits in release notes.
6. Publish only after checks pass; do not silently move an existing version tag.
7. Download the published assets into a new directory and verify them again.

Release tooling builds artifacts only; it does not publish GitHub releases,
modify tags, change repository visibility or qualify live VM behavior automatically.
