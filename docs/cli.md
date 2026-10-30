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

## Resource tag filters (unreleased)

Requires a controller/CLI and agent release containing tag support; not available
in v1.0.0/preview.10. Configure [resource tags](configuration.md) on the agent,
then list matching resources on one selected node:

```sh
hlcp -n debian-01 -r --tag kostka
hlcp -n debian-01 -r -t kostka -t production --json
```

`-t` and `--tag` are aliases; repeat for AND (all tags must match). Matching is
exact and case-sensitive; use lowercase identifiers, not globs, expressions or
comma-separated values. Unknown tags return an empty list. Without filters,
untagged resources remain visible. Maximum 16 unique filters of 64 bytes each.
Filters are valid only on resource lists, never a specific resource/action,
result lookup or dry-run. They do not dispatch actions or grant permissions.

Text lists show a `TAGS` column when any row has tags. JSON adds optional `tags`
arrays to inventory descriptors; empty tags are omitted. Bash completes tag
values from the selected node's authorized tag catalog. Already selected tags
are excluded; additional suggestions come from resources matching prior filters.

An older controller can ignore unknown query parameters. The new CLI therefore
requires `X-HLCP-Resource-Tags: 1` on a successful filtered response; otherwise it
exits 4 instead of showing an unfiltered list. JSON mode reports
`schema_version: 1`, `code: TAGS_UNSUPPORTED` and a fixed upgrade message.
Ordinary unfiltered navigation remains compatible. Old controllers discard
unknown inventory tag fields, so upgrade the controller before relying on tags.

### Local API for web clients

```text
GET /v1/nodes/{node}/resources?tag=kostka&tag=production
GET /v1/nodes/{node}/tags
GET /v1/nodes/{node}/tags?tag=kostka
```

The first returns the existing resource array filtered with AND. The catalog
returns sorted unique tag strings belonging only to visible resources; optional
filters restrict the contributing resources. Empty results are `[]`, not null.
Both endpoints require the existing loopback/bearer/Host/Origin protections.
Resource scope is applied before matching/catalog extraction. Unknown query keys,
malformed encoding, invalid/empty/duplicate tags and queries over 2048 bytes
return HTTP 400 `INVALID_QUERY`, without reflecting supplied values.

Tags do not change CBOR protocol version, durable commands, journals or ledgers.
The optional inventory map field is omitted when empty and ignored by v1's old
decoder; it does not change old untagged wire encodings. YAML unknown-field
validation remains strict on old agents. No remote YAML editor or tag mutation
API is added.

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
