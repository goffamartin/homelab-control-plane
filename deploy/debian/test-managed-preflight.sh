#!/bin/sh
# Fixture ONLY inside a newly created disposable non-booted container.
set -eu
if [ "${HLCP_PACKAGE_TEST:-}" != disposable-container ] || [ ! -f /.dockerenv ] || [ -d /run/systemd/system ]; then exit 1; fi
apt-get install -y "$1"
adduser --disabled-password --gecos '' acceptance
install -d -o acceptance -g acceptance -m 0771 /home/acceptance/.config
setup_controller() {
 /usr/bin/hlcp-controller setup --listen 127.0.0.1:19443 --server-name 127.0.0.1 --enrollment-listen 127.0.0.1:19445 --enrollment-url https://127.0.0.1:19445 --cli-user acceptance --json "$@"
}
if setup_controller --apply --yes >/root/refused.json; then echo 'Implicit group trust accepted' >&2; exit 1; fi
grep 'GROUP_WRITABLE_PARENT' /root/refused.json
grep '/home/acceptance/.config' /root/refused.json
test ! -e /root/hlcp-root-backup.json
test ! -e /etc/hlcp/controller/managed-bootstrap.json
test ! -e /var/lib/hlcp-controller/managed/authority.json
test ! -e /home/acceptance/.config/hlcp
test "$(stat -c %a /home/acceptance/.config)" = 771

chmod 0777 /home/acceptance/.config
if setup_controller --trust-user-group --apply --yes >/root/refused.json; then echo 'World write accepted' >&2; exit 1; fi
grep 'WORLD_WRITABLE_PARENT' /root/refused.json
test ! -e /root/hlcp-root-backup.json
chmod 0771 /home/acceptance/.config

printf '%s\n' 'disposable preflight password 123456' | setup_controller --trust-user-group --backup-password-stdin --backup-confirmed --apply --yes
test "$(stat -c %a /home/acceptance/.config)" = 771
test "$(stat -c %a /home/acceptance/.config/hlcp)" = 700
test "$(stat -c %a /home/acceptance/.config/hlcp/cli.token)" = 600
test "$(stat -c %U /home/acceptance/.config/hlcp/cli.token)" = acceptance
before=$(sha256sum /etc/hlcp/controller/managed-bootstrap.json /var/lib/hlcp-controller/managed/authority.json /var/lib/hlcp-controller/managed/nodes-managed.json /home/acceptance/.config/hlcp/cli.token /root/hlcp-root-backup.json)

# No password, repeated listener or CLI flags: saved choices finish idempotently.
/usr/bin/hlcp-controller setup --apply --yes --json </dev/null
test "$before" = "$(sha256sum /etc/hlcp/controller/managed-bootstrap.json /var/lib/hlcp-controller/managed/authority.json /var/lib/hlcp-controller/managed/nodes-managed.json /home/acceptance/.config/hlcp/cli.token /root/hlcp-root-backup.json)"

# A retained trust choice never permits world-write; failure retains identity.
chmod 0777 /home/acceptance/.config
if /usr/bin/hlcp-controller setup --apply --yes --json >/root/refused.json; then exit 1; fi
grep 'WORLD_WRITABLE_PARENT' /root/refused.json
test "$before" = "$(sha256sum /etc/hlcp/controller/managed-bootstrap.json /var/lib/hlcp-controller/managed/authority.json /var/lib/hlcp-controller/managed/nodes-managed.json /home/acceptance/.config/hlcp/cli.token /root/hlcp-root-backup.json)"
/usr/bin/hlcp-controller setup --skip-client --apply --yes --json
chmod 0771 /home/acceptance/.config

# Simulate an already configured preview.6 that has no saved setup request.
# Removing this fixture metadata is NEVER an instruction for a real VM.
rm -- /etc/hlcp/controller/managed-setup-request.json
/usr/bin/hlcp-controller setup --cli-user acceptance --trust-user-group --apply --yes --json </dev/null
test "$before" = "$(sha256sum /etc/hlcp/controller/managed-bootstrap.json /var/lib/hlcp-controller/managed/authority.json /var/lib/hlcp-controller/managed/nodes-managed.json /home/acceptance/.config/hlcp/cli.token /root/hlcp-root-backup.json)"
echo 'Preflight before CA, explicit group trust without chmod, saved resume/skip and preview.6 completion preserved identity/admission/token. Live systemd/reboot NOT RUN.'
