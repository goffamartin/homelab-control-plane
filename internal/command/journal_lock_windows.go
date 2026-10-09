package command

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var lockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")

func acquireJournalLock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	var overlapped syscall.Overlapped
	result, _, failure := lockFileEx.Call(file.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if result == 0 {
		file.Close()
		return nil, fmt.Errorf("journal is already owned or cannot be locked: %w", failure)
	}
	return file, nil
}

// Windows supports development tests; power-loss durability is guaranteed only
// for the reference Linux deployment, which syncs the renamed file's directory.
func syncJournalDirectory(string) error { return nil }
