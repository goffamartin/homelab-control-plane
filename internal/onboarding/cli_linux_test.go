package onboarding

import (
	"bytes"
	"hlcp/internal/agent"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeSetupCLIWritesOnlyConfirmedPlan(t *testing.T) {
	input := t.TempDir()
	target := filepath.Join(t.TempDir(), "config")
	os.Chmod(input, 0700)
	material := agentMaterial(t)
	files := map[string][]byte{"cert": material.certificate, "key": material.key, "peer": material.peerTrust, "issuer": material.issuerTrust}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(input, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	journal := filepath.Join(t.TempDir(), "existing-journal")
	os.WriteFile(journal, []byte("PRIVATE existing history"), 0600)
	scope := "user"
	if os.Geteuid() == 0 {
		scope = "system"
	}
	args := []string{"--node", "debian-01", "--controller", "controller.test:9443", "--server-name", "controller.test",
		"--directory", target, "--journal", journal, "--cert", filepath.Join(input, "cert"), "--key", filepath.Join(input, "key"),
		"--peer-ca", filepath.Join(input, "peer"), "--issuer-ca", filepath.Join(input, "issuer"), "--scope", scope, "--json"}
	var out bytes.Buffer
	if runAgentCLI(args, nil, &out, nativeSetupHost{}, false, testNow) != 0 {
		t.Fatalf("%s", out.String())
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("preview wrote")
	}
	if !strings.Contains(out.String(), "create-directory") {
		t.Fatal("directory absent from preview")
	}
	out.Reset()
	if runAgentCLI(append(args, "--apply", "--yes"), nil, &out, nativeSetupHost{}, false, testNow) != 0 {
		t.Fatalf("%s", out.String())
	}
	config, err := agent.Load(filepath.Join(target, "agent.yaml"))
	if err != nil || len(config.Resources) != 0 {
		t.Fatal("generated config")
	}
	if scope == "system" && (config.CertificateFile != "/run/credentials/hlcp-agent.service/agent.crt" || config.PrivateKeyFile != "/run/credentials/hlcp-agent.service/agent.key" || config.CAFile != "/run/credentials/hlcp-agent.service/ca.crt") {
		t.Fatal("system service would read inaccessible root provisioning paths")
	}
	if scope == "user" && config.CertificateFile != filepath.Join(target, "agent.crt") {
		t.Fatal("user profile incorrectly uses system credentials")
	}
	out.Reset()
	if runAgentCLI(append(args, "--apply", "--yes"), nil, &out, nativeSetupHost{}, false, testNow) != 0 ||
		!strings.Contains(out.String(), `"changed":0`) {
		t.Fatalf("repeat %s", out.String())
	}
	data, _ := os.ReadFile(journal)
	if string(data) != "PRIVATE existing history" {
		t.Fatal("journal changed")
	}
	os.Chmod(filepath.Join(input, "key"), 0644)
	out.Reset()
	if runAgentCLI(append(args, "--apply", "--yes"), nil, &out, nativeSetupHost{}, false, testNow) != 1 ||
		!strings.Contains(out.String(), "UNSAFE_TLS_INPUT") {
		t.Fatal("public key input accepted")
	}
	if terminalInput(strings.NewReader("input")) {
		t.Fatal("reader treated as TTY")
	}
}
