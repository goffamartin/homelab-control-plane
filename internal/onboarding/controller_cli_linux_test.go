package onboarding

import (
	"bytes"
	"hlcp/internal/localapi"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeControllerCLIPreviewApplyAndRepeat(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root controller profile integration")
	}
	input := t.TempDir()
	os.Chmod(input, 0700)
	dir := filepath.Join(t.TempDir(), "controller")
	material := controllerMaterial(t)
	for name, data := range map[string][]byte{"cert": material.certificate, "key": material.key, "peer": material.peerTrust, "issuer": material.issuerTrust} {
		if err := os.WriteFile(filepath.Join(input, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"--directory", dir, "--listen", "127.0.0.1:9443", "--server-name", "controller.test",
		"--cert", filepath.Join(input, "cert"), "--key", filepath.Join(input, "key"), "--peer-ca", filepath.Join(input, "peer"), "--issuer-ca", filepath.Join(input, "issuer"), "--json"}
	clock := func() time.Time { return testNow }
	var out bytes.Buffer
	if runControllerCLI(args, nil, &out, nativeSetupHost{}, false, clock) != 0 {
		t.Fatalf("%s", out.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("preview writes")
	}
	if runControllerCLI(append(args, "--apply", "--yes", "--admit", "debian-01"), nil, &out, nativeSetupHost{}, false, clock) != 0 {
		t.Fatalf("%s", out.String())
	}
	api, err := localapi.LoadToken(filepath.Join(dir, "api.token"))
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := os.ReadFile(filepath.Join(dir, "nodes.json"))
	out.Reset()
	if runControllerCLI(append(args, "--apply", "--yes"), nil, &out, nativeSetupHost{}, false, clock) != 0 || !strings.Contains(out.String(), `"changed":0`) {
		t.Fatalf("%s", out.String())
	}
	again, _ := localapi.LoadToken(filepath.Join(dir, "api.token"))
	after, _ := os.ReadFile(filepath.Join(dir, "nodes.json"))
	if again != api || string(after) != string(nodes) || string(nodes) != `["debian-01"]` {
		t.Fatal("repeat changed access")
	}
	if strings.Contains(out.String(), api) {
		t.Fatal("bearer leaked")
	}
}
