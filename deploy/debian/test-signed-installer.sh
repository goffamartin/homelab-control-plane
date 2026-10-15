#!/bin/sh
# Only a newly created disposable non-booted container, never a user's VM.
set -eu
if [ "${HLCP_PACKAGE_TEST:-}" != disposable-container ]||[ ! -f /.dockerenv ]||[ -d /run/systemd/system ];then exit 1;fi
source=$1
artifacts=$2
version=$3
helper="$source/deploy/install/install.sh"
sh "$helper" controller --version "$version" --from-directory "$artifacts"
test ! -e /usr/bin/hlcp-controller
sh "$helper" controller --version "$version" --from-directory "$artifacts" --apply --yes
printf '%s\n' 'disposable signing test password'|hlcp-controller setup --listen 127.0.0.1:19443 --server-name 127.0.0.1 --enrollment-listen 127.0.0.1:19445 --enrollment-url https://127.0.0.1:19445 --backup-password-stdin --backup-confirmed --apply --yes
before=$(find /etc/hlcp/controller /var/lib/hlcp-controller -type f ! -name '.managed.lock' -exec sha256sum '{}' +|sort)
sh "$helper" controller --version "$version" --from-directory "$artifacts" --update --offline-stopped --apply --yes
after=$(find /etc/hlcp/controller /var/lib/hlcp-controller -type f ! -name '.managed.lock' -exec sha256sum '{}' +|sort)
test "$before" = "$after"
test "$(find /var/backups/hlcp -name '*.json' -type f |wc -l)" -eq 1
test "$(stat -c %a /var/backups/hlcp)" = 700
for problem in unsigned tamper wrong-version missing bad-manifest;do
 fixture=$(mktemp -d)
 cp "$artifacts"/SHA256SUMS "$artifacts"/SHA256SUMS.sig "$artifacts"/build-manifest.json "$artifacts"/build-manifest.json.sig "$artifacts"/hlcp-linux-amd64 "$artifacts"/hlcp-controller_*_amd64.deb "$fixture/"
 case "$problem" in
 unsigned) rm "$fixture/SHA256SUMS.sig";;
 tamper) printf 'corruption' >>"$fixture/hlcp-linux-amd64";;
 wrong-version) selected=v99.0.0;;
 missing) rm "$fixture"/hlcp-controller_*_amd64.deb;;
 bad-manifest) printf 'corruption' >>"$fixture/build-manifest.json.sig";;
 esac
 selected=${selected:-$version}
 if sh "$helper" controller --version "$selected" --from-directory "$fixture" --update;then echo "Accepted $problem" >&2;exit 1;fi
 unset selected
done
echo 'Signed installer preview/install/offline update/private backup/preservation/refusal acceptance passed. No live systemd/reboot claim.'
