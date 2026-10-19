package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDebianVersion(t *testing.T) {
	for _, test := range []struct{ label, revision, want string }{
		{"v0.1.0", "abc123", "0.1.0"},
		{"0.2.0", "abc123", "0.2.0"},
		{"v0.1.0-preview.1", "abc123", "0.1.0~preview.1"},
		{"dev-build", "abc123", "0~dev~build+gitabc123"},
		{"dev", "abc123-dirty", "0~dev+gitabc123+dirty"},
		{"v0.1.0", "abc123-dirty", "0.1.0~dirty+gitabc123"},
	} {
		got := debianVersion(test.label, test.revision)
		if got != test.want || !debVersionPattern.MatchString(got) {
			t.Fatalf("%s: %s", test.label, got)
		}
	}
}

func readDebMembers(t *testing.T, input []byte) map[string][]byte {
	t.Helper()
	if string(input[:8]) != "!<arch>\n" {
		t.Fatal("invalid ar magic")
	}
	members := map[string][]byte{}
	for offset := 8; offset < len(input); {
		if len(input)-offset < 60 {
			t.Fatal("truncated ar header")
		}
		header := input[offset : offset+60]
		if string(header[58:]) != "\x60\n" {
			t.Fatal("invalid ar header terminator")
		}
		size, err := strconv.Atoi(strings.TrimSpace(string(header[48:58])))
		if err != nil || size < 0 || size > len(input)-offset-60 {
			t.Fatal("invalid ar size")
		}
		name := strings.TrimSuffix(strings.TrimSpace(string(header[:16])), "/")
		if _, duplicate := members[name]; duplicate {
			t.Fatal("duplicate ar member")
		}
		offset += 60
		members[name] = input[offset : offset+size]
		offset += size
		if size%2 != 0 {
			if offset >= len(input) || input[offset] != '\n' {
				t.Fatal("invalid ar padding")
			}
			offset++
		}
	}
	return members
}

func readDebTar(t *testing.T, data []byte) map[string]debEntry {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	entries := map[string]debEntry{}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Uid != 0 || header.Gid != 0 || header.ModTime.Unix() != 0 {
			t.Fatal("non-deterministic tar metadata")
		}
		contents, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		entries[strings.TrimPrefix(header.Name, "./")] = debEntry{header.Name, header.Mode, contents}
	}
	return entries
}

func TestDebArchiveDeterministic(t *testing.T) {
	control := []debEntry{{"control", 0644, []byte("odd")}}
	data := []debEntry{{"etc/hlcp/controller/", 0700, nil}, {"usr/bin/hlcp", 0755, []byte("binary")}}
	var first, second bytes.Buffer
	if err := writeDeb(&first, control, data); err != nil {
		t.Fatal(err)
	}
	if err := writeDeb(&second, control, data); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("non-deterministic deb")
	}
	members := readDebMembers(t, first.Bytes())
	if len(members) != 3 || string(members["debian-binary"]) != "2.0\n" {
		t.Fatal("invalid Debian members")
	}
	files := readDebTar(t, members["data.tar.gz"])
	if files["etc/hlcp/controller/"].mode != 0700 || files["usr/bin/hlcp"].mode != 0755 {
		t.Fatal("invalid modes")
	}
}

func TestControllerPackageContents(t *testing.T) {
	root := t.TempDir()
	for _, name := range append([]string{
		"deploy/systemd/hlcp-controller.service", "deploy/config/api-policy.json",
		"deploy/debian/controller/preinst", "deploy/debian/controller/postinst",
		"deploy/debian/controller/prerm", "deploy/debian/controller/postrm",
	}, operatorDocumentationSources()...) {
		contents, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	if err := os.Mkdir("out", 0700); err != nil {
		t.Fatal(err)
	}
	for _, binary := range []string{"hlcp", "hlcp-controller"} {
		if err := os.WriteFile(filepath.Join("out", binary+"-linux-amd64"), []byte("binary"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile("private.key", []byte("SECRET"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := packageControllerDeb("out", "v0.1.0", "abc123", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := packageControllerDeb("out", "v0.1.0", "abc123", "amd64"); err == nil {
		t.Fatal("overwrote output")
	}
	if _, err := packageControllerDeb("out", "v0.1.0", "abc123", "bad"); err == nil {
		t.Fatal("accepted architecture")
	}
	contents, err := os.ReadFile(filepath.Join("out", result.Name))
	if err != nil {
		t.Fatal(err)
	}
	members := readDebMembers(t, contents)
	control := readDebTar(t, members["control.tar.gz"])
	payload := readDebTar(t, members["data.tar.gz"])
	if !strings.Contains(string(control["control"].data), "Version: 0.1.0\nArchitecture: amd64\n") {
		t.Fatal("wrong control")
	}
	for _, script := range []string{"preinst", "postinst", "prerm", "postrm"} {
		if control[script].mode != 0755 || bytes.Contains(control[script].data, []byte("\r")) {
			t.Fatal("script permissions/LF")
		}
	}
	if len(payload) != 38 {
		t.Fatalf("unexpected file count: %d", len(payload))
	}
	for _, name := range []string{"hlcp", "hlcp-controller"} {
		if c := payload["usr/share/bash-completion/completions/"+name]; c.mode != 0644 || !bytes.Contains(c.data, []byte("complete -F _hlcp_complete "+name+"\n")) {
			t.Fatal("missing controller completion", name)
		}
	}
	if string(payload["usr/share/hlcp-controller/nodes.json"].data) != "[]\n" {
		t.Fatal("non-empty default allowlist")
	}
	if payload["etc/hlcp/controller/"].mode != 0700 || payload["usr/share/hlcp-controller/controller.env"].mode != 0644 {
		t.Fatal("directory or example mode")
	}
	unit := string(payload["usr/lib/systemd/system/hlcp-controller.service"].data)
	if strings.Contains(unit, "/usr/local/bin/") || !strings.Contains(unit, "/usr/bin/hlcp-controller") {
		t.Fatal("wrong packaged executable")
	}
	if _, exists := control["conffiles"]; exists {
		t.Fatal("live configuration must not be dpkg-owned")
	}
	for name := range payload {
		if strings.HasPrefix(name, "etc/hlcp/controller/") && name != "etc/hlcp/controller/" {
			t.Fatal("package occupies wizard namespace", name)
		}
	}
	for _, name := range []string{"controller.env", "nodes.json", "api-policy.json"} {
		if payload["usr/share/hlcp-controller/"+name].mode != 0644 {
			t.Fatal("missing public example", name)
		}
	}
	if bytes.Contains(contents, []byte("SECRET")) {
		t.Fatal("packaged secret")
	}
	preview, err := packageControllerDeb("out", "v0.1.0-preview.1", "abc123", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Name != "hlcp-controller_0.1.0-preview.1_amd64.deb" {
		t.Fatal("unsafe release asset name")
	}
	previewBytes, err := os.ReadFile(filepath.Join("out", preview.Name))
	if err != nil {
		t.Fatal(err)
	}
	previewControl := readDebTar(t, readDebMembers(t, previewBytes)["control.tar.gz"])
	if !strings.Contains(string(previewControl["control"].data), "Version: 0.1.0~preview.1\n") {
		t.Fatal("lost Debian prerelease ordering")
	}
}
