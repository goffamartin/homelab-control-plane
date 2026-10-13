//go:build !linux

package localapi

import (
	"os"
	"runtime"
)

func privateCredential(_ *os.File, info os.FileInfo) bool {
	return runtime.GOOS == "windows" || info.Mode().Perm()&0077 == 0
}
