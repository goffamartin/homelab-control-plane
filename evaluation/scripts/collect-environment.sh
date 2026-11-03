#!/bin/sh
# Read-only inventory. Run as the normal HLCP CLI/resource owner, not through sudo.
set -eu
umask 077
role=${1:-}
case "$role" in controller|agent) ;; *) echo 'Usage: sh collect-environment.sh controller|agent' >&2; exit 2 ;; esac
[ "$(uname -s)" = Linux ] || { echo 'Run on the Debian VM.' >&2; exit 2; }
printf 'collected_at_utc=%s\nrole=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$role"
hostname
id -un
cat /etc/os-release
uname -srmo
printf 'online_cpus='; getconf _NPROCESSORS_ONLN
LC_ALL=C lscpu | sed -n '/^Architecture:/p; /^CPU(s):/p; /^Model name:/p; /^Hypervisor vendor:/p'
free -b
uptime
df -h / /var/lib
if command -v ip >/dev/null 2>&1; then ip -4 route; fi
python3 --version
ssh -V 2>&1
if [ "$role" = controller ]; then
  hlcp-controller --version
  systemctl show hlcp-controller.service -p ActiveState -p MainPID -p ControlGroup
  hlcp -n --json
else
  hlcp-agent --version
  podman --version
  systemctl --user show hlcp-agent.service -p ActiveState -p MainPID -p ControlGroup
  podman ps -a --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}'
fi
# Deliberately exclude environment variables, YAML, token files, private keys,
# systemd unit bodies, process command lines, application logs and authentication data.
