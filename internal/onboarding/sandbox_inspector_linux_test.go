package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestSandboxInspectorProcessStatus(t *testing.T) {
	data := []byte("Uid:\t1000 1000 1000 1000\nGid:\t1000 1000 1000 1000\nGroups:\t1000\nNoNewPrivs:\t1\nCapInh:\t0000000000000000\nCapPrm:\t0000000000000000\nCapEff:\t0000000000000000\nCapAmb:\t0000000000000000\n")
	if !inspectorProcessStatus(data, 1000, 1000) {
		t.Fatal("valid process status refused")
	}
	for _, changed := range []string{
		strings.Replace(string(data), "NoNewPrivs:\t1", "NoNewPrivs:\t0", 1),
		strings.Replace(string(data), "Groups:\t1000", "Groups:\t1000 2000", 1),
		strings.Replace(string(data), "1000 1000 1000 1000", "1000 0 1000 1000", 1),
		strings.Replace(string(data), "CapEff:\t0000000000000000", "CapEff:\t0000000000000001", 1),
		string(data) + "NoNewPrivs:\t1\n", strings.Repeat("x", 64*1024+1),
	} {
		if inspectorProcessStatus([]byte(changed), 1000, 1000) {
			t.Fatal("unsafe process status accepted")
		}
	}
}

func TestNativeSandboxInspectorDisposable(t *testing.T) {
	if os.Getenv("HLCP_INSPECTOR_TEST") != "disposable-container" || os.Geteuid() != 0 {
		t.Skip("explicit disposable root container fixture")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("not a container")
	}
	if _, err := os.Stat("/run/systemd/system"); !os.IsNotExist(err) {
		t.Fatal("booted systemd fixture refused")
	}
	account, err := user.Lookup("hlcp")
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := strconv.ParseUint(account.Uid, 10, 32)
	gid, _ := strconv.ParseUint(account.Gid, 10, 32)
	directory, err := os.MkdirTemp("/opt", "hlcp-inspector-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	os.Chmod(directory, 0755)
	work := filepath.Join(directory, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(work, int(uid), int(gid)); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(work, "executed")
	executable := filepath.Join(directory, "probe")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(nnp, expected string) {
		child := exec.Command(self, "-test.run", "^TestNativeSandboxInspectorChild$", "-test.v")
		if nnp == "1" {
			child = exec.Command("/usr/bin/setpriv", "--no-new-privs", "--", self, "-test.run", "^TestNativeSandboxInspectorChild$", "-test.v")
		}
		child.Env = append(os.Environ(), "HLCP_INSPECTOR_CHILD=1", "HLCP_INSPECTOR_EXPECT="+expected, "HLCP_INSPECTOR_PATH="+executable, "HLCP_INSPECTOR_DIRECTORY="+work)
		child.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{uint32(gid)}}}
		if output, err := child.CombinedOutput(); err != nil {
			t.Fatalf("child: %v\n%s", err, output)
		}
	}
	run("1", "ok")
	run("0", "identity")
	os.Chmod(executable, 0777)
	run("1", "file")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("configured script executed")
	}
}
func TestNativeSandboxInspectorChild(t *testing.T) {
	if os.Getenv("HLCP_INSPECTOR_CHILD") != "1" || os.Geteuid() == 0 {
		t.Skip("inspector fixture child")
	}
	request := inspectorRequest{Version: 1, UID: uint32(os.Geteuid()), GID: uint32(os.Getegid()), SourceProof: strings.Repeat("a", 64), RequestProof: strings.Repeat("b", 64), Files: []inspectorFile{{os.Getenv("HLCP_INSPECTOR_PATH"), os.Getenv("HLCP_INSPECTOR_DIRECTORY")}}}
	data, _ := json.Marshal(request)
	var out bytes.Buffer
	code := runSandboxInspector(context.Background(), nil, bytes.NewReader(data), &out, nativeSandboxInspectorHost{})
	expected := os.Getenv("HLCP_INSPECTOR_EXPECT")
	if expected == "ok" {
		if code != 0 || !strings.Contains(out.String(), `"status":"inspected"`) {
			t.Fatal(code, out.String())
		}
	} else {
		failure := "IDENTITY_REFUSED"
		if expected == "file" {
			failure = "INSPECTION_REFUSED"
		}
		if code != 2 || !strings.Contains(out.String(), failure) {
			t.Fatal(code, out.String())
		}
	}
	if strings.Contains(out.String(), request.Files[0].Executable) || strings.Contains(out.String(), "SECRET") {
		t.Fatal("path leaked")
	}
	fmt.Println("native inspector identity/metadata checked without resource execution")
}
