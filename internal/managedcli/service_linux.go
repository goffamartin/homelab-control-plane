package managedcli

import (
	"context"
	"fmt"
	"hlcp/deploy/systemd"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type limitedOutput struct {
	data     []byte
	overflow bool
}

func (o *limitedOutput) Write(data []byte) (int, error) {
	n := len(data)
	if len(o.data)+n > 64<<10 {
		o.overflow = true
		if len(o.data) < 64<<10 {
			o.data = append(o.data, data[:(64<<10)-len(o.data)]...)
		}
	} else {
		o.data = append(o.data, data...)
	}
	return n, nil
}
func trustedRootFile(path string, executable bool) ([]byte, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, ErrSetup
	}
	for p := filepath.Dir(resolved); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() {
			return nil, ErrSetup
		}
		s, ok := info.Sys().(*syscall.Stat_t)
		if !ok || s.Uid != 0 || info.Mode().Perm()&0022 != 0 {
			return nil, ErrSetup
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	f, err := os.Open(resolved)
	if err != nil {
		return nil, ErrSetup
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return nil, ErrSetup
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != 0 || s.Gid != 0 || s.Nlink != 1 || info.Mode().Perm()&0022 != 0 || (executable && info.Mode().Perm()&0111 == 0) {
		return nil, ErrSetup
	}
	if executable {
		return nil, nil
	}
	return os.ReadFile(resolved)
}
func fixedProcess(parent context.Context, path string, args []string, scope string) ([]byte, error) {
	if _, err := trustedRootFile(path, true); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, args...)
	command.Dir = "/"
	command.WaitDelay = 250 * time.Millisecond
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "SYSTEMD_PAGER=cat", "SYSTEMD_COLORS=0"}
	if scope == "user" {
		if os.Geteuid() == 0 {
			return nil, ErrSetup
		}
		runtime := "/run/user/" + strconv.Itoa(os.Geteuid())
		info, err := os.Lstat(runtime)
		if err != nil || checkDirectory(runtime, info, os.Geteuid(), os.Getegid()) != nil {
			return nil, ErrSetup
		}
		command.Env = append(command.Env, "XDG_RUNTIME_DIR="+runtime, "DBUS_SESSION_BUS_ADDRESS=unix:path="+runtime+"/bus")
	}
	out, stderr := &limitedOutput{}, &limitedOutput{}
	command.Stdout = out
	command.Stderr = stderr
	if command.Run() != nil || ctx.Err() != nil || out.overflow || stderr.overflow {
		return nil, ErrSetup
	}
	return out.data, nil
}
func discoverContainers(parent context.Context, name string) ([]byte, error) {
	if name != "docker" && name != "podman" {
		return nil, ErrSetup
	}
	path := "/usr/bin/" + name
	if _, err := trustedRootFile(path, true); err != nil {
		return nil, err
	}
	u, uid, _, err := caller()
	if err != nil || uid == 0 {
		return nil, ErrSetup
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "ps", "--all", "--format", "{{.Names}}")
	cmd.Dir = u.HomeDir
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "HOME=" + u.HomeDir, "XDG_RUNTIME_DIR=/run/user/" + strconv.Itoa(uid)}
	out, stderr := &limitedOutput{}, &limitedOutput{}
	cmd.Stdout = out
	cmd.Stderr = stderr
	if cmd.Run() != nil || out.overflow || stderr.overflow || ctx.Err() != nil {
		return nil, ErrSetup
	}
	return out.data, nil
}
func ensureAccount(ctx context.Context, name string) (int, int, error) {
	if name != "hlcp" && name != "hlcp-controller" {
		return 0, 0, ErrSetup
	}
	if uid, gid, err := account(name); err == nil {
		return uid, gid, nil
	}
	if os.Geteuid() != 0 {
		return 0, 0, ErrSetup
	}
	if _, err := fixedProcess(ctx, "/usr/sbin/adduser", []string{"--system", "--group", "--no-create-home", "--home", "/nonexistent", name}, ""); err != nil {
		return 0, 0, err
	}
	return account(name)
}

type serviceState struct {
	active  string
	enabled bool
}

func inspectService(ctx context.Context, scope, unit string) (serviceState, error) {
	if scope != "system" && scope != "user" {
		return serviceState{}, ErrSetup
	}
	if (scope == "system" && os.Geteuid() != 0) || (scope == "user" && os.Geteuid() == 0) {
		return serviceState{}, ErrSetup
	}
	var expected string
	serviceUser := ""
	if unit == "hlcp-controller.service" && scope == "system" {
		expected = systemd.PackagedControllerUnit()
		serviceUser = "hlcp-controller"
	} else if unit == "hlcp-agent.service" {
		var ok bool
		expected, ok = systemd.PackagedAgentUnit(scope, unit)
		if !ok {
			return serviceState{}, ErrSetup
		}
		if scope == "system" {
			serviceUser = "hlcp"
		}
	} else {
		return serviceState{}, ErrSetup
	}
	self, err := os.Executable()
	if err != nil {
		return serviceState{}, ErrSetup
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil || self != "/usr/bin/"+strings.TrimSuffix(unit, ".service") {
		return serviceState{}, ErrSetup
	}
	if _, err := trustedRootFile(self, true); err != nil {
		return serviceState{}, ErrSetup
	}
	path := "/usr/lib/systemd/" + scope + "/" + unit
	data, err := trustedRootFile(path, false)
	if err != nil || strings.ReplaceAll(string(data), "\r\n", "\n") != expected {
		return serviceState{}, ErrSetup
	}
	properties := "LoadState,ActiveState,UnitFileState,FragmentPath,DropInPaths,NeedDaemonReload,User,Group"
	args := []string{"--" + scope, "--no-pager", "--no-ask-password", "show", unit, "--property=" + properties}
	out, err := fixedProcess(ctx, "/usr/bin/systemctl", args, scope)
	if err != nil {
		return serviceState{}, err
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return serviceState{}, ErrSetup
		}
		if _, duplicate := values[key]; duplicate {
			return serviceState{}, ErrSetup
		}
		values[key] = value
	}
	if len(values) != 8 || values["LoadState"] != "loaded" || values["FragmentPath"] != path || values["DropInPaths"] != "" || values["NeedDaemonReload"] != "no" || values["User"] != serviceUser || values["Group"] != serviceUser || (values["ActiveState"] != "active" && values["ActiveState"] != "inactive") || (values["UnitFileState"] != "enabled" && values["UnitFileState"] != "disabled") {
		return serviceState{}, ErrSetup
	}
	return serviceState{active: values["ActiveState"], enabled: values["UnitFileState"] == "enabled"}, nil
}
func activateService(ctx context.Context, scope, unit string, enable bool) error {
	state, err := inspectService(ctx, scope, unit)
	if err != nil {
		return err
	}
	if state.active == "inactive" {
		if _, err := fixedProcess(ctx, "/usr/bin/systemctl", []string{"--" + scope, "--no-ask-password", "start", unit}, scope); err != nil {
			return fmt.Errorf("service start uncertain: %w", err)
		}
	}
	state, err = inspectService(ctx, scope, unit)
	if err != nil || state.active != "active" {
		return ErrSetup
	}
	if enable && !state.enabled {
		if _, err := fixedProcess(ctx, "/usr/bin/systemctl", []string{"--" + scope, "--no-ask-password", "enable", unit}, scope); err != nil {
			return fmt.Errorf("service enable uncertain: %w", err)
		}
	}
	state, err = inspectService(ctx, scope, unit)
	if err != nil || state.active != "active" || (enable && !state.enabled) {
		return ErrSetup
	}
	return nil
}
