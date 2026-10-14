#!/bin/sh
# Destructive package lifecycle test ONLY inside a fresh disposable Docker container.
set -eu
if [ "${HLCP_PACKAGE_TEST:-}" != "disposable-container" ] || [ ! -f /.dockerenv ] || [ -d /run/systemd/system ]; then
  echo "Run only in a fresh non-booted disposable Docker container with HLCP_PACKAGE_TEST=disposable-container." >&2
  exit 1
fi
first=$1
upgrade=$2
arm=$3
test "$(dpkg-deb -f "$first" Architecture)" = amd64
test "$(dpkg-deb -f "$arm" Architecture)" = arm64
dpkg-deb --info "$first"
dpkg-deb --contents "$first"
scripts=$(mktemp -d)
dpkg-deb -e "$first" "$scripts"
for script in preinst postinst prerm postrm; do sh -n "$scripts/$script"; done

# A manual unit, including a mask symlink, must not be silently overridden.
ln -s /dev/null /etc/systemd/system/hlcp-controller.service
if dpkg -i "$first"; then echo "Accepted manual unit conflict" >&2; exit 1; fi
test "$(readlink /etc/systemd/system/hlcp-controller.service)" = /dev/null
rm /etc/systemd/system/hlcp-controller.service
apt-get install -y "$first"
test "$(stat -c %a /etc/hlcp/controller)" = 700
test ! -e /etc/hlcp/controller/controller.env
test ! -e /etc/hlcp/controller/nodes.json
test ! -e /etc/hlcp/controller/api-policy.json
test "$(cat /usr/share/hlcp-controller/nodes.json)" = "[]"
test "$(id -u hlcp-controller)" != 0
test ! -e /var/lib/hlcp-controller/controller-operations.json
test ! -e /etc/hlcp/controller/controller.key
test ! -e /etc/hlcp/controller/api.token
if systemctl is-enabled hlcp-controller.service; then echo "Service auto-enabled" >&2; exit 1; fi
systemd-analyze verify hlcp-controller.service
/usr/bin/hlcp-controller --version
/usr/bin/hlcp --version
uid=$(id -u hlcp-controller)

# Fixtures are deliberately inert data, not real credentials or a valid ledger.
install -d -o hlcp-controller -g hlcp-controller -m 0700 /var/lib/hlcp-controller
install -o hlcp-controller -g hlcp-controller -m 0600 /bin/true /var/lib/hlcp-controller/controller-operations.json
install -o root -g root -m 0600 /bin/true /etc/hlcp/controller/controller.key
ledger_hash=$(sha256sum /var/lib/hlcp-controller/controller-operations.json)
key_hash=$(sha256sum /etc/hlcp/controller/controller.key)
install -o root -g root -m 0600 /usr/share/hlcp-controller/controller.env /etc/hlcp/controller/controller.env
sed -i 's/127.0.0.1/192.0.2.44/' /etc/hlcp/controller/controller.env
config_hash=$(sha256sum /etc/hlcp/controller/controller.env)
apt-get install -y "$upgrade"
test "$uid" = "$(id -u hlcp-controller)"
test "$config_hash" = "$(sha256sum /etc/hlcp/controller/controller.env)"
test "$ledger_hash" = "$(sha256sum /var/lib/hlcp-controller/controller-operations.json)"
test "$key_hash" = "$(sha256sum /etc/hlcp/controller/controller.key)"
test "$(dpkg-query -W -f='${Version}' hlcp-controller)" = "$(dpkg-deb -f "$upgrade" Version)"
/usr/bin/hlcp-controller --version
dpkg -i "$first"
test "$config_hash" = "$(sha256sum /etc/hlcp/controller/controller.env)"
test "$ledger_hash" = "$(sha256sum /var/lib/hlcp-controller/controller-operations.json)"
dpkg --remove hlcp-controller
test ! -e /usr/bin/hlcp-controller
test -f /etc/hlcp/controller/controller.env
dpkg --purge hlcp-controller
test "$config_hash" = "$(sha256sum /etc/hlcp/controller/controller.env)"
test "$ledger_hash" = "$(sha256sum /var/lib/hlcp-controller/controller-operations.json)"
test "$key_hash" = "$(sha256sum /etc/hlcp/controller/controller.key)"
test "$uid" = "$(id -u hlcp-controller)"
echo "Package install/upgrade/downgrade/remove/purge checks passed; no booted-systemd claim."
