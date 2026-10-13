package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Core agent package installs system-owned binaries/templates only. It never
// chooses a container owner, generates credentials or touches a user's state.
func packageAgentDeb(out, version, revision, arch string) (artifact, error) {
	if arch != "amd64" && arch != "arm64" {
		return artifact{}, fmt.Errorf("unsupported Debian architecture")
	}
	packageVersion := debianVersion(version, revision)
	if !debVersionPattern.MatchString(packageVersion) {
		return artifact{}, fmt.Errorf("invalid Debian version")
	}
	var data []debEntry
	for _, dir := range []string{"usr/", "usr/bin/", "usr/lib/", "usr/lib/systemd/", "usr/lib/systemd/user/", "usr/lib/systemd/system/", "usr/share/", "usr/share/hlcp-agent/", "usr/share/doc/", "usr/share/doc/hlcp-agent/"} {
		data = append(data, debEntry{dir, 0755, nil})
	}
	binary, err := debSource(filepath.Join(out, "hlcp-agent-linux-"+arch), false)
	if err != nil {
		return artifact{}, err
	}
	data = append(data, debEntry{"usr/bin/hlcp-agent", 0755, binary})
	for _, profile := range []struct{ source, target string }{
		{"deploy/systemd/user/hlcp-agent.service", "usr/lib/systemd/user/hlcp-agent.service"},
		{"deploy/systemd/user/hlcp-agent-podman.service", "usr/lib/systemd/user/hlcp-agent-podman.service"},
		{"deploy/systemd/hlcp-agent.service", "usr/lib/systemd/system/hlcp-agent.service"},
	} {
		unit, err := debSource(profile.source, true)
		if err != nil {
			return artifact{}, err
		}
		unit = bytes.ReplaceAll(unit, []byte("%h/.local/bin/hlcp-agent"), []byte("/usr/bin/hlcp-agent"))
		unit = bytes.ReplaceAll(unit, []byte("/usr/local/bin/hlcp-agent"), []byte("/usr/bin/hlcp-agent"))
		data = append(data, debEntry{profile.target, 0644, unit})
	}
	for _, source := range []struct{ path, target string }{
		{"deploy/config/agent-podman.yaml", "usr/share/hlcp-agent/agent-podman.yaml"},
		{"deploy/config/agent-docker.yaml", "usr/share/hlcp-agent/agent-docker.yaml"},
		{"deploy/config/agent-user.yaml", "usr/share/hlcp-agent/agent-user.yaml"},
		{"deploy/config/agent-system.yaml", "usr/share/hlcp-agent/agent-system.yaml"},
		{"deploy/systemd/hlcp-agent-docker.conf", "usr/share/hlcp-agent/hlcp-agent-docker.conf"},
		{"docs/14-cli-setup-and-agent-installation.md", "usr/share/doc/hlcp-agent/14-cli-setup-and-agent-installation.md"},
		{"docs/17-generic-agent-profiles.md", "usr/share/doc/hlcp-agent/17-generic-agent-profiles.md"},
		{"docs/15-onboarding-and-agent-profile-design-notes.md", "usr/share/doc/hlcp-agent/15-onboarding-and-agent-profile-design-notes.md"},
		{"docs/16-plug-and-play-implementation-plan.md", "usr/share/doc/hlcp-agent/16-plug-and-play-implementation-plan.md"},
	} {
		contents, err := debSource(source.path, true)
		if err != nil {
			return artifact{}, err
		}
		data = append(data, debEntry{source.target, 0644, contents})
	}
	installedSize := 0
	for _, entry := range data {
		installedSize += (len(entry.data) + 1023) / 1024
	}
	controlText := fmt.Sprintf("Package: hlcp-agent\nVersion: %s\nArchitecture: %s\nMaintainer: Martin Goffa <goffamartin@gmail.com>\nSection: admin\nPriority: optional\nInstalled-Size: %d\nDepends: systemd (>= 252)\nSuggests: dbus-user-session\nHomepage: https://github.com/goffamartin/homelab-control-plane\nDescription: HomeLab runtime-independent agent\n Native mTLS agent with opt-in system and user units; no automatic startup.\n", packageVersion, arch, installedSize)
	filename := "hlcp-agent_" + strings.ReplaceAll(packageVersion, "~", "-") + "_" + arch + ".deb"
	preinst, err := debSource("deploy/debian/agent/preinst", true)
	if err != nil {
		return artifact{}, err
	}
	file, err := os.OpenFile(filepath.Join(out, filename), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return artifact{}, err
	}
	err = writeDeb(file, []debEntry{{"control", 0644, []byte(controlText)}, {"preinst", 0755, preinst}}, data)
	closeErr := file.Close()
	if err != nil {
		return artifact{}, err
	}
	if closeErr != nil {
		return artifact{}, closeErr
	}
	digest, err := checksum(filepath.Join(out, filename))
	return artifact{filename, digest}, err
}
