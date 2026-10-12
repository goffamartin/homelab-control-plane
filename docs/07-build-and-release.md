# Build, CI and dependency review

## Pinned toolchain and local checks

`.go-version` selects Go 1.27.2 and `go.mod` declares Go 1.27.0 with the 1.27.2
toolchain. Do not build deployment binaries with the historical 1.25.0 container.
Set `GOTOOLCHAIN=go1.27.2` when using an older bootstrap Go, or install the pinned
version. The release builder also checks its exact running Go version.

```text
go mod download
go mod verify
go vet ./...
go test -count=1 -timeout=120s ./...
go test -race -count=1 -timeout=120s ./...
go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
govulncheck ./...
```

Race tests require a supported CGO toolchain. The local reference validation uses
Go 1.27.2 on Linux; native Windows development tests/vet are also checked. These
checks include the actual CLI/mTLS recovery test and the stress/fuzz seed corpus.
CI additionally executes the four bounded mutation campaigns from the recovery
evidence document. Scanning consults the current Go vulnerability database and
can fail later without a code change when new findings are published.

## Release builder

Run at repository root with a clean committed source tree and cached modules:

```text
go run ./tools/release -version v0.1.0 -out dist/v0.1.0
```

This is a version-label example, not an announcement or publication of v0.1.0.
Default targets are `linux/amd64`, `linux/arm64`, `windows/amd64`; a subset can be
selected via `-targets linux/amd64`. For each target, the tool builds `hlcp`,
`hlcp-agent`, and `hlcp-controller`, with target-qualified names. Runtime binaries
do not require CGO; Linux/Debian remains the deployment reference. Windows output
is for development, not a new promise of Windows service deployment or power-loss
journal guarantees.

The builder uses the pinned compiler, `CGO_ENABLED=0`, `-trimpath`, read-only
modules, no automatic VCS metadata, no linker build ID, and explicit version/full
Git revision linker values. It clears inherited build flags/experiments and fixes
CPU baselines. Child builds run offline and ignore external Go workspaces. There
are no wall-clock timestamps or absolute host paths in release metadata.

Outputs:

- nine binaries by default;
- `build-manifest.json` with version, full Git revision, Go version and artifact
  SHA-256 hashes;
- `SHA256SUMS`, covering the binaries and manifest, in deterministic order.

The output directory must be new: existing files are never overwritten. A failed
build can leave a partial new directory; it is not a verified release unless the
checksum file was successfully written and verified. The builder rechecks source
revision/cleanliness after compilation. Do not edit sources concurrently.
`-allow-dirty` is only for local validation, and labels the revision `-dirty`;
those artifacts must not be represented as builds of a clean commit.

Reproduce a clean build and verify on Linux:

```text
go run ./tools/release -version v0.1.0 -out dist/first
go run ./tools/release -version v0.1.0 -out dist/second
diff -u dist/first/SHA256SUMS dist/second/SHA256SUMS
(cd dist/first && sha256sum -c SHA256SUMS)
```

Reproducibility means the same source tree, compiler, dependencies, version label,
targets and specified build settings yield identical checksums. It does not claim
identical outputs across different compiler versions or platforms used to host
the compiler. Checksums detect corruption; they are not cryptographic signatures
or proof that an untrusted download is authentic.

Every runtime binary supports `--version` before loading config, keys or tokens.
`hlcp --version --json` returns structured metadata. Deployment tooling can check
the version without creating an API connection or starting any service.

## GitHub CI

`.github/workflows/ci.yml` runs on main code pushes, pull requests and manual
dispatch. Markdown-only changes are skipped. One Ubuntu job has a 20-minute
timeout and cancels superseded runs. It verifies modules/tidy/formatting, vet,
full race tests, bounded fuzz campaigns and known reachable vulnerabilities,
then builds twice and compares/verifies checksums. It smoke-checks `--version`
for all Linux amd64 binaries and uploads one artifact set for seven days.

Actions are pinned to verified commit SHAs rather than floating tags; repository
permissions are read-only and checkout credentials are not persisted. The job
does not publish a release, push commits, install services, or invoke a live
Docker/Podman adapter. Hosted artifact download can lose Unix execute bits; set
the intended binary's mode to 0755 when installing it on Linux. Artifacts and
local `dist/` outputs contain no configuration, tokens, private keys or PKI.

The format/module/build/release steps also have local checks; hosted CI execution
is recorded separately in the completion plan. CI is not a substitute for the
still-open live Podman acceptance or Debian deployment/reboot/upgrade in F10.

## Dependency review (2026-10-12)

- Go 1.27.2 replaces the old test-container toolchain. Its upstream release notes
  include TLS/HTTP and other security fixes. [Go release history](https://go.dev/doc/devel/release).
- CBOR is updated from 2.9.2 to 2.9.6. The upstream release recommends upgrading
  and reports decoder fixes; API is unchanged. HLCP's existing strict CBOR limits,
  semantic tests and fuzz corpus are retained. [CBOR 2.9.6 release](https://github.com/fxamacker/cbor/releases/tag/v2.9.6).
- YAML remains exactly pinned at `go.yaml.in/yaml/v4 v4.0.0-rc.6`, the available
  v4 release at review time. Upstream recommends v4 for new projects while v3 is
  security-fixes-only. This retains an explicit prerelease risk, not a claim of
  stable v4: configuration is administrator-owned startup input, strict fields
  and single-document checks are tested, and module/version changes require the
  full regression gate. Reassess the pin when v4 becomes stable or a relevant
  advisory appears. [Maintained YAML upstream](https://github.com/yaml/go-yaml).
- `github.com/x448/float16 v0.8.4` remains the CBOR indirect dependency. No new
  runtime dependencies are introduced for builds or version reporting.
- The pinned `govulncheck v1.9.0` source scans on Windows and hosted Linux CI passed
  for this tree with Go 1.27.2. This means
  no known reachable finding in that database snapshot, not a complete security
  audit. [Go vulnerability management](https://go.dev/doc/security/vuln/).

Local evidence: full Windows test/vet and Linux race suite passed with the new
toolchain and CBOR. Two full cross-platform validation builds produced identical
checksums for all nine binaries and the manifest. Version metadata was checked
in all three Windows executables. Dirty-tree and output-overwrite rejection and
workflow actionlint checks passed. The local validation builds carry `-dirty`;
the hosted clean-commit CI run is the release provenance gate.

Hosted evidence: [run 38231609545](https://github.com/goffamartin/homelab-control-plane/actions/runs/38231609545)
passed on full revision `bf79a036ba814e094ad1166495cb69cf13ac6c2a`, including
format/module/vet/race/fuzz/vulnerability gates, two matching clean builds,
checksum verification, all three Linux amd64 `--version` smoke checks and
artifact upload. The artifact is named
`hlcp-bf79a036ba814e094ad1166495cb69cf13ac6c2a` and has seven-day retention.
Two local clean builds of the same revision also matched, with all checksums
verified and the Windows CLI reporting that exact revision without `-dirty`.
