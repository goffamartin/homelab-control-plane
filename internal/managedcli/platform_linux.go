package managedcli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

func offlineImage() bool {
	if _, e := os.Lstat("/run/systemd/system"); !os.IsNotExist(e) {
		return false
	}
	data, e := os.ReadFile("/proc/1/comm")
	return e == nil && strings.TrimSpace(string(data)) != "systemd"
}

func isTerminal(input io.Reader) bool {
	f, ok := input.(*os.File)
	if !ok {
		return false
	}
	var state syscall.Termios
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&state)))
	return err == 0
}
func hideEcho(input io.Reader) (func(), error) {
	f, ok := input.(*os.File)
	if !ok || !isTerminal(input) {
		return func() {}, nil
	}
	var saved syscall.Termios
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&saved)))
	if err != 0 {
		return nil, ErrSetup
	}
	state := saved
	state.Lflag &^= syscall.ECHO
	_, _, err = syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCSETS, uintptr(unsafe.Pointer(&state)))
	if err != 0 {
		return nil, ErrSetup
	}
	return func() {
		_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCSETS, uintptr(unsafe.Pointer(&saved)))
	}, nil
}
func checkDirectory(path string, info os.FileInfo, uid, gid int) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0700 || stat.Uid != uint32(uid) || stat.Gid != uint32(gid) {
		return ErrSetup
	}
	if resolved, err := filepath.EvalSymlinks(path); err != nil || resolved != path {
		return ErrSetup
	}
	return nil
}
func safeParent(path string, uid int) error {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() {
			return ErrSetup
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (stat.Uid != 0 && stat.Uid != uint32(uid)) || info.Mode().Perm()&0022 != 0 {
			return ErrSetup
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return ErrSetup
	}
	return nil
}
