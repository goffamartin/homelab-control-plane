# Existing installation migration and recovery

Source implemented and isolated package/process-tested; **no actual HomeLab VM
has been changed by this work**. Do not delete old CA keys, resource config,
credentials, journals, ledgers or manual-unit backups to make a test pass.

## Preflight / recoverable boundary

Record actual installed versions, IP/DNS, active user/system unit and FragmentPath,
drop-ins, account, node ID, resource IDs/actions, config/state locations, NTP and
certificate expiry. Never include private keys/tokens/config environment values
in reports/screenshots. The read-only collector in docs/47 is metadata only.
Existing VM facts from an earlier screenshot are not current verification.

Take a Proxmox recovery snapshot and an off-host private backup. Stop affected
services, then use the **trusted signed new helper binary** or installed new CLI:

```sh
sudo hlcp package-backup --role controller --scope system \
  --output /var/backups/hlcp/controller-before-migration.json --stopped-confirmed
```

As the actual agent user, after stopping its selected unit:

```sh
hlcp package-backup --role agent --scope user \
  --output "$HOME/.local/state/hlcp-backups/agent-before-migration.json" --stopped-confirmed
```

The archive is PRIVATE and contains secrets. It refuses symlinks/hardlinks,
unsafe modes/owners, a live managed-state lock, an existing output or a destination
inside the source. Source bytes are bounded (64 MiB total, 8 MiB per file, 1024
entries, eight directory levels). Larger installations need a separately reviewed
stopped-service backup; never skip a failed backup and continue APT. No automatic
restore command is provided: choosing a stale journal is a data-integrity decision,
not an unattended upgrade operation. Whole-host disaster recovery and compatible
binary rollback are different procedures.

## Imported-to-managed CA transition

The existing manual root may have pathLen:0. It cannot issue the new intermediary;
HLCP creates a NEW managed root rather than silently changing the old authority.
Migration requires a still-valid imported server certificate and explicit
self-signed peer trust anchors; complex external/intermediate legacy PKI needs a
separately reviewed maintenance migration, not inferred trust expansion.

1. Update the controller package first using verified artifacts. Keep its old
   private config, tokens and ledger. Stop the controller. Run:

   ```sh
   sudo hlcp-controller setup --migrate --interactive
   ```

   Enter the existing listen address/server name. Retain/acknowledge the NEW
   encrypted root backup off-host. Migration snapshots the original private
   config, imports the exact existing `nodes.json` IDs into the durable registry
   as explicitly marked legacy nodes, and keeps the old server leaf/trust while
   adding the new managed CA to client trust. It does not rotate API tokens or
   rewrite the operation ledger. The old nodes file ceases to be runtime truth.
   Start the controller explicitly. The separate HTTPS enrollment listener serves
   its new managed identity; HLCP 9443 temporarily retains the valid old server
   identity so existing agents can still authenticate it.

2. For each agent preserve its node ID, resources, journal and execution user.
   Stop its actual unit (for the old setup this may be the user
   `hlcp-agent-podman.service`). On the controller:

   ```sh
   hlcp pairing create --node debian-01 --replace --yes
   ```

   Replacement immediately revokes the old generation; do not create it before
   the maintenance window. On the agent, private stdin/TTY/file input only:

   ```sh
   hlcp-agent join --scope user --unit hlcp-agent-podman.service \
     --pairing-stdin --re-pair --apply --yes
   hlcp-agent activate --scope user --unit hlcp-agent-podman.service --apply --yes
   ```

   The new identity trusts the managed root and temporary old server root. Existing
   resource/capability/observation/retention/journal configuration is preserved;
   old identity/config have bounded private recovery snapshots. Unknown node IDs,
   unsafe files or another running profile are refused. No old journal is moved
   or reset. Verify registration, inventory and same-ID result lookup before the
   next agent. An interrupted join resumes its original saved key/request; no
   implicit regeneration. A generic profile can be selected explicitly only after
   the old profile is stopped/disabled and its package/manual override reviewed.

3. `hlcp pairing list` must show no active legacy node and no unfinished pairing.
   Explicitly revoke decommissioned nodes rather than forgetting them. Stop the
   controller and run:

   ```sh
   sudo hlcp-controller pki finish-migration --apply --yes
   sudo systemctl start hlcp-controller.service
   ```

   This refuses an incomplete migration, saves the preceding runtime config,
   removes old client trust and switches HLCP to the managed server identity on
   startup. Agents with the overlap bundle can already authenticate the new leaf.
   Stop each managed agent, then remove its temporary old server trust via the
   authenticated managed enrollment endpoint:

   ```sh
   hlcp-agent certificates refresh-trust --scope user --apply --yes
   systemctl --user start hlcp-agent-podman.service
   ```

   No key/certificate re-pair or journal change occurs during trust refresh. The
   controller's valid mTLS response must confirm the same pinned managed root.
   Revoked identities cannot refresh. Natural renewal also removes obsolete peer
   trust, and existing TLS configurations enforce the current narrower trust on
   new handshakes. Explicit refresh finishes cleanup immediately. Retain the old
   encrypted CA/recovery material until live acceptance and a separate human
   decision to archive/delete it; this code does not delete it.

Do not use `--offline-stopped` on a VM: it is restricted to explicitly stopped,
non-booted image/process acceptance. Normal migration requires the real packaged
service to be inactive before new state is committed.

## Issuer maintenance, expiry and failure

Before the one-year issuing certificate expires, stop the controller and use the
encrypted root archive and its hidden password:

```sh
sudo hlcp-controller pki renew-issuer \
  --backup-file /private/hlcp-root-backup.json --apply --yes
sudo systemctl start hlcp-controller.service
```

The root and issuer KEY remain the same, the issuing certificate gains a new
validity period bounded by root expiry, and existing chains/registry/history are
retained. A valid old server leaf continues normally; an expired but authentic
managed server leaf is renewed locally before serving. This is planned expiry
maintenance, NOT compromised-key recovery. The issuer/root eventually require
different recovery if their private keys are compromised or the root expires.

An expired/offline agent must explicitly re-pair; never disable date, hostname,
CA, explicit role or ALPN verification. Renewal failures retain the last valid
identity and bounded retry state. Deleted/corrupt admission files fail closed,
not as an empty registry or a legacy fallback.

## Recovery versus rollback

For a failed ordinary update keep the CURRENT journal/ledger and review the
reported partial state. Run no new command ID to compensate for an unknown
result. The installer refuses a downgrade; a deliberate compatible binary
rollback requires verifying protocol 1, managed state 1, journal 3, ledger 1,
the actual reader compatibility of the selected binary and the chosen artifacts,
stopping services and preserving the current state. Do not replace journal files
with the older update archive to make older binaries start.

Disaster restore uses the latest internally consistent stopped-service private
snapshot on an isolated fresh host, with reviewed service-user ownership, original
identity/config/state and external encrypted root archive. Validate config,
certificate/key/chain and node admission before making the controller reachable.
Never restore an older agent journal into a running node or automatically replay
old controller operations. If journal freshness is unknown, stop, preserve the
evidence and resolve unknown results manually. The booted acceptance gate must
exercise this recovery boundary before calling the release production-ready.
