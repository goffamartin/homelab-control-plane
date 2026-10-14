package onboarding

import (
	"context"
	"errors"
	"hlcp/internal/agent"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// Explicit opt-in only in the disposable build container. Never fabricate host
// dependencies during normal tests or overwrite an installed runtime/helper.
func TestNativeResourceDisposableRuntimeFixtures(t *testing.T) {
	if os.Getenv("HLCP_RESOURCE_FIXTURES") != "1" || os.Geteuid() != 0 {
		t.Skip("disposable root container opt-in")
	}
	account, err := user.Lookup("hlcp-test-user")
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := strconv.ParseUint(account.Uid, 10, 32)
	gid, _ := strconv.ParseUint(account.Gid, 10, 32)
	paths := []string{"/run/docker.sock", "/usr/bin/podman", "/usr/bin/newuidmap", "/usr/bin/newgidmap"}
	for _, path := range paths {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Skip("existing runtime fixture path")
		}
	}
	data, err := os.ReadFile("/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths[1:] {
		if err := os.WriteFile(path, data, 0755); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Remove(path) })
		if path != "/usr/bin/podman" {
			if err := os.Chmod(path, 0755|os.ModeSetuid); err != nil {
				t.Fatal(err)
			}
		}
	}
	listener, err := net.Listen("unix", "/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chown("/run/docker.sock", 0, int(gid)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod("/run/docker.sock", 0660); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(socket, mappings string) {
		cmd := exec.Command(self, "-test.run", "^TestNativeResourceFixtureChild$", "-test.v")
		cmd.Env = append(os.Environ(), "HLCP_RESOURCE_CHILD=1", "HLCP_EXPECT_SOCKET="+socket, "HLCP_EXPECT_MAPPINGS="+mappings)
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{uint32(gid)}}}
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("non-root metadata fixture: %v\n%s", err, output)
		}
	}
	run("ok", "ok")
	os.Chmod("/run/docker.sock", 0666)
	run("refuse", "ok")
	os.Chmod("/run/docker.sock", 0600)
	run("refuse", "ok")
	os.Chmod("/run/docker.sock", 0660)
	os.Chmod("/usr/bin/newuidmap", 0755)
	run("ok", "refuse")
}
func TestNativeResourceFixtureChild(t *testing.T) {
	if os.Getenv("HLCP_RESOURCE_CHILD") != "1" || os.Geteuid() == 0 {
		t.Skip("fixture child")
	}
	h := nativeResourceHost{}
	if proof, err := h.RuntimeExecutable(context.Background(), "podman"); err != nil || proof == [32]byte{} {
		t.Fatal("fixed executable fingerprint", err)
	}
	for _, row := range []struct {
		name     string
		probe    func(context.Context) ([32]byte, error)
		expected string
	}{
		{"socket", h.DockerSocket, os.Getenv("HLCP_EXPECT_SOCKET")}, {"mappings", h.PodmanMappings, os.Getenv("HLCP_EXPECT_MAPPINGS")},
	} {
		proof, err := row.probe(context.Background())
		if row.expected == "ok" {
			if err != nil || proof == [32]byte{} {
				t.Fatal(row.name, err)
			}
			again, err := row.probe(context.Background())
			if err != nil || again != proof {
				t.Fatal("unstable dependency evidence", row.name, err)
			}
		} else if !errors.Is(err, ErrAgentActivationResources) {
			t.Fatal("unsafe dependency accepted", row.name, err)
		}
	}
}

type resourceTestInfo struct {
	mode     os.FileMode
	uid, gid uint32
}

func (i resourceTestInfo) Name() string       { return "metadata" }
func (i resourceTestInfo) Size() int64        { return 0 }
func (i resourceTestInfo) Mode() os.FileMode  { return i.mode }
func (i resourceTestInfo) ModTime() time.Time { return time.Time{} }
func (i resourceTestInfo) IsDir() bool        { return i.mode.IsDir() }
func (i resourceTestInfo) Sys() any           { return &syscall.Stat_t{Uid: i.uid, Gid: i.gid} }

func TestNativeResourcePOSIXAccessClass(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("non-root POSIX permission classes")
	}
	uid, gid := uint32(os.Geteuid()), uint32(os.Getegid())
	for _, row := range []struct {
		info    resourceTestInfo
		want    os.FileMode
		allowed bool
	}{
		{resourceTestInfo{0600, uid, gid}, 6, true}, {resourceTestInfo{0060, uid, gid}, 6, false},
		{resourceTestInfo{0060, 0, gid}, 6, true}, {resourceTestInfo{0606, 0, gid}, 6, false},
		{resourceTestInfo{0006, 0, 424242}, 6, true}, {resourceTestInfo{0001, uid, gid}, 1, false},
	} {
		if resourceAccess(row.info, row.want) != row.allowed {
			t.Fatal(row)
		}
	}
}

type nativeResourceActivationFixture struct {
	*agentActivationFake
	config        agent.Config
	mutateOnStart string
}

func (h *nativeResourceActivationFixture) ChangeAgent(ctx context.Context, p AgentServiceProfile, action ControllerServiceAction) error {
	if err := h.agentActivationFake.ChangeAgent(ctx, p, action); err != nil {
		return err
	}
	if action == ControllerStart && h.mutateOnStart != "" {
		return os.WriteFile(h.mutateOnStart, []byte("#!/bin/sh\n# changed after start\ntrue\n"), 0700)
	}
	return nil
}

func (h *nativeResourceActivationFixture) InspectAgent(ctx context.Context, p AgentServiceProfile) (AgentActivationSnapshot, error) {
	r, err := PlanResourceReadiness(ctx, nativeResourceHost{}, p, h.config)
	if err != nil {
		return AgentActivationSnapshot{}, err
	}
	snapshot := h.snapshot
	snapshot.Service.Proof = r.proof
	return snapshot, nil
}
func TestNativeResourceActivationStaleScriptRefusesStart(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires non-root resource owner")
	}
	directory := t.TempDir()
	os.Chmod(directory, 0700)
	executable := filepath.Join(directory, "probe")
	os.WriteFile(executable, []byte("#!/bin/sh\ntrue\n"), 0700)
	c := readinessConfig()
	c.Resources = c.Resources[3:]
	c.Capabilities = []string{"resource.control", "script"}
	c.Resources[0].Actions = []string{"status"}
	c.Resources[0].Commands = map[string]agent.ScriptCommandConfig{"status": {Executable: executable, WorkingDirectory: directory}}
	h := &nativeResourceActivationFixture{agentActivationFake: newAgentActivationFake(), config: c}
	h.snapshot.CallerUID = uint32(os.Geteuid())
	h.snapshot.CallerGID = uint32(os.Getegid())
	p, err := PlanAgentActivation(context.Background(), h, AgentServiceProfile{"user", "hlcp-agent.service"}, false)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(executable, []byte("#!/bin/sh\n# changed\ntrue\n"), 0700)
	if _, err := p.Apply(context.Background(), true); !errors.Is(err, ErrActivationStale) || len(h.actions) != 0 {
		t.Fatal("stale dependency allowed start", err, h.actions)
	}
	h = &nativeResourceActivationFixture{agentActivationFake: newAgentActivationFake(), config: c, mutateOnStart: executable}
	h.snapshot.CallerUID = uint32(os.Geteuid())
	h.snapshot.CallerGID = uint32(os.Getegid())
	p, err = PlanAgentActivation(context.Background(), h, AgentServiceProfile{"user", "hlcp-agent.service"}, false)
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Apply(context.Background(), true)
	if !errors.Is(err, ErrActivationIncomplete) || !r.Uncertain || len(h.actions) != 1 {
		t.Fatal("post-start dependency change not uncertain", r, err)
	}
	if _, err := p.Apply(context.Background(), true); !errors.Is(err, ErrActivationStale) || len(h.actions) != 1 {
		t.Fatal("automatic repeat after uncertainty", err)
	}
}

func TestNativeResourceRejectsRootAndUnknownExecutable(t *testing.T) {
	h := nativeResourceHost{}
	if _, err := h.RuntimeExecutable(context.Background(), "/bin/sh"); !errors.Is(err, ErrAgentActivationResources) {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		return
	}
	for _, probe := range []func() ([32]byte, error){
		func() ([32]byte, error) { return h.RuntimeExecutable(context.Background(), "systemctl") },
		func() ([32]byte, error) { return h.DockerSocket(context.Background()) },
		func() ([32]byte, error) { return h.PodmanMappings(context.Background()) },
		func() ([32]byte, error) {
			return h.ScriptFiles(context.Background(), agent.ScriptCommandConfig{Executable: "/bin/sh", WorkingDirectory: "/"})
		},
	} {
		if _, err := probe(); !errors.Is(err, ErrAgentActivationResources) {
			t.Fatal("root resource inspection accepted", err)
		}
	}
}
func TestNativeResourceScriptReadOnlyAndStaleEvidence(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires actual non-root owner")
	}
	directory := t.TempDir()
	os.Chmod(directory, 0700)
	marker := filepath.Join(directory, "executed")
	executable := filepath.Join(directory, "probe")
	data := []byte("#!/bin/sh\ntouch '" + marker + "'\n")
	if err := os.WriteFile(executable, data, 0700); err != nil {
		t.Fatal(err)
	}
	command := agent.ScriptCommandConfig{Executable: executable, WorkingDirectory: directory}
	h := nativeResourceHost{}
	proof, err := h.ScriptFiles(context.Background(), command)
	if err != nil || proof == [32]byte{} {
		t.Fatal(err)
	}
	again, err := h.ScriptFiles(context.Background(), command)
	if err != nil || again != proof {
		t.Fatal("unstable metadata", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("script executed")
	}
	if err := os.WriteFile(executable, append(data, []byte("# changed\n")...), 0700); err != nil {
		t.Fatal(err)
	}
	changed, err := h.ScriptFiles(context.Background(), command)
	if err != nil || changed == proof {
		t.Fatal("script change not bound", err)
	}
	other := filepath.Join(directory, "other")
	os.Mkdir(other, 0700)
	command.WorkingDirectory = other
	changedDirectory, err := h.ScriptFiles(context.Background(), command)
	if err != nil || changedDirectory == changed {
		t.Fatal("working directory not bound", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.ScriptFiles(ctx, command); err == nil {
		t.Fatal("canceled inspection accepted")
	}
	// Minimal build containers need not contain systemd.
	_, installed := os.Stat("/usr/bin/systemctl")
	if _, err := h.RuntimeExecutable(context.Background(), "systemctl"); (installed == nil && err != nil) || (os.IsNotExist(installed) && err == nil) {
		t.Fatal("unexpected installed executable evidence", err)
	}
}
func TestNativeResourceScriptUnsafeFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires actual non-root owner")
	}
	directory := t.TempDir()
	os.Chmod(directory, 0700)
	executable := filepath.Join(directory, "probe")
	command := agent.ScriptCommandConfig{Executable: executable, WorkingDirectory: directory}
	h := nativeResourceHost{}
	for _, data := range []string{"unrecognized", "#!/usr/bin/env sh\ntrue\n", "#!/missing-interpreter\ntrue\n"} {
		os.WriteFile(executable, []byte(data), 0700)
		if _, err := h.ScriptFiles(context.Background(), command); err == nil {
			t.Fatal("unsupported executable accepted")
		}
	}
	os.WriteFile(executable, []byte("#!/bin/sh\ntrue\n"), 0700)
	for _, mode := range []os.FileMode{0600, 0770, 0702, 0700 | os.ModeSetuid} {
		os.Chmod(executable, mode)
		if _, err := h.ScriptFiles(context.Background(), command); err == nil {
			t.Fatal("unsafe executable accepted", mode)
		}
	}
	os.Chmod(executable, 0700)
	link := filepath.Join(directory, "hardlink")
	if err := os.Link(executable, link); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ScriptFiles(context.Background(), command); err == nil {
		t.Fatal("hardlink accepted")
	}
	os.Remove(link)
	os.Chmod(directory, 0770)
	if _, err := h.ScriptFiles(context.Background(), command); err == nil {
		t.Fatal("writable working directory accepted")
	}
	os.Chmod(directory, 0700)
	// Do not block on special files.
	os.Remove(executable)
	if err := os.Mkdir(executable, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ScriptFiles(context.Background(), command); err == nil {
		t.Fatal("directory executable accepted")
	}
}
