# CLI

Run `hlcp` on the controller as a user with a provisioned local API profile.
Run `hlcp-agent` on the agent, as its service/resource owner. Use each command's
`--help` for complete options; these are the normal operational paths.

## Nodes, resources and actions

```sh
hlcp -n                                    # list visible nodes
hlcp -n debian-01                          # node detail
hlcp -n debian-01 -r                       # resources
hlcp -n debian-01 -r app                   # resource detail
hlcp -n debian-01 -r app -a                # enabled actions
hlcp -n debian-01 -r app -a status
hlcp -n debian-01 -r app -a restart --dry-run
hlcp -n debian-01 -r app -a restart
```

`-n/--node`, `-r/--resource`, and `-a/--action` select progressively. `status`
is an action value, not a positional subcommand. An action requires its node
and resource; `hlcp -r` alone is not a global resource list. Dry-run performs
admission checks without executing; it is not proof that the runtime will succeed.

Actions return a command ID. Keep it to look up the result after disconnection:

```sh
hlcp -n debian-01 --result COMMAND_ID
hlcp -n debian-01 -r app -a restart --command-id maintenance-001
```

Retry the same logical operation with the same ID and payload, not a fresh ID.
Reusing an ID with a different payload is a conflict. Retention bounds replay
protection; unknown outcomes require inspection, not blind retries. A successful
runtime result does not prove the application is healthy.

## Container logs

```sh
hlcp -n debian-01 -r app -a logs --tail 20
hlcp -n debian-01 -r app -a logs --follow --tail 0
hlcp -n debian-01 -r app -a logs --tail 200 --contains error --json
```

Enable `logs` in resource YAML first. Ctrl+C stops reading, not the container.
History uses the runtime's retention; HLCP is not a log archive. Time filters
require RFC3339 timestamps, not relative strings such as `10m`. Follow is
bounded, not an endless lossless connection. Inspect `gap`, `truncated`, terminal
codes and `next_cursor` in machine output. See the [log reference](reference/56-container-logs.md).

## Agent service

```sh
hlcp-agent configure
hlcp-agent check-config
hlcp-agent start --enable
hlcp-agent restart
hlcp-agent restart --dry-run
hlcp-agent stop
hlcp-agent status
```

Service commands perform the named operation by default. `--dry-run` previews
start/restart/stop; `--json` changes format, not execution intent. `stop` does
not disable boot startup. `status` inspecting an inactive service successfully
is not a running-service claim. The old agent `activate` command is removed.
An already-running `start` does not reload YAML; use `restart` after edits.

For non-root use user scope without sudo. Root defaults to system scope;
use `--scope` explicitly when needed. The generic `hlcp-agent.service` is the
default; an existing compatible Podman profile needs
`--scope user --unit hlcp-agent-podman.service`. Custom units/drop-ins are not
silently adopted. See the [service contract](reference/57-agent-service-and-bash-completion.md).

## Machine-readable output

Use `--json`, never scrape tables or English messages. Pairing codes and tokens
are secrets regardless of output format.

| Command family | Machine contract |
|---|---|
| `hlcp` inventory/actions/results | Existing API payload for that command |
| Managed setup/join/configuration/resources/certificates | Versioned administrative report |
| Imported setup, `configure-client`, doctor | Existing command-specific report; not the new common envelope |
| Agent service commands | Versioned service report, including actual state |
| Container log history | Versioned snapshot with records and continuity flags |
| Container log follow | Versioned newline-delimited JSON events |
| Installer | One result on stdout; progress/details on stderr |

There is not one universal envelope. Administrative consumers should inspect
`schema_version`, `status`, `code`, `changed`, `completed` and `uncertain`, as
applicable; service consumers also inspect `state`. Reject unsupported schema
versions and tolerate unknown fields. A partial result can mean configuration
was saved but service startup failed. It is not equivalent to “nothing changed.”
See [administrative output](reference/55-administrative-output.md) and the
command-specific references for fields and exit codes. Installer
`--json --configure` is rejected because the setup wizard is interactive.

## Bash completion

Debian packages install completion files and recommend `bash-completion`.
Open a new interactive Bash/SSH session after installation. To enable completion
in the current shell:

```sh
source <(hlcp completion bash)             # controller CLI host
source <(hlcp-controller completion bash)  # controller host
source <(hlcp-agent completion bash)       # agent host
```

Load only installed binaries. Press Tab twice when several choices match.
Commands/flags complete offline. Node/resource/action names need a reachable
controller API and an authorized CLI profile. Dynamic completion performs
bounded read-only queries, never actions. Only Bash is supported in v1.0.
