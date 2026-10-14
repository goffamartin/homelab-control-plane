#!/bin/sh
# ONLY a fresh disposable container, never a VM or host install.
set -eu
if [ "${HLCP_PACKAGE_TEST:-}" != "disposable-container" ] || [ ! -f /.dockerenv ] || [ -d /run/systemd/system ]; then
  echo "Disposable non-booted Docker container required." >&2
  exit 1
fi
package=$1
apt-get install -y "$package"
test "$(stat -c %a /etc/hlcp/controller)" = 700
for name in controller.env nodes.json api-policy.json controller.key api.token observer.token; do
  test ! -e "/etc/hlcp/controller/$name"
done
fixture=$(mktemp -d)
chmod 700 "$fixture"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
  -keyout "$fixture/ca.key" -out "$fixture/ca.crt" -days 3 -subj /CN=Package-Test-CA \
  -addext 'basicConstraints=critical,CA:TRUE' -addext 'keyUsage=critical,keyCertSign,cRLSign'
openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
  -keyout "$fixture/controller.key" -out "$fixture/controller.csr" -subj /CN=controller.test
# Extension fixture is test data, not production configuration.
printf '%s\n' 'basicConstraints=critical,CA:FALSE' 'keyUsage=critical,digitalSignature' \
  'extendedKeyUsage=serverAuth' 'subjectAltName=DNS:controller.test' > "$fixture/server.ext"
openssl x509 -req -in "$fixture/controller.csr" -CA "$fixture/ca.crt" \
  -CAkey "$fixture/ca.key" -CAcreateserial -days 2 -out "$fixture/controller.crt" -extfile "$fixture/server.ext"
chmod 600 "$fixture"/*
setup_controller() {
  /usr/bin/hlcp-controller setup --listen 192.0.2.44:9443 --server-name controller.test \
    --cert "$fixture/controller.crt" --key "$fixture/controller.key" \
    --issuer-ca "$fixture/ca.crt" --peer-ca "$fixture/ca.crt" "$@"
}
setup_controller --json
test ! -e /etc/hlcp/controller/controller.env
test ! -e /etc/hlcp/controller/api.token
setup_controller --apply --yes --json
for name in controller.crt controller.key ca.crt issuer-ca.crt controller.env api-policy.json api.token observer.token nodes.json; do
  test "$(stat -c %a "/etc/hlcp/controller/$name")" = 600
done
test "$(cat /etc/hlcp/controller/nodes.json)" = "[]"
before=$(find /etc/hlcp/controller -maxdepth 1 -type f ! -name '.hlcp-*' -exec sha256sum '{}' + | sort)
setup_controller --apply --yes --json
test "$before" = "$(find /etc/hlcp/controller -maxdepth 1 -type f ! -name '.hlcp-*' -exec sha256sum '{}' + | sort)"
/usr/bin/hlcp-controller -check-config \
  -cert /etc/hlcp/controller/controller.crt -key /etc/hlcp/controller/controller.key \
  -ca /etc/hlcp/controller/ca.crt -api-policy-file /etc/hlcp/controller/api-policy.json \
  -node-allowlist-file /etc/hlcp/controller/nodes.json
test ! -e /var/lib/hlcp-controller/controller-operations.json
# A deliberately changed deployment choice remains a conflict, never a repair.
sed -i 's/192.0.2.44/192.0.2.45/' /etc/hlcp/controller/controller.env
changed=$(sha256sum /etc/hlcp/controller/controller.env)
if setup_controller --apply --yes --json; then echo "Existing config was replaced" >&2; exit 1; fi
test "$changed" = "$(sha256sum /etc/hlcp/controller/controller.env)"
echo "Fresh package setup/preview/repeat/conflict passed; no live service or reboot claim."
