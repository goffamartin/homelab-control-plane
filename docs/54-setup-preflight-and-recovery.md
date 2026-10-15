# Setup preflight, primary-group trust and completion

Intended release: preview.7. This changes onboarding, not the wire protocol,
controller roles or remote-action allowlists. No arbitrary systemd overrides are
automatically accepted and no existing HomeLab VM is changed by development tests.

## What happens before CA creation

The installed controller checks its service account and existing private config,
state and backup directories without repairing ownership/modes. When startup is
requested, it checks the exact installed systemd profile before generating keys.
The selected human CLI account is inspected by a subprocess actually running as
that account. Root never opens/writes/chowns user-controlled profile paths.
The user-side preview does not receive an actual bearer; only metadata/digest.
Files already present must be safe and compatible, not silently overwritten.

Diagnostics name the failing path and use bounded, allowlisted codes. A normal
missing `.config/hlcp` is planned, not treated as a permissions failure. Existing
CLI token/profile conflicts, links, foreign owners, unsafe private modes and
world-writable ancestors remain blockers for CLI provisioning.

## Deliberately trusting the selected user's primary group

Default setup remains strict. On `GROUP_WRITABLE_PARENT` the wizard explains the
exact user-owned ancestor and group ID, then asks for explicit trust. Acceptance
allows group-write only where BOTH the owner UID and owning primary GID match the
selected non-root account. It does not allow arbitrary supplemental groups,
root-owned writable ancestors, other-write, or loose modes on HLCP's own files.
Group membership is the administrator's trust boundary: trusted members may
manipulate ancestors, so this is an acknowledged tradeoff, not equivalent to a
host with mutually hostile users. Existing sticky root-owned temporary-directory
handling in generic setup is unchanged. No default/global policy is weakened.

Noninteractive setup and standalone grants use explicit `--trust-user-group`.
Trust is retained with the selected account in private saved setup preferences;
changing the selected account clears inherited trust unless explicitly selected
again. No chmod, chown or sudo-group grants are performed. New HLCP directories
are 0700; token/config files are 0600. The controller's CA/runtime private store
never uses this exception. Existing same-role CLI users still share a token;
this is not individual-user auditing/revocation.

## Resume rather than recreate

Private `managed-setup-request.json` retains the network identity, state/backup
paths, CLI selection/trust and start/boot choices. Invalid saved metadata stops
setup rather than falling back to a fresh CA. Explicit flags override saved
settings; an interactive retry shows the reused settings and still asks consent.
Mode/bootstrap checkpoints continue to protect the existing CA and registry.

Run `sudo hlcp-controller setup` to complete a partially finished installation.
Once managed configuration is committed, completion does not generate a CA or
ask for its backup password. Older preview.6 bootstrap metadata supplies network
defaults; unsaved CLI/start choices are still asked. Custom older paths must be
supplied explicitly. `--resume` remains the explicit file-transaction recovery
flag, not permission to reset identities or journals.

If CLI provisioning fails later (for example a path changes after preflight),
the controller configuration remains committed. The actual safe diagnostic is
shown rather than swallowed. The user can keep it and continue without CLI,
or stop and rerun setup after inspection. `--skip-client` performs that choice
noninteractively. It does not remove/revoke an existing profile/token. Never
delete the CA, registry or journal merely to finish a CLI profile.

## Validation boundary

`deploy/debian/test-managed-preflight.sh` is guarded for new disposable containers
only. It covers refusal before CA creation, explicit 0771-parent trust without
chmod, 0700/0600 user-owned output, saved completion with no password/input,
continued refusal of world-write, skip-client and recovery of preview.6 metadata.
It checks byte-identical CA/bootstrap/admission/CLI token/backup preservation.
Non-root Linux tests exercise trusted creation/idempotence, symlink/world-write/
nonprivate-profile refusal and parent replacement after preview. Separate tests
cover root/foreign-group refusal and diagnostic path/code filtering.

These are local process/unit checks. Interactive consent and actual booted
systemd service activation/reboot remain the live acceptance gate in docs/51.
