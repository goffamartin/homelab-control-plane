#!/bin/sh
# Download this reviewed, commit-pinned helper first; never curl-pipe to root.
set -eu
umask 077
PATH=/usr/bin:/bin
export PATH
unset GH_DEBUG GH_FORCE_TTY
GH_HOST=github.com
GH_PROMPT_DISABLED=1
export GH_HOST GH_PROMPT_DISABLED
usage() {
 echo 'Usage: sh install.sh controller|agent --version vX.Y.Z [--public|--from-directory /artifacts] [--scope system|user] [--update] [--restart] [--configure] [--apply --yes]'
 echo 'Default verifies/downloads only. Private GitHub uses your existing gh login, never a PAT argument. Update snapshots config/state and never restores an old journal.'
 echo 'Advanced: --unit hlcp-agent-podman.service (user compatibility profile); --offline-stopped only for non-booted image maintenance, never live restart.'
}
fail(){ echo "HLCP installation refused: $*" >&2; exit 1; }
role=${1:-}
case "$role" in controller|agent) shift;; --help|-h) usage;exit 0;; *) usage;exit 2;;esac
version=;public=no;source_dir=;scope=system;update=no;restart=no;configure=no;apply=no;yes=no;offline=no;unit=
while [ "$#" -gt 0 ];do
 case "$1" in
  --version|--from-directory|--scope|--unit) [ "$#" -ge 2 ]||fail 'missing option value';key=$1;shift;case "$key" in --version) version=$1;;--from-directory) source_dir=$1;;--scope) scope=$1;;--unit) unit=$1;;esac;;
  --public) public=yes;;--update) update=yes;;--restart) restart=yes;;--configure) configure=yes;;--offline-stopped) offline=yes;;--apply) apply=yes;;--yes) yes=yes;;--help|-h) usage;exit 0;;*) fail 'unknown option';;
 esac;shift
done
[ "$apply" = "$yes" ]||fail '--apply and --yes are both required for changes'
[ "$scope" = system ]||[ "$scope" = user ]||fail 'invalid scope'
[ "$role" != controller ]||[ "$scope" = system ]||fail 'controller requires system scope'
[ "$restart" = no ]||[ "$update" = yes ]||fail '--restart is only for updates; use --configure on a fresh install'
[ "$configure" = no ]||[ "$update" = no ]||fail 'update never reruns provisioning'
if [ -z "$unit" ];then unit="hlcp-$role.service";fi
case "$role:$scope:$unit" in controller:system:hlcp-controller.service|agent:system:hlcp-agent.service|agent:user:hlcp-agent.service|agent:user:hlcp-agent-podman.service);;*)fail 'unsupported service profile';;esac
if [ "$offline" = yes ];then
 [ "$update" = yes ]&&[ "$restart" = no ]&&[ ! -d /run/systemd/system ]&&[ "$(cat /proc/1/comm)" != systemd ]||fail '--offline-stopped is only for explicit non-booted image maintenance, never live restart'
fi
printf '%s' "$version"|grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.]+)?$'||fail 'an exact numeric release version is required; latest is not accepted'
[ "$(uname -s)" = Linux ]||fail 'Debian Linux required'
command -v dpkg >/dev/null||fail 'Debian dpkg required'
arch=$(dpkg --print-architecture)
case "$arch" in amd64|arm64);;*)fail 'unsupported architecture';;esac
for dependency in openssl sha256sum awk mktemp dpkg-deb;do command -v "$dependency" >/dev/null||fail "install prerequisite: $dependency";done
stage=$(mktemp -d /tmp/hlcp-install.XXXXXXXX)
chmod 700 "$stage"
# Deliberately retain this bounded staging/recovery evidence; no recursive delete.
echo "Private download staging: $stage"
success=no
cleanup(){
 if [ "$success" = yes ];then
  case "$stage" in /tmp/hlcp-install.*)
   for file in release.pub SHA256SUMS SHA256SUMS.sig build-manifest.json build-manifest.json.sig "hlcp-linux-$arch" "hlcp-${role}_${version#v}_${arch}.deb";do rm -f -- "$stage/$file";done
   rmdir -- "$stage"||true;;
  esac
 else echo "Inspection files retained privately: $stage" >&2;fi
}
trap cleanup EXIT
cat >"$stage/release.pub" <<'HLCP_RELEASE_TRUST'
-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAOw51flSK7qPQF9bSBlzqYTGqpwAl+ydiSJQ7fUD0PUw=
-----END PUBLIC KEY-----
HLCP_RELEASE_TRUST
fetch(){
 name=$1
 if [ -n "$source_dir" ];then
  case "$source_dir" in /*);;*)fail '--from-directory must be absolute';;esac
  [ -f "$source_dir/$name" ]&&[ ! -L "$source_dir/$name" ]||fail "missing local artifact: $name"
  cp -- "$source_dir/$name" "$stage/$name"
 elif [ "$public" = yes ];then
  command -v curl >/dev/null||fail 'curl required for public downloads'
  curl --disable --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --silent --show-error --location --max-redirs 3 --connect-timeout 10 --max-time 120 --max-filesize 67108864 --output "$stage/$name" "https://github.com/goffamartin/homelab-control-plane/releases/download/$version/$name"||fail 'download denied or unavailable'
 else
  command -v gh >/dev/null||fail 'install gh and run gh auth login as yourself for private downloads'
  gh release download "$version" --repo goffamartin/homelab-control-plane --pattern "$name" --dir "$stage"||fail 'private release unavailable or access denied'
 fi
 [ "$(wc -c <"$stage/$name")" -le 67108864 ]||fail 'artifact size limit'
}
fetch SHA256SUMS
fetch SHA256SUMS.sig
[ "$(wc -c <"$stage/SHA256SUMS")" -le 1048576 ]||fail 'checksum metadata too large'
[ "$(wc -c <"$stage/SHA256SUMS.sig")" -eq 64 ]||fail 'invalid signature size'
openssl pkeyutl -verify -pubin -inkey "$stage/release.pub" -rawin -in "$stage/SHA256SUMS" -sigfile "$stage/SHA256SUMS.sig" >/dev/null 2>&1||fail 'release signature invalid; no binaries executed'
verify_file(){
 name=$1
 expected=$(awk -v name="$name" '$2==name {if(length($1)!=64||$1~/[^0-9a-f]/||NF!=2) exit 1;print $1;n++} END {if(n!=1)exit 1}' "$stage/SHA256SUMS")||fail "missing or duplicate signed artifact: $name"
 actual=$(sha256sum "$stage/$name");actual=${actual%% *}
 [ "$actual" = "$expected" ]||fail "checksum mismatch: $name"
}
package="hlcp-${role}_${version#v}_${arch}.deb"
for name in build-manifest.json "hlcp-linux-$arch" "$package";do fetch "$name";verify_file "$name";done
fetch build-manifest.json.sig
chmod 700 "$stage/hlcp-linux-$arch"
verified=$("$stage/hlcp-linux-$arch" verify-release --directory "$stage" --version "$version" --role "$role" --arch "$arch")||fail 'manifest verification failed'
[ "$verified" = "$package" ]||fail 'unexpected package name'
[ "$(dpkg-deb -f "$stage/$package" Package)" = "hlcp-$role" ]||fail 'package role mismatch'
[ "$(dpkg-deb -f "$stage/$package" Architecture)" = "$arch" ]||fail 'package architecture mismatch'
target_version=$(printf '%s' "${version#v}"|tr '-' '~')
[ "$(dpkg-deb -f "$stage/$package" Version)" = "$target_version" ]||fail 'package version mismatch'
installed=$(dpkg-query -W -f='${Version}' "hlcp-$role" 2>/dev/null||true)
if [ -n "$installed" ];then
 dpkg --compare-versions "$target_version" ge "$installed"||fail 'downgrade refused; use a separately reviewed compatible rollback'
 [ "$update" = yes ]||fail 'package already exists; use explicit --update'
fi
echo "Verified hlcp-$role: ${installed:-not installed} -> $target_version ($arch, scope=$scope, protocol=1, managed=1, journal=3, ledger=1)."
if [ "$apply" = no ];then echo 'Read-only verification complete. No package, service or configuration changed.';success=yes;exit 0;fi
root(){ if [ "$(id -u)" -eq 0 ];then "$@";else sudo -- "$@";fi; }
systemctl_scope(){ if [ "$scope" = user ];then [ "$(id -u)" -ne 0 ]||fail 'run user-scope installation as the real resource owner, not root';systemctl --user --no-ask-password "$@";else root systemctl --system --no-ask-password "$@";fi; }
if [ "$update" = yes ]&&[ -n "$installed" ];then
 if [ "$offline" = yes ];then active=inactive;else active=$(systemctl_scope show "$unit" --property=ActiveState --value)||fail 'cannot inspect service';fi
 case "$active" in active) [ "$restart" = yes ]||fail 'stop service first or explicitly request --restart';systemctl_scope stop "$unit"||fail 'service stop uncertain';;inactive);;*)fail 'service must be active or inactive; inspect failed/transitioning unit';;esac
 [ "$offline" = yes ]||[ "$(systemctl_scope show "$unit" --property=ActiveState --value)" = inactive ]||fail 'service did not stop'
 stamp=$(date -u +%Y%m%dT%H%M%SZ)
 if [ "$scope" = user ];then
  account_home=$(getent passwd "$(id -u)"|cut -d: -f6)
  case "$account_home" in /*);;*)fail 'invalid account home';;esac
  backup="$account_home/.local/state/hlcp-backups/$role-$stamp-$$.json"
  "$stage/hlcp-linux-$arch" package-backup --role "$role" --scope user --output "$backup" --stopped-confirmed||fail 'state backup failed; no package installed'
 else
  backup="/var/backups/hlcp/$role-$stamp-$$.json"
  root "$stage/hlcp-linux-$arch" package-backup --role "$role" --scope system --output "$backup" --stopped-confirmed||fail 'state backup failed; no package installed'
 fi
fi
root apt-get install -y -o Dpkg::Options::=--force-confold "$stage/$package"||fail 'package install failed; inspect state and backup before retry'
if [ "$restart" = yes ];then
 systemctl_scope start "$unit"||fail 'start failed; new package/state retained for inspection'
 [ "$(systemctl_scope show "$unit" --property=ActiveState --value)" = active ]||fail 'service not active'
 echo 'Service started. Verify controller registration/inventory before issuing actions.'
fi
if [ "$configure" = yes ];then
 if [ "$role" = controller ];then root /usr/bin/hlcp-controller setup;elif [ "$scope" = user ];then /usr/bin/hlcp-agent join;else root /usr/bin/hlcp-agent join;fi
fi
echo 'Verified installation complete. No journal/ledger rollback or implicit resource privileges.'
success=yes
