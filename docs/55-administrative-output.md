# Administrative output and YAML workflow

The human-readable CLI is for operators. Use `--json` on `configure`,
`check-config`, `resources list|discover`, `join`, `certificates`, `prepare-user`,
controller `setup`, `pki renew-issuer|finish-migration`, or the download
installer for machine consumers. Never parse English messages or terminal tables.
The existing controller API CLI `hlcp --json` retains its API payload shapes;
it is not the administrative result envelope described below.

New administrative results use `schema_version: 1`, `operation`, `status`,
`code`, `phase`, `changed`, `uncertain`, `completed` (an array), optional `next`
and command-specific `data`. Human messages are not a stable machine contract.
Status `ok`/`preview` exits zero; `failed`/`partial`/`uncertain` exits nonzero.
`changed` describes known effects of this invocation, not whether earlier state
exists. `uncertain` means the last effect cannot be confirmed; it does not mean
that a rollback occurred. Do not automatically retry mutating actions or re-pair.

`configure --json` reports the private YAML location without opening an editor.
The YAML body, script arguments/environment, pairing codes, private keys and
tokens are not included. `check-config --json` reports syntax/schema validity,
not TLS freshness, service availability or successful resource execution.
Resources must be explicitly added to YAML; discovery never imports them.

The installer emits exactly one result on stdout in JSON mode; download/APT and
other progress go to stderr. It rejects `--json --configure` before installation,
because the setup wizard is interactive. Install, then invoke setup/join
separately with explicit noninteractive inputs. Installer `changed` excludes
private temporary download staging but includes stopped services, saved recovery
backups and installed packages. `phase` and `completed` identify these effects.
Installer errors use `INSTALL_INCOMPLETE`; human detail remains on stderr.

Activation keeps its existing `preview`, `result`, `read_only` report fields,
with `schema_version: 1`, safe `check` and `next` diagnostics added. A failed
preflight causes no service activation. Failed apply can contain verified
completed actions and an uncertain last action. A join that already persisted
identity/YAML but failed activation is partial, not a refused pairing; its
`data.activation` carries the underlying activation report. Resume a genuinely
interrupted join, but activate an already completed one without new pairing.

Compatibility: older imported setup, doctor and
controller API commands retain their existing shapes unless documented otherwise.
Consumers must select a command and schema explicitly, tolerate unknown fields,
and reject unsupported schema versions. No network-accessible web API or remote
YAML editor is introduced by this output option.

## Local validation (2026-10-15)

Windows Go tests/vet and Linux Go race tests/vet passed. Disposable Debian 13
process tests passed for fresh managed onboarding, YAML resource configuration,
noninteractive configure inspection, editor no-op, controller-dispatched script
execution and revocation. Imported-to-managed migration retained resources,
journal, ledger and same-command-ID results. Controller group-trust/resume
preflight checks also passed. Local dirty test packages were not published.
The current installer verified existing signed preview.7 artifacts in read-only
JSON mode, with one stdout result and progress on stderr; invalid versions and
interactive JSON setup were rejected before installation.

These are process/container checks, not booted systemd acceptance. The actual
cause of the reported VM activation refusal remains to be collected using the
new safe `check` diagnostic. No VM credentials/configuration were changed here.
