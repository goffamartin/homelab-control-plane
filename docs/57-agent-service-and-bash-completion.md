# Agent service commands and Bash completion

## Agent service control

```sh
hlcp-agent start                 # validate, then start; already running is a no-op
hlcp-agent start --enable        # also enable boot startup (linger must exist)
hlcp-agent restart               # validate first, then reload YAML via restart
hlcp-agent restart --dry-run     # read-only preflight and planned operation
hlcp-agent stop                  # stop only; keep boot startup setting
hlcp-agent status                # read the actual service state
hlcp-agent status --json         # versioned machine-readable report
```

These commands perform their named operation by default. Use `--dry-run` with
start/restart/stop for preview; `--json` changes output, not execution intent.
The optional `--apply --yes` pair is retained for explicit automation but is not
required for an explicit service command. `activate` is removed, not an alias.
This change applies to **hlcp-agent**; controller activation is unchanged.

The invoking non-root account defaults to user scope; root defaults to system
scope. Run user scope as the actual resource owner without sudo. Select a
different supported profile explicitly when needed:

```sh
hlcp-agent restart --scope user --unit hlcp-agent-podman.service
sudo hlcp-agent start --scope system
```

No auto-detection or takeover of another owner, manager, unit or peer service.
The generic user unit is the default. Existing packaged-unit, account, peer,
linger, private YAML/TLS and resource readiness checks remain in force for
start/restart. No permission repairs, privilege grants, identity regeneration,
journal reset or automatic resource actions occur. Invalid YAML/preflight
refuses restart **before stopping the running agent**. An observed configuration
change between planning and applying refuses the stale operation. A restart
failure after dispatch can leave the agent stopped or failed; it is explicitly
reported as unverified, without automatic rollback or retry.

Stop and status inspect the packaged service/manager boundary without reading
YAML, TLS or journals, so they work when configuration is invalid. Stop does
not disable the unit. Status successfully inspecting an inactive/failed service
is not an active-service health claim: inspect `state.active`. A verified active
service is not proof of mTLS registration or resource health; check `hlcp -n` on
the controller. Custom units/drop-ins remain outside this guarded interface.
After package/unit changes, a required manager daemon-reload is reported
explicitly; editing YAML itself does not require daemon-reload.

JSON has `schema_version: 1`, `operation: agent.start|restart|stop|status`,
`status`, `code`, `message`, `read_only`, `changed`, `uncertain`, `completed`,
and applicable `check`, `next`, `state`, `preview`, `result`, `constraints`.
Errors contain fixed safe codes, not configuration values or manager stderr.
Exit 0 means the requested operation/query completed, 1 a refused/unverified
operation and 2 invalid options/output failure. Service status is separate from
the agent/controller application protocol and does not create a command ID.

## Bash Tab completion

Debian controller packages install `hlcp` and `hlcp-controller` completions;
agent packages install `hlcp-agent` completion into
`/usr/share/bash-completion/completions/`. They recommend `bash-completion`,
which normal APT installation installs unless recommendations are disabled.
Open a new interactive Bash after installation. On minimal installations,
install/enable Debian's `bash-completion`, or load a script explicitly for the
current shell (no shell startup files are edited automatically):

```sh
source <(hlcp completion bash)
source <(hlcp-agent completion bash)
source <(hlcp-controller completion bash)
```

Each command prints its script without inspecting services, TLS or credentials.
Other shells are not supported by this version. The implementation uses Bash's
standard [programmable-completion interface](https://www.gnu.org/software/bash/manual/html_node/Programmable-Completion.html).

Examples (`<Tab>` means pressing the Tab key):

```text
hlcp-agent res<Tab>                   # resources / restart
hlcp-agent start --scope <Tab>        # user / system
hlcp -n <Tab>                        # visible node IDs
hlcp -n debian-01 -r <Tab>            # visible resources
hlcp -n debian-01 -r app -a <Tab>     # enabled resource action names
hlcp -n debian-01 -r app -a logs --stream <Tab>
```

Static command/flag completion is offline. Dynamic node/resource/action lists
use only the existing scoped read API and current CLI profile, or explicit
`--api`, `--api-token-file`, `--config`. No permission elevation or capability
grant occurs; completion is a hint, not execution authorization. Actual action
submission is still independently authorized. The helper makes at most one GET
per completion, with a 500ms HTTP budget, bounded input/body/candidate counts,
no proxies, redirects, shell evaluation, telemetry/body cache or error output.
Unavailable/denied/invalid APIs simply produce no dynamic candidates. Names are
filtered to safe identifier characters before entering Bash's candidate array.
It never dispatches commands, creates pairing codes, edits config or starts a
service, including when the line already contains `start` or `restart`.

Automated tests cover actual Bash execution, literal arrays/URL colon handling,
read-only scoped endpoint selection, token authentication, malformed/injection
names, quiet failures/timeouts, service preflight/invalid YAML, stale evidence,
partial outcomes and packaged completion content. Fake service hosts are not
live booted-Debian/systemd/reboot acceptance; that remains a separate VM check.
