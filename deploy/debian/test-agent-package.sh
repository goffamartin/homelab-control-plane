#!/bin/sh
# Package lifecycle fixture; never run against a booted host/user manager.
set -eu
if [ "${HLCP_PACKAGE_TEST:-}" != "disposable-container" ] || [ ! -f /.dockerenv ] || [ -d /run/systemd/system ]; then
  echo "Requires a fresh marked non-booted disposable container." >&2
  exit 1
fi
package=$1
arm=$2
legacy=${3:-}
test "$(dpkg-deb -f "$package" Architecture)" = amd64
test "$(dpkg-deb -f "$arm" Architecture)" = arm64
useradd -m -u 1000 martin
install -d -o martin -g martin -m 0700 /home/martin/.config/hlcp /home/martin/.local/state/hlcp
install -o martin -g martin -m 0600 /bin/true /home/martin/.config/hlcp/agent.key
install -o martin -g martin -m 0600 /bin/true /home/martin/.local/state/hlcp/command-journal.json
install -o martin -g martin -m 0600 /bin/true /home/martin/.config/hlcp/agent.yaml
key_hash=$(sha256sum /home/martin/.config/hlcp/agent.key)
journal_hash=$(sha256sum /home/martin/.local/state/hlcp/command-journal.json)
config_hash=$(sha256sum /home/martin/.config/hlcp/agent.yaml)
! command -v podman
! command -v docker
! command -v newuidmap
! getent passwd hlcp
# Refuse manual system-unit shadowing, including a mask, without touching it.
ln -s /dev/null /etc/systemd/system/hlcp-agent.service
if dpkg -i "$package"; then echo "Accepted manual system unit/mask" >&2; exit 1; fi
test "$(readlink /etc/systemd/system/hlcp-agent.service)" = /dev/null
rm /etc/systemd/system/hlcp-agent.service
apt-get install -y -qq "$package"
/usr/bin/hlcp-agent --version
! command -v podman
! command -v docker
! command -v newuidmap
! getent passwd hlcp
test -f /usr/lib/systemd/user/hlcp-agent-podman.service
test -f /usr/lib/systemd/user/hlcp-agent.service
test -f /usr/lib/systemd/system/hlcp-agent.service
test ! -e /etc/systemd/system/hlcp-agent.service
test ! -e /etc/systemd/user/default.target.wants/hlcp-agent-podman.service
test ! -e /home/martin/.config/systemd/user/default.target.wants/hlcp-agent-podman.service
test ! -e /etc/systemd/system/multi-user.target.wants/hlcp-agent.service
test ! -e /home/martin/.config/systemd/user/default.target.wants/hlcp-agent.service
test ! -e /etc/hlcp/agent
grep -q 'ExecStart=/usr/bin/hlcp-agent' /usr/lib/systemd/user/hlcp-agent-podman.service
systemd-analyze verify /usr/lib/systemd/user/hlcp-agent-podman.service
systemd-analyze verify /usr/lib/systemd/user/hlcp-agent.service
useradd --system --user-group --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin hlcp
systemd-analyze verify /usr/lib/systemd/system/hlcp-agent.service
scripts=$(mktemp -d)
dpkg-deb -e "$package" "$scripts"
sh -n "$scripts/preinst"
test ! -e "$scripts/postinst"
test ! -e "$scripts/prerm"
test ! -e "$scripts/postrm"
if [ -n "$legacy" ]; then
  # Downgrade only in this fresh container, then model an enabled old user unit.
  dpkg --remove hlcp-agent
  apt-get install -y -qq "$legacy"
	legacy_unit_hash=$(sha256sum /usr/lib/systemd/user/hlcp-agent-podman.service)
  install -d -o martin -g martin -m 0700 /home/martin/.config/systemd/user/default.target.wants
  ln -s /usr/lib/systemd/user/hlcp-agent-podman.service /home/martin/.config/systemd/user/default.target.wants/hlcp-agent-podman.service
  install -o martin -g martin -m 0600 /bin/true /home/martin/.config/systemd/user/hlcp-agent-podman.service
  override_hash=$(sha256sum /home/martin/.config/systemd/user/hlcp-agent-podman.service)
  apt-get install -y -qq "$package"
	test "$legacy_unit_hash" = "$(sha256sum /usr/lib/systemd/user/hlcp-agent-podman.service)"
  test "$(readlink /home/martin/.config/systemd/user/default.target.wants/hlcp-agent-podman.service)" = /usr/lib/systemd/user/hlcp-agent-podman.service
  test "$override_hash" = "$(sha256sum /home/martin/.config/systemd/user/hlcp-agent-podman.service)"
  test ! -e /home/martin/.config/systemd/user/default.target.wants/hlcp-agent.service
  test "$config_hash" = "$(sha256sum /home/martin/.config/hlcp/agent.yaml)"
  test "$key_hash" = "$(sha256sum /home/martin/.config/hlcp/agent.key)"
  test "$journal_hash" = "$(sha256sum /home/martin/.local/state/hlcp/command-journal.json)"
fi
dpkg --remove hlcp-agent
test ! -e /usr/bin/hlcp-agent
test "$key_hash" = "$(sha256sum /home/martin/.config/hlcp/agent.key)"
test "$journal_hash" = "$(sha256sum /home/martin/.local/state/hlcp/command-journal.json)"
apt-get install -y -qq "$package"
dpkg --purge hlcp-agent
test "$key_hash" = "$(sha256sum /home/martin/.config/hlcp/agent.key)"
test "$journal_hash" = "$(sha256sum /home/martin/.local/state/hlcp/command-journal.json)"
test "$config_hash" = "$(sha256sum /home/martin/.config/hlcp/agent.yaml)"
echo "Generic agent fresh no-runtime install, legacy upgrade, mask refusal, reinstall/remove/purge and private-state preservation checks passed (not booted service evidence)."
