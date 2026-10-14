package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"hlcp/internal/setup"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestGrantSourceRoleSnapshotsAndPreviewValidation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root source fixture")
	}
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	for name, data := range map[string]string{"api-policy.json": controllerPolicy, "api.token": strings.Repeat("a", 64) + "\n", "observer.token": strings.Repeat("b", 64) + "\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, role := range []string{"operator", "observer"} {
		p, plan, c, err := controllerGrantSource(dir, role)
		if err != nil {
			t.Fatal(err)
		}
		if len(credentialDigest(c)) != 64 {
			t.Fatal("bad digest")
		}
		if p.Recheck(context.Background(), plan) != nil {
			t.Fatal("source stale")
		}
		if err := os.WriteFile(filepath.Join(dir, "api.token"), []byte(strings.Repeat("c", 64)+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if p.Recheck(context.Background(), plan) == nil {
			t.Fatal("changed source accepted")
		}
		if err := os.WriteFile(filepath.Join(dir, "api.token"), []byte(strings.Repeat("a", 64)+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		p.Close()
	}
	wrong := strings.Replace(controllerPolicy, `"role":"operator"`, `"role":"observer"`, 1)
	os.WriteFile(filepath.Join(dir, "api-policy.json"), []byte(wrong), 0600)
	if _, _, _, err := controllerGrantSource(dir, "operator"); err == nil {
		t.Fatal("wrong role exported")
	}
	os.WriteFile(filepath.Join(dir, "api-policy.json"), []byte(strings.Replace(controllerPolicy, "api.token", "/tmp/SECRET", 1)), 0600)
	if _, _, _, err := controllerGrantSource(dir, "operator"); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatal("external token path accepted or echoed")
	}
	preview := setup.Preview{ReadOnly: true, Changes: []setup.Change{{Name: "cli.json", Action: "create", Mode: 0600}, {Name: "cli.token", Action: "create", Mode: 0600}}}
	report := clientGrantReport{Status: "preview", ReadOnly: true, Preview: &preview}
	if !validGrantPreview(report) {
		t.Fatal("valid preview refused")
	}
	preview.Changes[0].Name = "SECRET"
	if validGrantPreview(report) {
		t.Fatal("arbitrary child text accepted")
	}
	if _, err := lookupGrantAccount("root"); err == nil {
		t.Fatal("root target accepted")
	}
	if code := ClientProvisionChild(strings.NewReader(`{}`), &bytes.Buffer{}); code == 0 {
		t.Fatal("root child allowed")
	}
}

// Explicit opt-in only, in a fresh throwaway container with the actual trusted
// controller binary installed and hlcp-test-user created by the test command.
func TestInstalledClientGrantEndToEnd(t *testing.T) {
	if os.Getenv("HLCP_TEST_INSTALLED_GRANT") != "1" {
		t.Skip("isolated installed controller integration")
	}
	if os.Geteuid() != 0 {
		t.Fatal("root fixture required")
	}
	if _, err := os.Lstat(controllerConfigDirectory); !os.IsNotExist(err) {
		t.Fatal("fixture refuses existing controller configuration")
	}
	if err := os.MkdirAll(controllerConfigDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(controllerConfigDirectory)
	for name, data := range map[string]string{"api-policy.json": controllerPolicy, "api.token": strings.Repeat("a", 64) + "\n", "observer.token": strings.Repeat("b", 64) + "\n"} {
		if err := os.WriteFile(filepath.Join(controllerConfigDirectory, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	account, err := user.Lookup("hlcp-test-user")
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(account.HomeDir, ".config")
	if output, err := exec.Command("/usr/sbin/runuser", "-u", account.Username, "--", "/usr/bin/mkdir", "-m", "700", parent).CombinedOutput(); err != nil {
		t.Fatalf("parent fixture: %v %s", err, output)
	}
	path := filepath.Join(parent, "hlcp")
	run := func(extra ...string) clientGrantReport {
		t.Helper()
		args := append([]string{"configure-client", "--user", account.Username, "--json"}, extra...)
		output, err := exec.Command("/usr/bin/hlcp-controller", args...).Output()
		var report clientGrantReport
		if json.Unmarshal(output, &report) != nil || bytes.Contains(output, []byte(strings.Repeat("a", 64))) || bytes.Contains(output, []byte(strings.Repeat("b", 64))) {
			t.Fatalf("invalid/redaction failed, exit=%v", err)
		}
		return report
	}
	r := run()
	if r.Status != "preview" || !r.ReadOnly {
		t.Fatal(r.Status, r.Code)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("preview wrote or exported credential")
	}
	r = run("--apply", "--yes")
	if r.Status != "applied" || r.Result == nil || r.Result.Changed != 2 {
		t.Fatal(r.Status, r.Code)
	}
	for _, file := range []string{"cli.json", "cli.token"} {
		info, err := os.Stat(filepath.Join(path, file))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("unsafe file")
		}
		stat := info.Sys().(*syscall.Stat_t)
		if stat.Uid == 0 {
			t.Fatal("root wrote user file")
		}
	}
	token, _ := os.ReadFile(filepath.Join(path, "cli.token"))
	if string(token) != strings.Repeat("a", 64)+"\n" {
		t.Fatal("wrong access exported")
	}
	r = run("--apply", "--yes")
	if r.Status != "applied" || r.Result.Changed != 0 {
		t.Fatal(r.Status, r.Code)
	}
	r = run("--role", "observer", "--apply", "--yes")
	if r.Status != "failed" || !r.ReadOnly {
		t.Fatal("existing access replaced")
	}
	after, _ := os.ReadFile(filepath.Join(path, "cli.token"))
	if !bytes.Equal(after, token) {
		t.Fatal("token rotated")
	}
}
