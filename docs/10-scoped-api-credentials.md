# Scoped controller API credentials — F10B

The local API stays loopback-only with the same Host/Origin, body, JSON and
timeout protections. F10B adds separate bearer identities and server-side scope;
it does not add a remote login, OAuth service, public API, MCP server or web UI.

## Startup and migration

Two mutually exclusive modes:

- Existing `-api-token-file`: one `local-api` operator with unrestricted node /
  resource scope. It preserves the existing CLI setup, not a read-only credential.
- New `-api-policy-file /etc/hlcp/api-policy.json`: versioned policy with separate
  principals. An explicitly supplied `-api-token-file` is rejected in this mode;
  the default token path is not silently loaded as an additional admin identity.

The [example policy](../examples/api-policy.json) keeps the existing token path
for the CLI operator and uses a different file for a limited MCP observer. It
contains no credentials and must be adapted before deployment. CLI grammar and
JSON output are unchanged; select a credential with `--api-token-file`.

Policy and token files must be regular, private files (0600 on Unix; Windows ACL
protection remains the operator's responsibility). Parent directories must also
be administrator-owned and not adapter-writable. Policy loading is bounded to
64 KiB, 32 principals, 128 nodes per principal and 256 resources per node. Relative
token paths resolve against the policy file directory. Unknown/case-variant
schema fields, duplicate JSON keys, IDs, resource IDs or credential digests,
invalid roles/identifiers and ambiguous wildcards fail loading without echoing
file contents or secret paths. Only SHA-256 credential digests remain in the
active policy; plaintext token text is not kept in its principals or audit.

## Permission contract

| API operation | Observer | Operator |
|---|---|---|
| Node/resource inventory and observed state | Read within scope | Read within scope |
| Action catalog | Read within scope; not an execution grant | Read within scope |
| Retained command result | Read if its stored resource is in scope | Same |
| POST resolve/dry-run | Non-mutating preview within scope | Same |
| POST commands, including resource `status` | Always denied | Allowed within scope and normal resolver/agent rules |

`status` through POST commands creates a ledger entry and sends COMMAND, even
though the underlying adapter observes state. An observer uses resource detail /
node GET snapshots instead. A preview is not an execution capability or approval.
Even re-submitting an already completed ID to POST commands requires operator
permission; observers use the dedicated GET lookup endpoint.

`scope.all_nodes: true` explicitly grants all nodes and resources. It cannot be
combined with node entries. Otherwise only map keys in `scope.nodes` are readable.
Each node grants its metadata, plus either explicit `resources` or
`all_resources: true`, never both. Empty/missing resource lists grant no resource
access; empty scope grants no node access. There is no implicit wildcard from a
missing property, role name or spoofed header. Node-level metadata (capabilities,
session and inventory revision) remains visible for an allowed node even when
its resources are filtered; revision changes are not a filtered-resource counter.

All node/list/detail responses filter inventory and observation arrays, without
altering the shared registry. Direct out-of-scope node/resource access returns
generic 403. Missing and hidden-resource command lookups both return 404 without
the stored payload/result. Resolve cannot use an allowed-resource payload to
obtain a conflict/result for a hidden resource's known operation ID.
Future diagnostics/log endpoints must explicitly apply this boundary too;
global metrics/audit must not simply expose hidden nodes to a scoped observer.

Authorization does not trust `X-User`, `X-Role` or client-supplied actor names.
Audit attribution uses the authenticated principal ID. Duplicate Authorization
headers are rejected. Trusted internal Go callers still have their internal
execution interface; adapters must use the protected HTTP API, not that bypass.

## Rotation, removal and concurrency

Policy mode accepts Linux SIGHUP to reread policy and all token files. Prepare
the files first, then signal the process under the deployment procedure. Windows
deployments use a controlled restart, not a claim of Unix signal support.
No unauthenticated or privileged HTTP reload endpoint was introduced.

The loader builds a complete immutable snapshot before swapping it. Invalid
reload leaves the last good policy intact and emits `POLICY_RELOAD_REJECTED`;
successful reload emits `POLICY_RELOADED`. Inspect those audit events: editing a
file alone does not rotate the active policy. Legacy token mode rotates by restart.

New requests use the new snapshot. Every successful reload invalidates older
request snapshots at durable admission, even if a principal is unchanged.
Permission checking and admission serialize with policy replacement. The lock
covers the bounded admission/ledger write, **not** the action or result wait;
reload can wait for an admission already in progress but not for an external
container command. Requests revoked while decoding/resolving cannot subsequently
admit. Already admitted actions continue under the existing durable ledger /
deduplication contract; removal does not retroactively undo a side effect.
Already authorized reads can finish with their original snapshot. Restart or a
separate connection/operation cancellation policy would be needed for stronger
in-flight read revocation; do not interpret token removal as erasing delivered data.

## Evidence and remaining work

Tests cover strict/private policy parsing; identity/scope and duplicate headers;
atomic failed/successful reload; token rotation and old-snapshot rejection under
concurrency; mutually exclusive startup modes and reload-worker shutdown;
scope filtering across every existing endpoint, hidden operation IDs, spoofed
headers and byte-for-byte unchanged ledger after observer requests.

The production mTLS integration verifies zero COMMAND frames/admissions/side
effects for observer `status`/`restart`, and one effect with the authenticated
operator in audit. Revocation injected during request-body decoding returns 403
without creating a ledger record. Existing real CLI/recovery tests retain legacy
compatibility. These tests use a fake external container runner, not live deployment.

Full Windows test/vet and isolated Linux race suites passed with Go 1.27.2.

F10C will add scoped bounded diagnostic queries; M1 will use a dedicated observer
credential after its transport/auth profile is chosen. No production credential,
running service or firewall was modified here. Debian deployment remains F10D/E.
