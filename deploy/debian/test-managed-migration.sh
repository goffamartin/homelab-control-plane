#!/bin/sh
# ONLY an isolated disposable container. No real HomeLab credentials or VM use.
set -eu
umask 077
if [ "${HLCP_PACKAGE_TEST:-}" != disposable-container ]||[ ! -f /.dockerenv ]||[ -d /run/systemd/system ];then exit 1;fi
apt-get install -y "$1" "$2"
adduser --disabled-password --gecos '' acceptance
fixture=$(mktemp -d /root/hlcp-legacy-test.XXXXXXXX)
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -keyout "$fixture/ca.key" -out "$fixture/ca.crt" -days 30 -subj /CN=Legacy-Test-Root -addext 'basicConstraints=critical,CA:TRUE,pathlen:0' -addext 'keyUsage=critical,keyCertSign,cRLSign' 2>/dev/null
openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -keyout "$fixture/controller.key" -out "$fixture/controller.csr" -subj /CN=127.0.0.1 2>/dev/null
printf '%s\n' 'basicConstraints=critical,CA:FALSE' 'keyUsage=critical,digitalSignature' 'extendedKeyUsage=serverAuth' 'subjectAltName=IP:127.0.0.1' >"$fixture/server.ext"
openssl x509 -req -in "$fixture/controller.csr" -CA "$fixture/ca.crt" -CAkey "$fixture/ca.key" -set_serial 2 -days 15 -out "$fixture/controller.crt" -extfile "$fixture/server.ext" 2>/dev/null
hlcp-controller setup --imported --listen 127.0.0.1:19443 --server-name 127.0.0.1 --cert "$fixture/controller.crt" --key "$fixture/controller.key" --issuer-ca "$fixture/ca.crt" --peer-ca "$fixture/ca.crt" --admit legacy-01 --apply --yes
install -d -m 700 -o hlcp-controller -g hlcp-controller /var/lib/hlcp-controller
for name in controller.crt controller.key ca.crt api.token nodes.json;do install -m 600 -o hlcp-controller -g hlcp-controller "/etc/hlcp/controller/$name" "/var/lib/hlcp-controller/test-$name";done
install -d -m 700 -o acceptance -g acceptance /home/acceptance/legacy-material
runuser -u acceptance -- openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -keyout /home/acceptance/legacy-material/agent.key -out /home/acceptance/legacy-material/agent.csr -subj /CN=legacy-01 2>/dev/null
printf '%s\n' 'basicConstraints=critical,CA:FALSE' 'keyUsage=critical,digitalSignature' 'extendedKeyUsage=clientAuth' 'subjectAltName=URI:urn:hlcp:node:legacy-01' >"$fixture/agent.ext"
openssl x509 -req -in /home/acceptance/legacy-material/agent.csr -CA "$fixture/ca.crt" -CAkey "$fixture/ca.key" -set_serial 3 -days 15 -out "$fixture/agent.crt" -extfile "$fixture/agent.ext" 2>/dev/null
install -m 600 -o acceptance -g acceptance "$fixture/agent.crt" /home/acceptance/legacy-material/agent.crt
install -m 600 -o acceptance -g acceptance "$fixture/ca.crt" /home/acceptance/legacy-material/ca.crt
install -d -m 700 -o acceptance -g acceptance /home/acceptance/.local/state/hlcp
install -d -m 700 -o acceptance -g acceptance /home/acceptance/.config
runuser -u acceptance -- hlcp-agent setup --scope user --directory /home/acceptance/.config/hlcp --node legacy-01 --controller 127.0.0.1:19443 --server-name 127.0.0.1 --journal /home/acceptance/.local/state/hlcp/command-journal.json --cert /home/acceptance/legacy-material/agent.crt --key /home/acceptance/legacy-material/agent.key --issuer-ca /home/acceptance/legacy-material/ca.crt --peer-ca /home/acceptance/legacy-material/ca.crt --apply --yes
runuser -u acceptance -- hlcp-agent resources configure --scope user --id kernel --adapter script --actions status --executable /usr/bin/uname --arg=-s --apply --yes
sed -i 's/^agent_version:.*/agent_version: legacy-version/' /home/acceptance/.config/hlcp/agent.yaml
controller_pid=;agent_pid=
trap 'if [ -n "$agent_pid" ];then kill "$agent_pid" 2>/dev/null||true;fi;if [ -n "$controller_pid" ];then kill "$controller_pid" 2>/dev/null||true;fi' EXIT
cli(){ hlcp --api-token-file /etc/hlcp/controller/api.token "$@"; }
start_controller(){
 runuser -u hlcp-controller -- hlcp-controller -listen 127.0.0.1:19443 -api-listen 127.0.0.1:9444 -cert /var/lib/hlcp-controller/test-controller.crt -key /var/lib/hlcp-controller/test-controller.key -ca /var/lib/hlcp-controller/test-ca.crt -api-token-file /var/lib/hlcp-controller/test-api.token -ledger-file /var/lib/hlcp-controller/controller-operations.json "$@" >/root/controller-migration.log 2>&1 &
 controller_pid=$!
 attempt=0;until cli -n;do attempt=$((attempt+1));if [ "$attempt" -gt 20 ];then cat /root/controller-migration.log;exit 1;fi;sleep 1;done
}
start_agent(){
 runuser -u acceptance -- hlcp-agent -config /home/acceptance/.config/hlcp/agent.yaml >/root/agent-migration.log 2>&1 &
 agent_pid=$!
 attempt=0;until cli -n|grep 'legacy-01.*online';do attempt=$((attempt+1));if [ "$attempt" -gt 20 ];then cat /root/agent-migration.log /root/controller-migration.log;exit 1;fi;sleep 1;done
 cli -n|grep -F "$(hlcp-agent --version|awk '{print $1}')"
}
stop_agent(){ kill "$agent_pid";wait "$agent_pid"||true;agent_pid=; }
stop_controller(){ kill "$controller_pid";wait "$controller_pid"||true;controller_pid=; }
start_controller -node-allowlist-file /var/lib/hlcp-controller/test-nodes.json
start_agent
cli -n legacy-01 -r kernel -a status --command-id migration-keep-1|grep 'success.*true'
stop_agent
stop_controller
journal_before=$(sha256sum /home/acceptance/.local/state/hlcp/command-journal.json)
ledger_before=$(sha256sum /var/lib/hlcp-controller/controller-operations.json)
credentials_before=$(sha256sum /etc/hlcp/controller/controller.crt /etc/hlcp/controller/controller.key /etc/hlcp/controller/ca.crt /etc/hlcp/controller/api.token /etc/hlcp/controller/nodes.json)
printf '%s\n' 'disposable migration backup password'|hlcp-controller setup --migrate --offline-stopped --listen 127.0.0.1:19443 --server-name 127.0.0.1 --enrollment-listen 127.0.0.1:19445 --enrollment-url https://127.0.0.1:19445 --backup-password-stdin --backup-confirmed --apply --yes
test "$credentials_before" = "$(sha256sum /etc/hlcp/controller/controller.crt /etc/hlcp/controller/controller.key /etc/hlcp/controller/ca.crt /etc/hlcp/controller/api.token /etc/hlcp/controller/nodes.json)"
if hlcp-controller pki finish-migration --apply --yes;then echo 'Premature migration finish accepted';exit 1;fi
start_controller -managed-state /var/lib/hlcp-controller/managed
start_agent
cli pairing list|grep '"legacy":true'
stop_agent
cli pairing create --node legacy-01 --replace --yes --output-file /root/migration-code
runuser -u acceptance -- hlcp-agent join --scope user --pairing-stdin --re-pair --offline-stopped --apply --yes </root/migration-code
test "$journal_before" = "$(sha256sum /home/acceptance/.local/state/hlcp/command-journal.json)"
runuser -u acceptance -- hlcp-agent resources list --scope user|grep kernel
start_agent
cli -n legacy-01 -r kernel -a status --command-id migration-keep-1|grep 'success.*true'
stop_agent
stop_controller
test "$ledger_before" = "$(sha256sum /var/lib/hlcp-controller/controller-operations.json)"
hlcp-controller pki finish-migration --apply --yes
start_controller -managed-state /var/lib/hlcp-controller/managed
# Both-trust agent still connects to the new controller leaf before cleanup.
start_agent
stop_agent
runuser -u acceptance -- hlcp-agent certificates refresh-trust --scope user --apply --yes
runuser -u acceptance -- hlcp-agent certificates --scope user >/root/certificates-after-migration
if grep '"migration":true' /root/certificates-after-migration;then echo 'Legacy trust retained';exit 1;fi
start_agent
cli -n legacy-01 -r kernel -a status --command-id migration-keep-1|grep 'success.*true'
stop_agent
stop_controller
test "$journal_before" = "$(sha256sum /home/acceptance/.local/state/hlcp/command-journal.json)"
test "$ledger_before" = "$(sha256sum /var/lib/hlcp-controller/controller-operations.json)"
echo 'Imported-to-managed overlap/re-pair/cutover/trust refresh preserved resources, journal, ledger and same-ID result. Live systemd/reboot NOT RUN.'
