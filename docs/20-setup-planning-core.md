# Read-only setup planning core — P04a

This is an internal implementation block, not an installation wizard. P04a adds
`internal/setup`; it does **not** add `setup`, `doctor`, `join`, service orchestration,
certificate generation, filesystem Apply/rollback or a new release. P04 remains
open until transactional application/recovery and diagnostics are implemented.

Historical boundary: P04b now adds [confirmed transactions/recovery](21-setup-transactions.md).
The no-write claim below applies to Build/Preview/Recheck and read-only input
operations, not the newly confirmed Apply/Recover/DiscardBackup APIs.

Local verification (2026-10-13): full Windows test/vet and Linux race/vet suites
passed, followed by targeted tests after the final value/pointer redaction check.
The final 10s bounded fuzz campaign passed 75,921 executions; earlier campaigns
also passed. Linux arm64 tests compiled with CGO disabled but were not executed
on ARM. No dependency changes, preview release or VM operations were required.

## Contract

A planner accepts developer-defined local file intents with pure semantic
validators. Both proposed and existing contents must validate. Invalid existing
configuration is never treated as absent, even if replacement is selected.
The plan classifies each file as `create`, `unchanged`, or explicitly selected
`replace`. Different content **or mode** requires replacement selection; merely
providing a template never authorizes an overwrite or chmod.

Preview reports only sorted destination basenames, actions and permission modes,
plus `read_only: true`. It exposes no values, digests of secrets, private source
paths, validator errors or raw filesystem errors. Plan JSON and normal/debug
formatting use the same public preview. Candidate and snapshot bytes are copied
before storage/validation; changing an input or returned preview does not change
the stored plan. This is not a guarantee of memory zeroization or an encryption
mechanism; callers must not log raw Intent/Source snapshot objects.

Limits: 1–32 files, 1 MiB per file, 8 MiB total candidate contents and separately
8 MiB total existing snapshots. Candidate modes are exactly 0600 or 0644. Names
are flat ASCII basenames (1–128 bytes, letters/digits/period/underscore/hyphen);
dot/dot-dot, path separators and reserved `.hlcp-` transaction names are rejected.
There is no recursive destination creation or guessed parent directory.

`Recheck` rereads expected snapshots and detects file creation/removal, content,
mode, ownership and source identity drift (Linux device/inode). Even replacement
by a new file with identical bytes is stale. It does not acquire a filesystem lock, create a journal
or make a write atomic. A future Apply must recheck **under its own lock** and
validate its directory binding, use no-clobber staging, durable backups/recovery,
bounded retention and explicit operator consent. P04a has no Apply method and
cannot perform writes.

The bounded private-input helper consumes file/stdin bytes without trimming or
logging and returns controlled errors rather than raw reader messages. It does
not implement hidden terminal input, input schema validation or a timeout for an
interactive reader. No current command line reads secrets through this helper;
TTY handling and interactive/noninteractive wizard integration are later work.

## Native Linux source

- Target directory must already exist, be mode 0700, and match an explicitly
  supplied UID/GID for a registered account's primary group. Root can inspect that
  explicit account; a non-root process can inspect only its own identity. Owner
  selection never relies on HOME, SUDO_USER or another unchecked environment value.
- Each ancestor is opened relative to the previous descriptor with DIRECTORY,
  NOFOLLOW and CLOEXEC. Owners must be root or the selected account; group/other
  writable parents are rejected except root-owned sticky ancestors such as `/tmp`.
- The final directory descriptor remains anchored even if the original pathname
  is replaced by a symlink. Mode/ownership are rechecked on that descriptor before
  every file read. This is safe read anchoring, not validation of a future write
  to a replaced directory pathname.
- File opens are descriptor-relative, NOFOLLOW and NONBLOCK. Only regular,
  single-link, correctly owned files without special mode bits or group/other
  write permissions are accepted. Symlinks (including dangling links), hardlinks,
  directories, FIFOs and oversized files fail closed. Read errors are not absence.
- No mkdir, chmod, chown, credential generation, exec, service call or explicit
  network probe occurs. Account lookup follows the host's OS/NSS policy, which
  may use a remote directory service. Ordinary reads may update kernel-managed atime;
  the no-write claim refers to application mutations, not frozen access metadata.
- Native sources on non-Linux platforms fail closed. The pure planner with an
  injected read-only Source remains testable on Windows; this is not a promise of
  Windows provisioning support or protection from a malicious same-account/root
  process with equivalent file access.

## Verification and remaining P04 work

Unit tests cover pure/mocked sources, explicit replacement, invalid existing
files, stale snapshots, limits, input copying, bounded private input, controlled
errors, concurrent reads/close and secret-free JSON/debug preview. Linux fixtures
cover anchored descriptor reads, replaced directory paths, unsafe parents/modes,
symlink/hardlink/FIFO/non-regular inputs, missing-directory no-creation and file
content/inode/mode/mtime preservation. Private fixture permissions are provisioned
by the tests, never repaired by the planner.

Next blocks:

1. Transactional application with confirmation, FD/no-clobber staging, durable
   backups, rollback and interruption/disk-full/crash recovery tests.
2. Read-only doctor and CLI entrypoints with OS/account/time/network/TLS expiry,
   units/overrides/runtime/subordinate IDs/user manager/linger diagnostics.
3. Imported-certificate wizards in P05, using the same validators and transaction
   engine; no shortcuts around invalid existing files or missing consent.

No homelab VM changes are part of this block. Preview.4 remains the published
runtime checkpoint A; its artifacts do not include P04a.
