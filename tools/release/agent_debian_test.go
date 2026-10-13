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
	for _, path := range []string{"deploy/systemd/user/hlcp-agent-podman.service", "deploy/config/agent-podman.yaml", "docs/14-cli-setup-and-agent-installation.md"} {
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
		if len(control) != 1 || len(payload) != 13 {
			t.Fatal("unexpected package files / maintainer scripts")
		}
		if !strings.Contains(string(control["control"].data), "podman, uidmap, dbus-user-session") {
			t.Fatal("missing dependencies")
		}
		unit := string(payload["usr/lib/systemd/user/hlcp-agent-podman.service"].data)
		if strings.Contains(unit, ".local/bin") || !strings.Contains(unit, "ExecStart=/usr/bin/hlcp-agent") || strings.Contains(unit, "NoNewPrivileges=true") {
			t.Fatal("invalid rootless unit")
		}
		if payload["usr/bin/hlcp-agent"].mode != 0755 {
			t.Fatal("invalid executable mode")
		}
		if _, err := packageAgentDeb(dir, "v0.1.0-preview.3", "abc123", "amd64"); err == nil {
			t.Fatal("overwrote package")
		}
	}
	if _, err := packageAgentDeb("first", "v0.1.0", "abc123", "bad"); err == nil {
		t.Fatal("accepted unsupported architecture")
	}
}
