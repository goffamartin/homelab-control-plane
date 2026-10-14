#!/bin/sh
# Read-only, fixed-role evidence. No credentials, config contents or logs.
if [ "$#" -eq 1 ] && [ "$1" = "--help" ]; then
  printf '%s\n' 'Usage: sh inspect-onboarding-host.sh agent|controller' \
    'Read-only version/systemd evidence on a booted Debian 13 host.' \
    'No start/enable/restart, secrets, configuration contents, logs or network requests.'
  exit 0
fi
if [ "$#" -ne 1 ]; then
  printf '%s\n' 'Select exactly agent or controller. Use --help.' >&2
  exit 2
fi
case "$1" in
  agent) unit=hlcp-agent.service; package=hlcp-agent; binary=/usr/bin/hlcp-agent ;;
  controller) unit=hlcp-controller.service; package=hlcp-controller; binary=/usr/bin/hlcp-controller ;;
  *) printf '%s\n' 'Invalid role. Use --help.' >&2; exit 2 ;;
esac
PATH=/usr/bin:/bin
LC_ALL=C
export PATH LC_ALL
init=
if [ -r /proc/1/comm ]; then read -r init < /proc/1/comm; fi
if [ "$init" != systemd ] || [ ! -d /run/systemd/system ]; then
  printf '%s\n' 'Booted local systemd required; a build container is not live acceptance.' >&2
  exit 1
fi
if ! /usr/bin/awk '/^VERSION_ID="13"$/ {found=1} END {exit !found}' /etc/os-release; then
  printf '%s\n' 'Debian 13 fixture required for this acceptance collector.' >&2
  exit 1
fi
if ! /usr/bin/awk '/^ID=debian$/ {found=1} END {exit !found}' /etc/os-release; then
  printf '%s\n' 'Debian fixture required.' >&2
  exit 1
fi
printf 'Role=%s\n' "$1"
/usr/bin/date -u '+ObservedAt=%Y-%m-%dT%H:%M:%SZ'
/usr/bin/uname -m
printf 'CallerUID='; /usr/bin/id -u
printf 'CallerGID='; /usr/bin/id -g
printf 'BootID='; /usr/bin/cat /proc/sys/kernel/random/boot_id
status=0
/usr/bin/timeout 5 /usr/bin/dpkg-query -W -f='${Package} ${Version}\n' "$package" || status=1
if [ -x "$binary" ]; then
  /usr/bin/timeout 5 "$binary" --version || status=1
else
  printf '%s\n' 'Installed role binary unavailable.' >&2
  status=1
fi
fields=LoadState,ActiveState,SubState,UnitFileState,FragmentPath,DropInPaths,NeedDaemonReload,MainPID,Result,User,Group
/usr/bin/timeout 5 /usr/bin/systemctl --system --no-pager --all show "$unit" --property="$fields" || status=1
if [ "$1" = agent ]; then
  # Missing probe may cause a nonzero systemctl exit; print properties but do
  # not interpret absence as cleanup success. Compare with actual probe report.
  /usr/bin/timeout 5 /usr/bin/systemctl --system --no-pager --all show \
    hlcp-agent-resource-probe.service --property=LoadState,ActiveState,SubState,MainPID || status=1
fi
printf '%s\n' 'Evidence only: does not establish sandbox success, registration, health or reboot acceptance.'
exit "$status"
