package doctor

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func (NativeHost) ClockSynced() (bool, error) {
	state := syscall.Timex{} // Modes=0 is an unprivileged query, not a clock adjustment.
	_, err := syscall.Adjtimex(&state)
	if err != nil {
		return false, errProbe
	}
	return state.Status&0x40 == 0, nil // STA_UNSYNC
}

func (NativeHost) Account() (Account, error) {
	uid := strconv.Itoa(os.Geteuid())
	account, err := user.LookupId(uid)
	if err != nil || account.Uid != uid || account.Gid != strconv.Itoa(os.Getegid()) {
		return Account{}, errProbe
	}
	return Account{Name: account.Username, UID: uid, GID: account.Gid}, nil
}

func (NativeHost) Properties(query Query) (map[string]string, error) {
	path := "/usr/bin/systemctl"
	args := []string{"--no-pager", "--no-ask-password", "--all"}
	fields := []string{"LoadState", "ActiveState", "UnitFileState", "FragmentPath", "DropInPaths"}
	switch query.Kind {
	case "unit":
		if query.Unit != "hlcp-agent.service" && query.Unit != "hlcp-agent-podman.service" && query.Unit != "hlcp-controller.service" {
			return nil, errProbe
		}
		if query.Scope == "user" {
			if os.Geteuid() == 0 || query.UID != strconv.Itoa(os.Geteuid()) {
				return nil, errProbe
			}
			args = append(args, "--user")
		} else if query.Scope != "system" {
			return nil, errProbe
		}
		args = append(args, "show", query.Unit, "--property=LoadState,ActiveState,UnitFileState,FragmentPath,DropInPaths")
	case "manager", "linger":
		uid, err := strconv.ParseUint(query.UID, 10, 32)
		if err != nil || uid == 0 || query.UID != strconv.Itoa(os.Geteuid()) {
			return nil, errProbe
		}
		if query.Kind == "linger" {
			path = "/usr/bin/loginctl"
			args = []string{"--no-pager", "--no-ask-password", "--all", "show-user", query.UID, "--property=Linger"}
			fields = []string{"Linger"}
		} else {
			args = append(args, "show", "user@"+query.UID+".service", "--property=ActiveState")
			fields = []string{"ActiveState"}
		}
	default:
		return nil, errProbe
	}
	// Fixed distro tools only; never execute a PATH-selected/user-owned program
	// with privileges. Root-owned immutable ancestors are required too.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !trustedProbeExecutable(resolved) {
		return nil, errProbe
	}
	env := []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "SYSTEMD_PAGER=cat", "SYSTEMD_COLORS=0"}
	if query.Scope == "user" {
		runtime := "/run/user/" + query.UID
		info, err := os.Lstat(runtime)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return nil, errProbe
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Geteuid()) {
			return nil, errProbe
		}
		env = append(env, "XDG_RUNTIME_DIR="+runtime, "DBUS_SESSION_BUS_ADDRESS=unix:path="+runtime+"/bus")
	}
	data, err := runProbe(resolved, args, env)
	if err != nil {
		return nil, errProbe
	}
	return parseProperties(data, fields)
}

func trustedProbeExecutable(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 ||
		info.Mode().Perm()&0022 != 0 || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return false
	}
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err = os.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
			return false
		}
		stat, ok = info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 {
			return false
		}
		if parent == filepath.Dir(parent) {
			return true
		}
	}
}

type boundedProbeWriter struct {
	data     []byte
	overflow bool
}

func (w *boundedProbeWriter) Write(p []byte) (int, error) {
	count := len(p)
	remaining := maximumProbeOutput - len(w.data)
	if len(p) > remaining {
		w.overflow = true
		p = p[:remaining]
	}
	w.data = append(w.data, p...)
	return count, nil
}

func runProbe(path string, args, env []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, args...)
	command.Env = env // Never inherit credentials, HOME, SUDO_USER or remote bus addresses.
	command.Dir = "/"
	command.WaitDelay = 250 * time.Millisecond
	stdout, stderr := &boundedProbeWriter{}, &boundedProbeWriter{}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil || ctx.Err() != nil || stdout.overflow || stderr.overflow {
		return nil, errProbe
	}
	return stdout.data, nil
}
