package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type debEntry struct {
	name string
	mode int64
	data []byte
}

var debVersionPattern = regexp.MustCompile(`^[0-9][A-Za-z0-9.+~]*$`)

// Numeric release versions map directly. Named builds are prerelease snapshots;
// their hash suffix identifies source, not chronological upgrade ordering.
func debianVersion(version, revision string) string {
	numeric := strings.ReplaceAll(strings.TrimPrefix(version, "v"), "-", "~")
	if debVersionPattern.MatchString(numeric) {
		if strings.HasSuffix(revision, "-dirty") {
			return numeric + "~dirty+git" + strings.TrimSuffix(revision, "-dirty")
		}
		return numeric
	}
	return "0~" + strings.ReplaceAll(version, "-", "~") + "+git" + strings.ReplaceAll(revision, "-", "+")
}

func debTar(entries []debEntry) ([]byte, error) {
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		kind := byte(tar.TypeReg)
		if strings.HasSuffix(entry.name, "/") {
			kind = tar.TypeDir
		}
		if err := tw.WriteHeader(&tar.Header{Name: "./" + entry.name, Mode: entry.mode,
			Size: int64(len(entry.data)), Typeflag: kind}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(entry.data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func writeARMember(output io.Writer, name string, data []byte) error {
	if len(name) > 15 || len(data) > 9999999999 {
		return fmt.Errorf("invalid Debian archive member")
	}
	header := fmt.Sprintf("%-16s%-12d%-6d%-6d%-8s%-10d\x60\n", name+"/", 0, 0, 0, "100644", len(data))
	if _, err := io.WriteString(output, header); err != nil {
		return err
	}
	if _, err := output.Write(data); err != nil {
		return err
	}
	if len(data)%2 != 0 {
		_, err := io.WriteString(output, "\n")
		return err
	}
	return nil
}

func writeDeb(output io.Writer, control, data []debEntry) error {
	if _, err := io.WriteString(output, "!<arch>\n"); err != nil {
		return err
	}
	if err := writeARMember(output, "debian-binary", []byte("2.0\n")); err != nil {
		return err
	}
	for _, member := range []struct {
		name    string
		entries []debEntry
	}{
		{"control.tar.gz", control}, {"data.tar.gz", data},
	} {
		compressed, err := debTar(member.entries)
		if err != nil {
			return err
		}
		if err := writeARMember(output, member.name, compressed); err != nil {
			return err
		}
	}
	return nil
}

func debSource(path string, text bool) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 32*1024*1024 {
		return nil, fmt.Errorf("invalid package source %s", path)
	}
	data, err := os.ReadFile(path)
	if text {
		data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	}
	return data, err
}

func packageControllerDeb(out, version, revision, arch string) (artifact, error) {
	if arch != "amd64" && arch != "arm64" {
		return artifact{}, fmt.Errorf("unsupported Debian architecture")
	}
	packageVersion := debianVersion(version, revision)
	if !debVersionPattern.MatchString(packageVersion) {
		return artifact{}, fmt.Errorf("invalid Debian version")
	}
	var data []debEntry
	for _, dir := range []string{"etc/", "etc/hlcp/", "usr/", "usr/bin/", "usr/lib/", "usr/lib/systemd/",
		"usr/lib/systemd/system/", "usr/share/", "usr/share/doc/", "usr/share/doc/hlcp-controller/"} {
		data = append(data, debEntry{dir, 0755, nil})
	}
	data = append(data, debEntry{"etc/hlcp/controller/", 0700, nil})
	for _, binary := range []string{"hlcp-controller", "hlcp"} {
		contents, err := debSource(filepath.Join(out, binary+"-linux-"+arch), false)
		if err != nil {
			return artifact{}, err
		}
		data = append(data, debEntry{"usr/bin/" + binary, 0755, contents})
	}
	unit, err := debSource("deploy/systemd/hlcp-controller.service", true)
	if err != nil {
		return artifact{}, err
	}
	unit = bytes.ReplaceAll(unit, []byte("/usr/local/bin/hlcp-controller"), []byte("/usr/bin/hlcp-controller"))
	data = append(data, debEntry{"usr/lib/systemd/system/hlcp-controller.service", 0644, unit})
	// Configuration is packaged as conffiles; production credentials never are.
	data = append(data, debEntry{"etc/hlcp/controller/controller.env", 0600, []byte("HLCP_AGENT_LISTEN=127.0.0.1:9443\n")},
		debEntry{"etc/hlcp/controller/nodes.json", 0600, []byte("[]\n")})
	policy, err := debSource("deploy/config/api-policy.json", true)
	if err != nil {
		return artifact{}, err
	}
	data = append(data, debEntry{"etc/hlcp/controller/api-policy.json", 0600, policy})
	for _, name := range []string{"07-build-and-release.md", "09-telemetry-and-audit.md", "10-scoped-api-credentials.md",
		"11-bounded-diagnostics-api.md", "12-debian-management-deployment.md", "13-debian-controller-package.md", "14-cli-setup-and-agent-installation.md", "17-generic-agent-profiles.md", "15-onboarding-and-agent-profile-design-notes.md", "16-plug-and-play-implementation-plan.md"} {
		contents, err := debSource(filepath.Join("docs", name), true)
		if err != nil {
			return artifact{}, err
		}
		data = append(data, debEntry{"usr/share/doc/hlcp-controller/" + name, 0644, contents})
	}
	installedSize := 0
	for _, entry := range data {
		installedSize += (len(entry.data) + 1023) / 1024
	}
	controlText := fmt.Sprintf("Package: hlcp-controller\nVersion: %s\nArchitecture: %s\nMaintainer: Martin Goffa <goffamartin@gmail.com>\nSection: admin\nPriority: optional\nInstalled-Size: %d\nDepends: adduser, systemd (>= 252)\nHomepage: https://github.com/goffamartin/homelab-control-plane\nDescription: HomeLab controller and local CLI\n Native mTLS control plane. Credentials must be provisioned before startup.\n", packageVersion, arch, installedSize)
	control := []debEntry{{"control", 0644, []byte(controlText)},
		{"conffiles", 0644, []byte("/etc/hlcp/controller/controller.env\n/etc/hlcp/controller/nodes.json\n/etc/hlcp/controller/api-policy.json\n")}}
	for _, name := range []string{"preinst", "postinst", "prerm", "postrm"} {
		script, err := debSource(filepath.Join("deploy/debian/controller", name), true)
		if err != nil {
			return artifact{}, err
		}
		control = append(control, debEntry{name, 0755, script})
	}
	// GitHub normalizes '~' in uploaded asset names. Keep filenames portable;
	// the Debian version inside control retains '~' for prerelease ordering.
	filename := "hlcp-controller_" + strings.ReplaceAll(packageVersion, "~", "-") + "_" + arch + ".deb"
	file, err := os.OpenFile(filepath.Join(out, filename), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return artifact{}, err
	}
	err = writeDeb(file, control, data)
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
