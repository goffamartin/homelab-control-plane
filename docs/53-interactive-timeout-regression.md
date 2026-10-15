# Interactive wizard timeout regression

## Live observation (2026-10-15)

After user-operated removal of old preview.3 on two snapshotted VMs, the user
downloaded and verified signed preview.5 on the Debian controller. The helper
installed the controller package successfully. Interactive setup collected the
listener, server identity, CLI user and start/boot consent, then refused with
`SERVICE_MANAGER_RELOAD_FAILED`. Manual `sudo systemctl daemon-reload` succeeded;
the packaged service remained inactive/disabled. Historical journal entries do
not establish a new managed service startup.

Source inspection found a two-minute context created BEFORE interactive input.
Thus a user pausing to ask about a prompt could expire the context before any
systemd command. The source defect is confirmed; the generic live error alone
does not independently prove that timeout was the only possible failure cause.
No new CA or journal should be deleted as a workaround. Retry live with the
corrected exact signed release and preserve any state if another error appears.

## Fix intended for preview.6

- A signal-cancellable parent spans the command; human prompts have no deadline.
- Each technical phase has a fresh two-minute deadline; fixed child processes
  retain their thirty-second limit and enrollment retains its network limits.
- Controller phases restart after initial consent, password entry and off-host
  backup acknowledgement. The latter can take as long as copying the backup
  requires, without poisoning subsequent provisioning/startup.
- Agent join, resource configuration and interactive issuer-password entry use
  the same separation. Noninteractive password/code input remains bounded.
- Ordinary prompt reads now honor interruption, as secret prompts already do.
- Pairing invitations still expire normally: waiting does not extend admission
  lifetime, bypass certificate checks or silently generate replacement keys.

Virtual-time regression tests exercise twelve minutes of prompt waits, fresh
technical deadlines, actual operation timeout, parent cancellation and input
bounds/no secret echo. Full local suites and final artifact tests must pass
before publication. Booted controller setup/reboot remain a live retry gate;
container process tests are not a replacement for that evidence.
