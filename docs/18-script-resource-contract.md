# Local script resources (P02/P03)

P02 defined the configuration; P03 adds Linux execution with no container runtime.
Existing Docker, Podman and systemd configurations still run. Non-Linux script
startup fails before opening the journal. `hlcp-agent -check-config`
validates the schema without accessing TLS files, probing executables or running
scripts. A successful check does not establish executable availability or rights.

## Agent-local resource example

Add `script` and `resource.control` to the agent's capabilities and configure:

```yaml
resources:
  - id: diagnostics
    type: task
    adapter: script
    actions: [diagnose, reload]
    commands:
      diagnose:
        executable: /bin/bash
        argv: [/opt/hlcp/diagnose.sh]
        working_directory: /opt/hlcp
        timeout: 10s
        environment:
          LANG: C.UTF-8
      reload:
        executable: /opt/hlcp/reload
        argv: []
        working_directory: /opt/hlcp
        timeout: 20s
```

Every action must have exactly one matching command; extra/missing commands,
duplicate keys, unknown fields and duplicate actions are rejected. Script
resources have no `target`. Other adapters do not accept `commands`.

Executable and working directory are required clean absolute Linux paths, at
most 4096 bytes, valid UTF-8 without NUL. No executable PATH lookup or implicit
shell. Relative arguments are literal arguments interpreted by the configured
program relative to the explicitly selected working directory. Local `/bin/bash`
with an absolute script argument is supported by this contract; inline shell
programs and remote arguments are not part of the HLCP command protocol.

Limits: 1–32 actions, 0–32 argv entries and 8 KiB total argv bytes including one
terminator per argument. Timeout omitted/zero means 10s; positive values must
not exceed 20s. Explicit environment permits 0–32 POSIX-named variables with
8 KiB total including `=` and terminators. Values/arguments must be valid UTF-8
without NUL. Semantic limit checks do not echo argv or environment values;
YAML parser diagnostics may quote malformed input, so do not store secrets here.

The process environment starts with only `PATH=/usr/bin:/bin`, `LANG=C.UTF-8`
and `LC_ALL=C.UTF-8`, plus the locally configured overrides. It does not inherit
the agent's environment (including HOME, tokens, credential paths, SSH_AUTH_SOCK
and loader variables). `environment` is an explicit trusted local override,
not a remote input and not a secret store. Configuration and scripts should be
owned and writable only by the operator/agent account. A script has that account's
local rights; it is not sandboxed and gains no implicit sudo privileges.

## Controller and CLI

Only resource ID, type `task` and action names appear in the existing inventory.
Executable, argv, working directory and environment never travel over HLCP.
The controller requires `resource.control` + `script`. An older controller
rejects the unsupported task type through its existing capability check; there
is no new frame or protocol version. Upgrade controller before enabling P03 agents.

The existing syntax works without a new CLI grammar:

```sh
hlcp -n debian-01 -r diagnostics -a
hlcp -n debian-01 -r diagnostics -a diagnose --dry-run
hlcp -n debian-01 -r diagnostics -a reload
```

Dry-run checks admission without execution or ledger changes. Every fresh task
action is treated as potentially mutating, including `status`. A known same-ID
operation remains a read-only lookup, not a second invocation. No runtime argv,
script upload, command string or resource/action remapping is accepted.

Task observations remain `unknown / NOT_OBSERVED`; no script is called for
automatic polling, including one named `status`.

## Execution and outcomes

One dispatcher worker executes directly with fixed argv and an explicit working
directory. There is no stdin stream or interactive terminal. Combined stdout/stderr
capture is at most 4 KiB, including an explicit `[output truncated]` marker when
necessary. Extra output is drained and discarded. The result is valid UTF-8;
invalid bytes become `?`. Scripts must not print secrets: output is retained in
the private journal/controller ledger and returned by the authorized result API,
not sanitized into safe content. Global telemetry does not receive raw output.

- Exit 0: `OK`; nonzero exit: `EXECUTION_FAILED` (failure is not rollback).
- Cannot start or canceled before startup: `EXECUTION_NOT_STARTED`.
- Timeout, signal termination, uncertain pipe completion or interrupted execution:
  `EXECUTION_UNKNOWN`. Side effects may already have happened; do not repeat with
  a new command ID merely because success was not reported.

On cancellation, the Linux process group receives TERM, then KILL after up to
250ms, with a bounded 1s final wait. Inherited output pipes after leader exit have
a 250ms wait limit. Same-group background children are killed even after normal
leader exit: this contract is for bounded commands, not daemon launching.
An unkillable OS process keeps the agent's one script process slot occupied until
reaped; further scripts fail before startup instead of accumulating processes.
The configured maximum 20s plus cleanup fits the default 30s result budget; a
custom shorter controller result timeout can still expire before completion.

Process groups are not a sandbox. A child can escape with `setsid`, access files
allowed to the agent account or have persistent external effects. A hard agent
crash does not itself terminate an independently grouped script; the systemd
unit's default cgroup cleanup handles service stop/restart. Full per-command
cgroup containment is not provided. Do not configure daemonizing/untrusted scripts.

Admission is journaled before ACK; execution starts after successful ACK delivery.
Duplicate IDs return the retained result or active job, never another invocation.
Changed payloads conflict. After a crash, pending intent is recovered as unknown
and reconciled read-only; neither agent nor controller automatically replays it.
This is bounded deduplication with the documented journal retention, not exactly-once
side effects or unlimited ID history.

## Linux deployment smoke (operator-run)

Update controller and agent together; no automatic service restart happens during
APT installation. The packaged `/usr/share/hlcp-agent/agent-script.yaml` is an
inactive generic-user example with `host-check / diagnose` mapped to `uname --all`.
Review and merge its resource/capabilities into your own configuration; do not
overwrite existing keys, journal paths or container resources. Use the generic
user unit described in [the profile runbook](17-generic-agent-profiles.md), or keep
the legacy Podman unit during migration. Both use the same agent binary.

As the actual resource owner on the agent:

```sh
hlcp-agent -config ~/.config/hlcp/agent.yaml -check-config
# Restart ONLY the currently selected user service after reviewing the config.
systemctl --user restart hlcp-agent.service
systemctl --user status hlcp-agent.service --no-pager -l
```

On the configured controller CLI:

```sh
hlcp -n debian-01 -r host-check -a
hlcp -n debian-01 -r host-check -a diagnose --dry-run
hlcp -n debian-01 -r host-check -a diagnose --command-id script-smoke-01
hlcp -n debian-01 --result script-smoke-01
```

Reuse that ID only for the same original operation. Reboot/linger and live
Docker/Podman regressions remain operator VM gates; container/parser tests do not
claim them as verified production deployment.
