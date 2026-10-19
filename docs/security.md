# Security and accounts

## Trust boundaries

Agents initiate outbound TLS 1.3 connections to the controller, using mutual
certificate authentication and ALPN `hlcp/1`. Node identity is bound to the agent
certificate, not a client-supplied display name. Managed pairing enrolls one
identity using a single-use code and pinned enrollment trust. Codes are secrets.

The local management API binds to loopback and requires an API credential, plus
Host/Origin protections. Keep it local. HLCP v1.0 does not add a public web login,
OAuth service, web UI or remotely accessible YAML editor.

## Who runs what

| Component | Normal identity | Authority |
|---|---|---|
| Controller service | Dedicated `hlcp-controller` account | Controller runtime state |
| Local CLI | Selected existing user | API permissions of its credential |
| User agent | Container/resource owner | That user's existing local rights |
| System agent | Dedicated packaged `hlcp` account | Restricted system profile, not unrestricted root |

Installing packages uses root; normal runtime does not inherit the installing
user's sudo authority. Selecting a CLI account does not make it the controller
service account. Each agent is configured locally under its own identity.

## Files and shared groups

Private config, credentials and state are checked for safe ownership, modes and
parent paths. Group-writable parents can allow another account to replace a
profile or redirect a credential path. Membership in a group, even one whose
members can use sudo, is not automatically explicit trust in that path.

Managed controller setup supports explicit `--trust-user-group` consent for
the selected user's primary group where applicable. This trusts that group for
the checked parent directories; it does not make tokens publicly readable or
silently chmod existing directories. Review group membership before consenting.
Default refusal protects against unintended sharing; it is not a reason to
recursively change ownership/modes or delete an existing profile.

## More than one CLI user

Multiple local accounts can access one controller. Provision each deliberately:

```sh
sudo hlcp-controller configure-client --user alice
sudo hlcp-controller configure-client --user alice --apply --yes
```

The first command previews; the second applies. The default role is `operator`;
an explicitly provisioned `--role observer` is read-only. This handoff does not
create a new independent API principal or node/resource policy automatically;
do not confuse separate private profile copies with separate audit identities.
For independently scoped/revocable credentials, use the
[API policy reference](reference/10-scoped-api-credentials.md).

Never copy another user's profile into a group-readable shared directory. Local
API users can read logs and action output within their granted scope; that data
may contain application secrets. No redaction system can guarantee removal of
secrets printed by an application/script.

## Execution and durability

The controller can select only agent-allowlisted resource/action names. It
cannot upload scripts, supply arbitrary shell text/argv or open an interactive
container console. Locally configured scripts are trusted code with the agent
account's rights. Docker socket permissions can effectively grant host control;
HLCP does not reduce those underlying permissions.

The agent journal and controller ledger retain bounded command history and
support same-ID deduplication/reconciliation. They do not promise exactly-once
external side effects across every crash. Unknown outcomes are explicit; timeout
is not rollback. Do not erase history to resolve a retry conflict.

Log reads are separately authorized and bounded. They do not enter command
history; HLCP is not a durable log archive or complete forensic audit store.
Keep runtime logging, backups and host security controls appropriate to your needs.

## Release trust and limitations

Use signed artifacts verified with the pinned release key, and authenticate the
installer itself through a trusted source. Checksums alone prove integrity, not
publisher identity. No private key or production credential belongs in a release.

Read [validation](validation.md): live disaster restoration, certificate lifecycle
fault cases and broad network-failure qualification remain deferred. A stable
version number is not an independent security audit or a high-availability guarantee.
