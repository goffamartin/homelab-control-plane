# Configuration

Agent YAML is the source of truth for allowed resources and actions. Discovery
only lists candidates; it never imports resources or grants permissions.

## Edit, validate, apply

Run as the agent's actual resource owner:

```sh
hlcp-agent configure
hlcp-agent check-config
hlcp-agent restart
```

`configure` opens the existing file using `VISUAL`, `EDITOR`, then an available
`nano`/`vi`. It does not validate or restart automatically. `configure --json`
reports the location without opening an editor. `check-config` checks YAML syntax
and schema only; restart performs additional readiness checks before stopping
an existing service. Keep a private backup before editing.

| Scope | Configuration | Service identity |
|---|---|---|
| User, recommended | `~/.config/hlcp/agent.yaml` | Invoking non-root user |
| System, advanced | `/etc/hlcp/agent/agent.yaml` | Packaged `hlcp` account |

User commands default to user scope; root defaults to system scope. For a system
agent use `sudo hlcp-agent configure --scope system` and the same explicit scope
for validation/service operations. Do not accidentally edit one scope and start
another. The runtime binary's low-level `-config` default is not the managed
user-profile path; use the service commands for managed deployments.

## Container resource

Merge these sections into the generated file; this is **not a complete agent
configuration**. Preserve controller address/name, identity/TLS paths, managed
directory, node ID and journal settings.

```yaml
capabilities: [resource.control, podman]
resources:
  - id: app
    type: container
    adapter: podman
    target: my-existing-container
    actions: [status, start, stop, restart, logs]
```

- `id`: stable name exposed to HLCP clients; unique within the agent.
- `target`: existing container name understood by the local runtime.
- `adapter`: `podman` or `docker`; use the matching capability as well.
- `actions`: explicit allowlist. Remove mutations you do not want available.

For Docker, change both occurrences of `podman` to `docker`. Runtime access must
already work under the service account. Docker socket access can confer broad
host privileges; it is not equivalent to rootless Podman isolation.

```sh
hlcp-agent resources list
hlcp-agent resources discover --adapter podman
```

No `resources configure/remove` mutation workflow exists; edit YAML instead.
After restart/reconnect, inspect inventory from the controller with `hlcp -n
debian-01 -r`. Changing YAML does not create, pull, start or delete containers.

## Custom named actions

You can add fixed local scripts to any resource, including a container:

```yaml
capabilities: [resource.control, podman, script]
resources:
  - id: app
    type: container
    adapter: podman
    target: my-existing-container
    actions: [status, restart, backup]
    commands:
      backup:
        executable: /bin/bash
        argv: [/opt/hlcp/scripts/backup.sh]
        working_directory: /opt/hlcp
        timeout: 10s
```

Create and review that script locally first; this example does not install it.
The service user must have the necessary access. The controller selects only
the resource/action name, never an executable, script body, environment or argv.
No implicit pre/post hooks run. Native `status/start/stop/restart`, and container
`logs`, cannot be overridden. Every custom action must have a matching command.

For a standalone script, use `type: task`, `adapter: script`, omit `target`,
and provide a command for every enabled action. Use capabilities
`[resource.control, script]`. Scripts have the service account's rights and are
not an additional security sandbox. Their output is bounded and retained in
command history, so never print secrets. Timeouts do not undo side effects.
See the [script contract](reference/18-script-resource-contract.md) for limits,
environment handling and uncertain outcomes.

## Advanced profiles

System-scope scripts require the explicit sandbox metadata probe/start workflow;
ordinary readiness cannot establish every resource's access inside a systemd
sandbox. `hlcp-agent probe-resources --help` describes the guarded system-only
path. Probing does not execute configured actions. Mixed system-scope runtime
resources can still be refused when readiness cannot be proven.

The `systemd` adapter uses `type: service`, a local unit `target`, and capabilities
`[resource.control, systemd]`. Service-manager authorization and sandbox constraints
still apply; user-scope native mutations are not generally authorized by the
managed readiness path. These are advanced profiles, not interchangeable copies
of the rootless-container tutorial. See [validation](validation.md).

Unknown fields and duplicate YAML keys are rejected. Legacy empty `commands: {}`
on native resources remains valid and needs no cleanup. Resource edits do not
require re-pairing or deleting journals.
