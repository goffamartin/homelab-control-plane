package doctor

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

var errUnsafeFile = errors.New("unsafe diagnostic file")

type NativeHost struct{}

func (NativeHost) Platform() (string, string) { return runtime.GOOS, runtime.GOARCH }

func (NativeHost) ReadFile(path string, private bool) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errUnsafeFile
	}
	// Check every ancestor. Diagnostics reject links rather than treating a
	// dangling link as absence. A root-owned sticky temporary directory is allowed.
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !safeOwner(info) ||
			(info.Mode().Perm()&0022 != 0 && !safeSticky(info)) {
			return nil, errUnsafeFile
		}
		if parent == filepath.Dir(parent) {
			break
		}
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, errUnsafeFile
	}
	if !safeFile(before, private) {
		return nil, errUnsafeFile
	}
	file, err := openReadOnly(path)
	if err != nil {
		return nil, errUnsafeFile
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || !safeFile(opened, private) {
		return nil, errUnsafeFile
	}
	data, err := io.ReadAll(io.LimitReader(file, MaximumFileSize+1))
	if err != nil || len(data) > MaximumFileSize {
		return nil, errUnsafeFile
	}
	after, err := file.Stat()
	if err != nil || !safeFile(after, private) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return nil, errUnsafeFile
	}
	return data, nil
}

func safeFile(info os.FileInfo, private bool) bool {
	if !info.Mode().IsRegular() || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 ||
		info.Mode().Perm()&0022 != 0 || !safeOwner(info) || !singleLink(info) || info.Size() > MaximumFileSize {
		return false
	}
	return !private || info.Mode().Perm()&0077 == 0
}

func (NativeHost) Executable(name string) bool {
	path := name
	if !filepath.IsAbs(name) {
		// Match the runtime's PATH lookup but never execute the result.
		var err error
		path, err = exec.LookPath(name)
		if err != nil {
			return false
		}
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 &&
		info.Mode().Perm()&0022 == 0 && safeOwner(info)
}
