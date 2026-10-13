# Confirmed local setup transactions — P04b

P04b adds internal Apply, Inspect, Recover and DiscardBackup APIs to the validated
planner. They are **not** command-line setup/doctor commands yet. P04c supplies
diagnostics/entrypoints; P05 supplies imported-certificate wizards. No service,
account, firewall, certificate issuance or homelab VM operation happens here.

## Scope and consent

Transactions cover flat files in one already-existing private directory selected
with an explicit registered UID/primary GID. The P04a path/ownership/permission,
semantic validation and bounded-input rules remain mandatory. This is not global
atomicity across directories or a filesystem snapshot. Readers may see a mixed
set during a batch; wizard service activation/reload must happen only after the
file transaction completes. Do not treat file setup as service orchestration.

Apply, Recover and DiscardBackup each require separate explicit consent. Merely
building/rechecking a plan, inspecting state, or selecting AllowReplace does not
authorize writes. Unconfirmed calls return without filesystem changes. A stale
plan, rebinding the root pathname or a conflicting directory lock stops Apply.
The directory flock coordinates HLCP setup processes, not arbitrary editors.

## Private layout, durability and bounded retention

- `.hlcp-setup-transaction/`: at most one pending transaction, directory 0700.
- `.hlcp-setup-backup/`: at most one completed backup, directory 0700.
- `manifest.json` and temporary `manifest.next`: mode 0600, capped at 64 KiB.
- `stage-00` ... `stage-31`: newly staged files before commit, original files
  after replacement. Initial writes are exclusive and apply the explicit owner.
- Candidate and existing-content limits remain 32 files, 1 MiB/file, 8 MiB each.
  Staging plus one retained backup is bounded; no timestamped unlimited history.

Manifest metadata contains names, phases, original/new inode/device identities,
owner/mode/size and private SHA256 digests. Contents are **not** embedded in JSON,
but hashes and original files are still sensitive: keep the whole directory
private and do not edit engine-owned transaction/backup files. Public status
contains only controlled state and changed-file count, not contents or hashes.

Original replacement inodes are retained instead of recreated from a template.
Create uses renameat2 NOREPLACE; replacement uses EXCHANGE to preserve the
displaced entry. There is no overwriting-rename fallback. The Linux native backend
supports amd64/arm64; the kernel and filesystem must support these operations.
The syscall semantics are described by the primary
[Linux rename(2) manual](https://man7.org/linux/man-pages/man2/rename.2.html).
Other native mutation architectures fail closed; injected effects remain testable
on Windows. Linux directory/file fsync failures are errors, not silently ignored.

New files are synced before publishing. Existing files are synced before exchange;
prepared intent is durably recorded before changing targets. Directory changes are
synced on both sides of moves/exchanges. The committed marker is written through
an exclusive next file and atomic exchange before the transaction directory is
published as the retained backup. Candidate bytes/modes and displaced originals
are checked before marking the batch committed. Setup never starts an executable.

Successful unchanged plans are no-ops, even with a retained backup. Another
mutating plan stops while that backup exists. DiscardBackup is a separate confirmed
cleanup that never deletes user targets. A future wizard can present that retention
choice in its confirmation flow; this is not a new authorization profile or an
implicit permission grant. Backups are not silently evicted on a failed attempt.

## Recovery and conflict policy

The authoritative main manifest, not an in-memory phase or a prospective next
manifest, determines recovery:

- `staging`: remove only validated/proven staging entries; no target has been
  published. Unrecognized stage inodes are preserved, not guessed as our files.
- `prepared`: infer which individual rename/exchange landed using exact snapshots,
  reverse those steps and preserve original content/mode/owner/inode.
- `rolled_back`: finish interrupted staging cleanup without rewriting targets.
- `committed`: finish backup publication only; never reapply target configuration.
- `discarding`: finish the explicitly requested private backup cleanup only.

Root binding is revalidated for mutation. A canceled operation before the durable
commit marker triggers rollback without reusing the canceled context. After that
marker, recovery finishes the committed transaction and reports it as applied;
late cancellation does not silently undo it. No credentials are regenerated and
no scripts/services are replayed during recovery.

Strict bounded/canonical JSON rejects unknown fields, duplicate keys, trailing
documents, invalid phases, path traversal and foreign-root/owner metadata.
Unexpected files, changed target bytes/inodes, missing originals or unsafe
entries block mutation/cleanup. Recovery stops on a conflict rather than erasing
manual edits; originals and staged data remain private for operator inspection.
A batch can therefore remain partially visible when safe rollback is impossible.
No force bypass or recursive deletion of an arbitrary directory is provided.

An actually empty owned transaction directory can be removed after confirmed
recovery. A missing/corrupt main manifest with nonempty contents, an unrecorded
staging inode after a crash in its proof-recording window, persistent disk failure
or ambiguous namespace state requires operator inspection. It is never treated
as an empty successful transaction. Fsync ordering and hard-process-crash tests
do not prove power-loss correctness on every storage device/filesystem.

## Verification

Local gates passed: full Windows tests/vet and full Linux race/vet suites;
targeted transaction/recovery tests also passed three repeated race runs.
The bounded metadata fuzz campaign passed 233,871 executions. Linux arm64
tests compiled with CGO disabled, but were not executed on ARM. Final native
tests additionally cover the racing-editor window and empty-directory cleanup
after removal of its durable manifest. No runtime dependency was added.

Mocked filesystem tests cover partial writes/disk failures, exclusive-create
collisions, mutation admission, controlled secret-free errors, corruption and
unrecognized files. Metadata fuzzing checks strict parsing without side effects.

Native Linux tests cover successful create/replace/no-op, private backup retention,
separate discard consent, directory binding and flock, cancellation, fault
boundaries, preservation of manual edits, root-to-explicit-user ownership and
hard subprocess exits during staging, immediately after exchange, after one file,
after the committed marker, during rollback cleanup and during backup discard.
Repeated recovery is read-only/no-op once the selected terminal state is complete.

Remaining work: P04c doctor/CLI, P05 wizard integration and operator-run VM
acceptance. Preview.4 remains unchanged; no new preview is issued for P04b alone.
