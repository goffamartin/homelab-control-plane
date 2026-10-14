package onboarding

import (
	"context"
	"errors"
	"hlcp/deploy/systemd"
	"hlcp/internal/setup"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validActivationProperties = "LoadState=loaded\nActiveState=inactive\nUnitFileState=disabled\nFragmentPath=/usr/lib/systemd/system/hlcp-controller.service\nDropInPaths=\nNeedDaemonReload=no\nUser=hlcp-controller\nGroup=hlcp-controller\n"

func TestActivationPropertiesFailClosed(t *testing.T) {
	if _, err := parseActivationProperties([]byte(validActivationProperties)); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"loaded", "masked"}, {"inactive", "failed"}, {"inactive", "activating"}, {"disabled", "enabled-runtime"}, {"disabled", "static"}, {"NeedDaemonReload=no", "NeedDaemonReload=yes"}, {"DropInPaths=", "DropInPaths=/etc/override.conf"}, {"User=hlcp-controller", "User=root"}, {"Group=hlcp-controller", "Group=root"}, {"/usr/lib/systemd/system/hlcp-controller.service", "/etc/systemd/system/hlcp-controller.service"}} {
		if _, err := parseActivationProperties([]byte(strings.Replace(validActivationProperties, pair[0], pair[1], 1))); err == nil {
			t.Fatal(pair)
		}
	}
	for _, data := range []string{validActivationProperties + "User=hlcp-controller\n", validActivationProperties + "SECRET=1\n", strings.Replace(validActivationProperties, "User=hlcp-controller\n", "", 1), strings.Repeat("x", 16*1024+1)} {
		if _, err := parseActivationProperties([]byte(data)); err == nil {
			t.Fatal("accepted invalid properties")
		}
	}
	p, err := parseActivationProperties([]byte(strings.ReplaceAll(strings.ReplaceAll(validActivationProperties, "inactive", "active"), "disabled", "enabled")))
	if err != nil || p["ActiveState"] != "active" {
		t.Fatal(err)
	}
	if strings.Contains(systemd.PackagedControllerUnit(), "/usr/local/bin/") || !strings.Contains(systemd.PackagedControllerUnit(), "LoadCredential=api.token:") {
		t.Fatal("incorrect packaged profile")
	}
}

func TestNativeActivationConfigAndPreservation(t *testing.T) {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		t.Skip("root fixture integration")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	material := controllerMaterial(t)
	credentials, err := NewControllerCredentials([]byte(strings.Repeat("a", 64)), []byte(strings.Repeat("b", 64)))
	if err != nil {
		t.Fatal(err)
	}
	p, err := PlanController(context.Background(), dir, setup.Owner{}, ControllerOptions{ListenAddress: "127.0.0.1:9443", ServerName: "controller.test", AdmittedNodes: []string{"debian-01"}}, material, credentials, testNow)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err = p.Apply(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	files, err := activationConfig(context.Background(), dir, "controller.test", testNow)
	if err != nil || len(files) != 9 {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "controller-operations.json")); !os.IsNotExist(err) {
		t.Fatal("ledger touched")
	}
	if _, err = activationConfig(context.Background(), dir, "wrong.test", testNow); err == nil {
		t.Fatal("wrong name accepted")
	}
	if _, err = activationConfig(context.Background(), dir, "controller.test", testNow.Add(400*24*time.Hour)); err == nil {
		t.Fatal("expired TLS accepted")
	}
	api := filepath.Join(dir, "api.token")
	original, _ := os.ReadFile(api)
	if err = os.Chmod(api, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = activationConfig(context.Background(), dir, "controller.test", testNow); err == nil {
		t.Fatal("public token accepted")
	}
	after, _ := os.ReadFile(api)
	if string(after) != string(original) {
		t.Fatal("token changed")
	}
	if err = os.Chmod(api, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(dir, "observer.token")); err != nil {
		t.Fatal(err)
	}
	if _, err = activationConfig(context.Background(), dir, "controller.test", testNow); err == nil {
		t.Fatal("partial credentials accepted")
	}
	if _, err = os.Stat(filepath.Join(dir, "observer.token")); !os.IsNotExist(err) {
		t.Fatal("token regenerated")
	}
}

func TestNativeActivationFileAndCommandRefusals(t *testing.T) {
	// Safe directory outside sticky /tmp: activation artifacts require strict parents.
	if os.Geteuid() != 0 {
		t.Skip("root artifact fixture")
	}
	dir, err := os.MkdirTemp("/root", "hlcp-activation-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "artifact")
	if err = os.WriteFile(path, []byte{0x7f, 'E', 'L', 'F', 1}, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = activationFile(path, 64, true); err != nil {
		t.Fatal(err)
	}
	if _, err = activationFile(path, 4, true); err == nil {
		t.Fatal("oversize accepted")
	}
	if err = os.Chmod(path, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err = activationFile(path, 64, true); err == nil {
		t.Fatal("writable executable accepted")
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err = activationFile(link, 64, false); err == nil {
		t.Fatal("redirected unit accepted")
	}
	for _, args := range [][]string{{"restart", controllerUnitName}, {"start", "other.service"}, {"enable", "--now", controllerUnitName}, {"show", controllerUnitName}} {
		if _, err = runActivationProcess(context.Background(), args, time.Second); !errors.Is(err, ErrActivationInvalid) {
			t.Fatal(args, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = runActivationProcess(ctx, []string{"start", controllerUnitName}, time.Second); !errors.Is(err, ErrActivationInvalid) {
		t.Fatal(err)
	}
	var out activationOutput
	data := make([]byte, 16*1024+1)
	n, err := out.Write(data)
	if n != len(data) || err != nil || !out.overflow || len(out.data) != 16*1024 {
		t.Fatal("unbounded output")
	}
	if _, err = (nativeActivationHost{"controller.test"}).Inspect(context.Background()); err == nil {
		t.Fatal("test executable accepted as installed controller")
	}
}

func TestNativePackagedActivationAccount(t *testing.T) {
	if os.Getenv("HLCP_TEST_SERVICE_ACCOUNT") != "1" {
		t.Skip("isolated container package-account fixture")
	}
	account, err := activationAccount()
	if err != nil || !strings.HasPrefix(account, "hlcp-controller:") {
		t.Fatal("package account refused", err)
	}
}
