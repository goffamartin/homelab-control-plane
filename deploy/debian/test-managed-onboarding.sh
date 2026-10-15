#!/bin/sh
# Destructive ONLY inside a newly created disposable non-booted Docker container.
set -eu
if [ "${HLCP_PACKAGE_TEST:-}" != disposable-container ] || [ ! -f /.dockerenv ] || [ -d /run/systemd/system ]; then
 echo 'Fresh disposable non-booted Docker container required.' >&2
 exit 1
fi
apt-get install -y "$1" "$2"
adduser --disabled-password --gecos '' acceptance
preview_setup() {
 /usr/bin/hlcp-controller setup --listen 127.0.0.1:19443 --server-name 127.0.0.1 --enrollment-listen 127.0.0.1:19445 --enrollment-url https://127.0.0.1:19445 "$@"
}
preview_setup --json
test ! -e /root/hlcp-root-backup.json
printf '%s\n' 'disposable test password 123456' | preview_setup --cli-user acceptance --backup-password-stdin --backup-confirmed --apply --yes --json
test "$(stat -c %a /root/hlcp-root-backup.json)" = 600
authority_before=$(sha256sum /var/lib/hlcp-controller/managed/authority.json)
registry_before=$(sha256sum /var/lib/hlcp-controller/managed/nodes-managed.json)
preview_setup --apply --yes --json
test "$authority_before" = "$(sha256sum /var/lib/hlcp-controller/managed/authority.json)"
test "$registry_before" = "$(sha256sum /var/lib/hlcp-controller/managed/nodes-managed.json)"
test "$(stat -c %U /var/lib/hlcp-controller/managed/authority.json)" = hlcp-controller
install -m 600 -o hlcp-controller -g hlcp-controller /etc/hlcp/controller/api.token /var/lib/hlcp-controller/test-api.token
runuser -u hlcp-controller -- /usr/bin/hlcp-controller -listen 127.0.0.1:19443 -api-listen 127.0.0.1:9444 -managed-state /var/lib/hlcp-controller/managed -api-token-file /var/lib/hlcp-controller/test-api.token -ledger-file /var/lib/hlcp-controller/controller-operations.json >/root/controller-test.log 2>&1 &
controller_pid=$!
trap 'kill "$controller_pid" 2>/dev/null || true' EXIT
cli() { /usr/bin/hlcp --api-token-file /etc/hlcp/controller/api.token "$@"; }
attempt=0
until cli -n; do attempt=$((attempt+1));if [ "$attempt" -gt 20 ]; then cat /root/controller-test.log;exit 1;fi;sleep 1;done
runuser -u acceptance -- /usr/bin/hlcp -n
cli pairing create --node acceptance-01 --output-file /root/pairing-code
test "$(stat -c %a /root/pairing-code)" = 600
runuser -u acceptance -- /usr/bin/hlcp-agent join --pairing-stdin --scope user --json </root/pairing-code
test ! -e /home/acceptance/.config/hlcp/agent.yaml
runuser -u acceptance -- /usr/bin/hlcp-agent join --pairing-stdin --scope user --apply --yes --json </root/pairing-code
runuser -u acceptance -- /usr/bin/hlcp-agent join --resume --scope user --apply --yes --json
if runuser -u acceptance -- /usr/bin/hlcp-agent join --pairing-stdin --scope user --apply --yes </root/pairing-code; then echo 'Unexpected second join accepted';exit 1;fi
test "$(stat -c %U /home/acceptance/.local/state/hlcp/managed/identity.json)" = acceptance
runuser -u acceptance -- sed -i 's/^capabilities:.*/capabilities: [resource.control, script]/' /home/acceptance/.config/hlcp/agent.yaml
runuser -u acceptance -- sed -i '/^resources: \[\]$/c\resources:\n  - id: kernel\n    type: task\n    adapter: script\n    actions: [status]\n    commands:\n      status:\n        executable: /usr/bin/uname\n        argv: [-s]\n        working_directory: /\n        timeout: 10s' /home/acceptance/.config/hlcp/agent.yaml
runuser -u acceptance -- /usr/bin/hlcp-agent check-config --scope user --json
runuser -u acceptance -- env VISUAL=/bin/false /usr/bin/hlcp-agent configure --scope user --json
runuser -u acceptance -- env VISUAL=/bin/true /usr/bin/hlcp-agent configure --scope user
runuser -u acceptance -- /usr/bin/hlcp-agent resources list --scope user --json | grep 'kernel'
if runuser -u acceptance -- /usr/bin/hlcp-agent resources configure --scope user --json;then echo 'Removed writer was accepted';exit 1;fi
runuser -u acceptance -- /usr/bin/hlcp-agent certificates --scope user
runuser -u acceptance -- /usr/bin/hlcp-agent -config /home/acceptance/.config/hlcp/agent.yaml >/root/agent-test.log 2>&1 &
agent_pid=$!
trap 'kill "$agent_pid" "$controller_pid" 2>/dev/null || true' EXIT
attempt=0
until cli -n | grep 'acceptance-01.*online';do attempt=$((attempt+1));if [ "$attempt" -gt 20 ];then cat /root/agent-test.log /root/controller-test.log;exit 1;fi;sleep 1;done
cli -n acceptance-01 -r
cli -n acceptance-01 -r kernel -a status | tee /root/action-result
grep 'success.*true' /root/action-result
runuser -u acceptance -- /usr/bin/hlcp-agent certificates --scope user
journal_before=$(sha256sum /home/acceptance/.local/state/hlcp/command-journal.json)
cli certificates
cli pairing revoke --node acceptance-01 --yes
sleep 2
cli -n | grep 'acceptance-01.*offline'
test "$journal_before" = "$(sha256sum /home/acceptance/.local/state/hlcp/command-journal.json)"
echo 'Managed package/setup/pairing/join/script/diagnostics/revocation process acceptance passed. Live systemd/reboot NOT RUN.'
