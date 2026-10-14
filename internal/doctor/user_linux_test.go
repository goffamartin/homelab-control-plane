package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAccountProcessDropsUIDGIDGroupsAndEnvironment(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-only credential test")
	}
	account, err := user.Lookup("nobody")
	if err != nil {
		t.Skip("nobody account unavailable")
	}
	credential, err := selectedCredential(account.Username)
	if err != nil {
		t.Fatal(err)
	}
	data, exit, err := runAccountProcess("/usr/bin/id", []string{"-u"}, credential)
	if err != nil || exit != 0 || strings.TrimSpace(string(data)) != account.Uid {
		t.Fatalf("uid %q %d %v", data, exit, err)
	}
	data, exit, err = runAccountProcess("/usr/bin/id", []string{"-g"}, credential)
	if err != nil || exit != 0 || strings.TrimSpace(string(data)) != account.Gid {
		t.Fatalf("gid %q %d %v", data, exit, err)
	}
	data, exit, err = runAccountProcess("/usr/bin/id", []string{"-G"}, credential)
	if err != nil || exit != 0 {
		t.Fatal("group query failed")
	}
	seen := map[uint32]bool{}
	for _, value := range strings.Fields(string(data)) {
		gid, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		seen[uint32(gid)] = true
	}
	if len(seen) != len(credential.Groups) {
		t.Fatal("parent groups retained")
	}
	for _, gid := range credential.Groups {
		if !seen[gid] {
			t.Fatal("group mismatch")
		}
	}
	t.Setenv("SUDO_USER", "SECRET")
	t.Setenv("HOME", "SECRET")
	t.Setenv("LD_PRELOAD", "SECRET")
	data, exit, err = runAccountProcess("/usr/bin/env", nil, credential)
	if err != nil || exit != 0 || strings.Contains(string(data), "SECRET") ||
		strings.Contains(string(data), "HOME=") || strings.Contains(string(data), "SUDO_USER=") {
		t.Fatal("environment inherited")
	}
	if _, err := selectedCredential("root"); err == nil {
		t.Fatal("root target accepted")
	}
	if _, err := selectedCredential("hlcp-missing-account-SECRET"); err == nil {
		t.Fatal("missing account accepted")
	}
}

func TestInstalledUserCLIReadPermissions(t *testing.T) {
	binary := os.Getenv("HLCP_DOCTOR_TEST_BINARY")
	if binary == "" || os.Geteuid() != 0 {
		t.Skip("explicit installed binary integration fixture required")
	}
	if !trustedProbeExecutable(binary) {
		t.Fatal("unsafe fixture binary")
	}
	account, err := user.Lookup("nobody")
	if err != nil {
		t.Skip("nobody unavailable")
	}
	credential, err := selectedCredential(account.Username)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp("/tmp", "hlcp-user-doctor-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	if err := os.Chown(directory, int(credential.Uid), int(credential.Gid)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "config")
	if err := os.WriteFile(path, []byte(minimalAgent), 0600); err != nil {
		t.Fatal(err)
	}
	check := func(wantConfig string) {
		t.Helper()
		before := snapshotTree(t, directory)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "doctor", "--user", account.Username, "--scope", "user", "--config", path, "--setup-dir", directory, "--json")
		cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "HOME=SECRET", "SUDO_USER=SECRET"}
		data, err := cmd.Output()
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			t.Fatal(err)
		}
		var report Report
		if json.Unmarshal(data, &report) != nil || !report.ReadOnly || stateFor(report, "agent.config") != wantConfig ||
			stateFor(report, "setup.transaction") != "ok" || strings.Contains(string(data), "SECRET") {
			t.Fatalf("%s", data)
		}
		if !reflect.DeepEqual(before, snapshotTree(t, directory)) {
			t.Fatal("child changed configuration/state")
		}
	}
	check("fail") // Root-owned private file cannot be read using parent's root privileges.
	if err := os.Chown(path, int(credential.Uid), int(credential.Gid)); err != nil {
		t.Fatal(err)
	}
	check("ok") // TLS fixture paths are absent; only configuration readability is asserted.
}
