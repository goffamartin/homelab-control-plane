#!/bin/sh
# Package lifecycle fixture; never run against a booted host/user manager.
set -eu
if [ "${HLCP_PACKAGE_TEST:-}" != "disposable-container" ] || [ ! -f /.dockerenv ] || [ -d /run/systemd/system ]; then
  echo "Requires a fresh marked non-booted disposable container." >&2
  exit 1
fi
package=$1
arm=$2
test "$(dpkg-deb -f "$package" Architecture)" = amd64
test "$(dpkg-deb -f "$arm" Architecture)" = arm64
useradd -m -u 1000 martin
install -d -o martin -g martin -m 0700 /home/martin/.config/hlcp /home/martin/.local/state/hlcp
install -o martin -g martin -m 0600 /bin/true /home/martin/.config/hlcp/agent.key
install -o martin -g martin -m 0600 /bin/true /home/martin/.local/state/hlcp/command-journal.json
key_hash=$(sha256sum /home/martin/.config/hlcp/agent.key)
journal_hash=$(sha256sum /home/martin/.local/state/hlcp/command-journal.json)
apt-get install -y "$package"
/usr/bin/hlcp-agent --version
test -f /usr/lib/systemd/user/hlcp-agent-podman.service
test ! -e /etc/systemd/system/hlcp-agent.service
test ! -e /etc/systemd/user/default.target.wants/hlcp-agent-podman.service
test ! -e /home/martin/.config/systemd/user/default.target.wants/hlcp-agent-podman.service
test ! -e /home/martin/.config/hlcp/agent.yaml
grep -q 'ExecStart=/usr/bin/hlcp-agent' /usr/lib/systemd/user/hlcp-agent-podman.service
systemd-analyze verify /usr/lib/systemd/user/hlcp-agent-podman.service
dpkg --remove hlcp-agent
test ! -e /usr/bin/hlcp-agent
test "$key_hash" = "$(sha256sum /home/martin/.config/hlcp/agent.key)"
test "$journal_hash" = "$(sha256sum /home/martin/.local/state/hlcp/command-journal.json)"
apt-get install -y "$package"
dpkg --purge hlcp-agent
test "$key_hash" = "$(sha256sum /home/martin/.config/hlcp/agent.key)"
test "$journal_hash" = "$(sha256sum /home/martin/.local/state/hlcp/command-journal.json)"
echo "Agent package dependency/install/reinstall/remove/purge and private-state preservation checks passed (not booted user-service evidence)."
