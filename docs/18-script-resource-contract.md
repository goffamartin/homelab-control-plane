# Local script resource contract (P02)

P02 defines and validates configuration only. Script execution ships in P03;
until then startup with a script resource fails explicitly before opening the
command journal. Existing Docker, Podman and systemd configurations still run.
Do not deploy this example as a running agent yet. `hlcp-agent -check-config`
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

P03 will supply a minimal documented process environment instead of inheriting
the agent's environment. `environment` is an explicit trusted local override,
not a remote input and not a secret store. Configuration and scripts should be
owned and writable only by the operator/agent account. A script has that account's
local rights; it is not sandboxed and gains no implicit sudo privileges.

## Controller and CLI

Only resource ID, type `task` and action names appear in the existing inventory.
Executable, argv, working directory and environment never travel over HLCP.
The controller requires `resource.control` + `script`. An older controller
rejects the unsupported task type through its existing capability check; there
is no new frame or protocol version. Upgrade controller before enabling P03 agents.

Once P03 is installed, the existing syntax will work without a new CLI grammar:

```sh
hlcp -n debian-01 -r diagnostics -a
hlcp -n debian-01 -r diagnostics -a diagnose --dry-run
hlcp -n debian-01 -r diagnostics -a reload
```

Dry-run checks admission without execution or ledger changes. Every fresh task
action is treated as potentially mutating, including `status`. A known same-ID
operation remains a read-only lookup, not a second invocation. No runtime argv,
script upload, command string or resource/action remapping is accepted.

P03 will leave task observations `unknown / NOT_OBSERVED` and never call a script
for automatic polling. Bounded output, cancellation and durable outcome handling
are P03 work, not promises of this configuration-only stage.
