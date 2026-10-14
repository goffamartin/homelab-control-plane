package doctor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func selectedCredential(name string) (*syscall.Credential, error) {
	if name == "" || len(name) > 128 || strings.ContainsRune(name, 0) {
		return nil, errUser
	}
	account, err := user.Lookup(name)
	if err != nil {
		return nil, errUser
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil || uid == 0 {
		return nil, errUser
	}
	gid, err := strconv.ParseUint(account.Gid, 10, 32)
	if err != nil {
		return nil, errUser
	}
	reverse, err := user.LookupId(account.Uid)
	if err != nil || reverse.Username != account.Username || reverse.Gid != account.Gid {
		return nil, errUser
	}
	if os.Geteuid() != 0 && uint32(os.Geteuid()) != uint32(uid) {
		return nil, errUser
	}
	ids, err := account.GroupIds()
	if err != nil || len(ids) > 128 {
		return nil, errUser
	}
	groups := []uint32{uint32(gid)}
	seen := map[uint32]bool{uint32(gid): true}
	for _, value := range ids {
		id, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return nil, errUser
		}
		if !seen[uint32(id)] {
			groups = append(groups, uint32(id))
			seen[uint32(id)] = true
		}
	}
	return &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: groups}, nil
}

func (NativeHost) DiagnoseUser(options Options, now time.Time) (Report, error) {
	if options.Role != "agent" || options.Scope != "user" {
		return Report{}, errUser
	}
	credential, err := selectedCredential(options.User)
	if err != nil {
		return Report{}, errUser
	}
	options.User = ""
	if os.Geteuid() != 0 {
		// The caller already has this account's actual process permissions.
		return Run(NativeHost{}, options, now), nil
	}
	executable, err := os.Executable()
	if err != nil {
		return Report{}, errUser
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil || !trustedProbeExecutable(executable) {
		return Report{}, errUser
	}
	// Only our root-owned installed executable; never a selected user's script.
	data, exit, err := runAccountProcess(executable, userArguments(options), credential)
	if err != nil {
		return Report{}, errUser
	}
	return decodeUserReport(data, exit)
}

func runAccountProcess(executable string, args []string, credential *syscall.Credential) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = "/"
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
	command.SysProcAttr = &syscall.SysProcAttr{Credential: credential, Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 250 * time.Millisecond
	stdout, stderr := &boundedProbeWriter{limit: maximumUserReport}, &boundedProbeWriter{}
	command.Stdout, command.Stderr = stdout, stderr
	err := command.Run()
	if ctx.Err() != nil || stdout.overflow || stderr.overflow {
		return nil, 0, errUser
	}
	exit := 0
	if err != nil {
		var status *exec.ExitError
		if !errors.As(err, &status) {
			return nil, 0, errUser
		}
		exit = status.ExitCode()
	}
	return stdout.data, exit, nil // Never forward child stderr or raw process errors.
}
