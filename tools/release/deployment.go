package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Explicit source allowlist: never package a directory walk or local secrets.
var deploymentFiles = []string{
	"deploy/systemd/hlcp-controller.service",
	"deploy/systemd/hlcp-agent.service",
	"deploy/systemd/hlcp-agent-docker.conf",
	"deploy/systemd/user/hlcp-agent-podman.service",
	"deploy/systemd/hlcp-telemetry.conf",
	"deploy/systemd/journald-hlcp-retention.conf",
	"deploy/config/controller.env",
	"deploy/config/nodes.json",
	"deploy/config/api-policy.json",
	"deploy/config/agent-docker.yaml",
	"deploy/config/agent-podman.yaml",
	"examples/api-policy.json",
	"docs/07-build-and-release.md",
	"docs/09-telemetry-and-audit.md",
	"docs/10-scoped-api-credentials.md",
	"docs/11-bounded-diagnostics-api.md",
	"docs/12-debian-management-deployment.md",
	"docs/13-debian-controller-package.md",
}

func writeDeployment(root string, output io.Writer) error {
	gz := gzip.NewWriter(output)
	tw := tar.NewWriter(gz)
	for _, name := range deploymentFiles {
		path := filepath.Join(root, filepath.FromSlash(name))
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
			return fmt.Errorf("invalid deployment source: %s", name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		data = []byte(strings.ReplaceAll(string(data), "\r\n", "\n"))
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func packageDeployment(out string) (artifact, error) {
	const name = "hlcp-deployment.tar.gz"
	path := filepath.Join(out, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return artifact{}, err
	}
	err = writeDeployment(".", file)
	closeErr := file.Close()
	if err != nil {
		return artifact{}, err
	}
	if closeErr != nil {
		return artifact{}, closeErr
	}
	digest, err := checksum(path)
	return artifact{Name: name, SHA256: digest}, err
}
