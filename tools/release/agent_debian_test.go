package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentPackageContentsAndReproducibility(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"deploy/systemd/user/hlcp-agent-podman.service", "deploy/systemd/user/hlcp-agent.service", "deploy/systemd/hlcp-agent.service", "deploy/systemd/hlcp-agent-docker.conf", "deploy/debian/agent/preinst", "deploy/config/agent-podman.yaml", "deploy/config/agent-docker.yaml", "deploy/config/agent-user.yaml", "deploy/config/agent-system.yaml", "docs/14-cli-setup-and-agent-installation.md", "docs/17-generic-agent-profiles.md", "docs/15-onboarding-and-agent-profile-design-notes.md", "docs/16-plug-and-play-implementation-plan.md"} {
		data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	var first []byte
	for _, dir := range []string{"first", "second"} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "hlcp-agent-linux-amd64"), []byte("binary"), 0600); err != nil {
			t.Fatal(err)
		}
		artifact, err := packageAgentDeb(dir, "v0.1.0-preview.3", "abc123", "amd64")
		if err != nil {
			t.Fatal(err)
		}
		if artifact.Name != "hlcp-agent_0.1.0-preview.3_amd64.deb" {
			t.Fatal("wrong filename")
		}
		data, err := os.ReadFile(filepath.Join(dir, artifact.Name))
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = data
		} else if !bytes.Equal(first, data) {
			t.Fatal("non-reproducible package")
		}
		members := readDebMembers(t, data)
		control := readDebTar(t, members["control.tar.gz"])
		payload := readDebTar(t, members["data.tar.gz"])
		if len(control) != 2 || len(payload) != 23 {
			t.Fatal("unexpected package files / maintainer scripts")
		}
		if !strings.Contains(string(control["control"].data), "Depends: systemd (>= 252)\n") || strings.Contains(string(control["control"].data), "podman") || strings.Contains(string(control["control"].data), "docker") || strings.Contains(string(control["control"].data), "uidmap") {
			t.Fatal("core package depends on a container runtime")
		}
		if control["preinst"].mode != 0755 || bytes.Contains(control["preinst"].data, []byte("\r")) {
			t.Fatal("invalid preinst script")
		}
		unit := string(payload["usr/lib/systemd/user/hlcp-agent-podman.service"].data)
		if strings.Contains(unit, ".local/bin") || !strings.Contains(unit, "ExecStart=/usr/bin/hlcp-agent") || strings.Contains(unit, "NoNewPrivileges=true") {
			t.Fatal("invalid rootless unit")
		}
		if payload["usr/bin/hlcp-agent"].mode != 0755 {
			t.Fatal("invalid executable mode")
		}
		generic := string(payload["usr/lib/systemd/user/hlcp-agent.service"].data)
		if !strings.Contains(generic, "Conflicts=hlcp-agent-podman.service") || !strings.Contains(generic, "ExecStart=/usr/bin/hlcp-agent") || strings.Contains(generic, "NoNewPrivileges=true") {
			t.Fatal("invalid generic user unit")
		}
		system := string(payload["usr/lib/systemd/system/hlcp-agent.service"].data)
		if !strings.Contains(system, "User=hlcp") || !strings.Contains(system, "ExecStart=/usr/bin/hlcp-agent") || !strings.Contains(system, "StateDirectory=hlcp") || !strings.Contains(system, "NoNewPrivileges=true") || strings.Contains(system, "/usr/local/bin") {
			t.Fatal("invalid generic system unit")
		}
		for path := range payload {
			if strings.HasPrefix(path, "etc/") || strings.HasPrefix(path, "home/") || strings.Contains(path, ".key") || path == "usr/bin/hlcp" {
				t.Fatal("packaged production config/secret or conflicting CLI")
			}
		}
		if _, err := packageAgentDeb(dir, "v0.1.0-preview.3", "abc123", "amd64"); err == nil {
			t.Fatal("overwrote package")
		}
	}
	if _, err := packageAgentDeb("first", "v0.1.0", "abc123", "bad"); err == nil {
		t.Fatal("accepted unsupported architecture")
	}
}
