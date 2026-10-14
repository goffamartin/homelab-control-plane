#!/bin/sh
# ONLY a fresh disposable container; synthetic legacy package metadata, not an old binary compatibility test.
set -eu
if [ "${HLCP_PACKAGE_TEST:-}" != "disposable-container" ] || [ ! -f /.dockerenv ] || [ -d /run/systemd/system ]; then
  echo "Disposable non-booted Docker container required." >&2
  exit 1
fi
package=$1
fixture=$(mktemp -d)
dpkg-deb -R "$package" "$fixture/legacy"
sed -i 's/^Version:.*/Version: 0.0.0~legacy-fixture/' "$fixture/legacy/DEBIAN/control"
for name in controller.env nodes.json api-policy.json; do
  install -m 0600 "$fixture/legacy/usr/share/hlcp-controller/$name" "$fixture/legacy/etc/hlcp/controller/$name"
  printf '/etc/hlcp/controller/%s\n' "$name" >> "$fixture/legacy/DEBIAN/conffiles"
done
dpkg-deb --build "$fixture/legacy" "$fixture/legacy.deb"
apt-get install -y "$fixture/legacy.deb"
sed -i 's/127.0.0.1/192.0.2.44/' /etc/hlcp/controller/controller.env
before=$(sha256sum /etc/hlcp/controller/controller.env /etc/hlcp/controller/nodes.json /etc/hlcp/controller/api-policy.json)
install -o root -g root -m 0600 /bin/true /etc/hlcp/controller/controller.key
key_hash=$(sha256sum /etc/hlcp/controller/controller.key)
apt-get install -y "$package"
test "$before" = "$(sha256sum /etc/hlcp/controller/controller.env /etc/hlcp/controller/nodes.json /etc/hlcp/controller/api-policy.json)"
test "$key_hash" = "$(sha256sum /etc/hlcp/controller/controller.key)"
# dpkg still remembers legacy conffiles as obsolete. Do not silently clear its database.
dpkg-query -W -f='${Conffiles}\n' hlcp-controller
dpkg --purge hlcp-controller
for name in controller.env nodes.json api-policy.json; do test ! -e "/etc/hlcp/controller/$name"; done
test "$key_hash" = "$(sha256sum /etc/hlcp/controller/controller.key)"
echo "Legacy conffiles survive upgrade; purge follows existing dpkg ownership. No binary rollback/reboot claim."
