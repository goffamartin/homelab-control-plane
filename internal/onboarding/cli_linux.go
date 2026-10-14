package onboarding

import (
	"hlcp/internal/setup"
	"io"
	"os"
	"os/user"
	"strconv"
	"syscall"
	"unsafe"
)

func (nativeSetupHost) Owner() (setup.Owner, error) {
	uid, gid := os.Geteuid(), os.Getegid()
	account, err := user.LookupId(strconv.Itoa(uid))
	if err != nil || account.Uid != strconv.Itoa(uid) || account.Gid != strconv.Itoa(gid) {
		return setup.Owner{}, ErrAgentOptions
	}
	return setup.Owner{UID: uint32(uid), GID: uint32(gid)}, nil
}
func terminalInput(input io.Reader) bool {
	file, ok := input.(*os.File)
	if !ok {
		return false
	}
	var state syscall.Termios
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&state)))
	return err == 0
}
