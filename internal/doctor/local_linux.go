package doctor

import (
	"bytes"
	"context"
	"hlcp/internal/setup"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func (NativeHost) InspectSetup(directory string) (setup.TransactionStatus, error) {
	planner, err := setup.OpenPlanner(directory, setup.Owner{UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())})
	if err != nil {
		return setup.TransactionStatus{}, err
	}
	defer planner.Close()
	// Inspect has no namespace writes, locks, recovery or backup disposal.
	return planner.Inspect(context.Background())
}

func (NativeHost) Script(executable, directory string) ScriptStatus {
	status := ScriptStatus{Directory: safeWorkingDirectory(directory)}
	header, ok := executableHeader(executable)
	status.Executable = ok
	if !ok {
		return status
	}
	if bytes.HasPrefix(header, []byte{0x7f, 'E', 'L', 'F'}) {
		status.Format = "binary"
		return status
	}
	if !bytes.HasPrefix(header, []byte("#!")) {
		return status
	}
	newline := bytes.IndexByte(header, '\n')
	if newline < 0 {
		return status
	} // Bounded header, do not read arbitrary contents.
	line := string(header[2:newline])
	if strings.ContainsRune(line, '\r') || strings.ContainsRune(line, 0) {
		return status
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return status
	}
	interpreter := fields[0]
	if _, ok := executableHeader(interpreter); !ok {
		return status
	}
	resolved, err := filepath.EvalSymlinks(interpreter)
	if err != nil {
		return status
	}
	if resolved == "/usr/bin/env" || resolved == "/bin/env" {
		status.Format = "indirect"
	} else {
		status.Format = "interpreter"
	}
	return status
}

func safeWorkingDirectory(path string) bool {
	if !cleanAbsolute(path) {
		return false
	}
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && safeOwner(info) && info.Mode().Perm()&0022 == 0 &&
		searchAccess(info) && safeScriptParents(path)
}

func cleanAbsolute(path string) bool {
	return len(path) <= 4096 && filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsRune(path, 0)
}

func safeScriptParents(path string) bool {
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || !safeOwner(info) || !searchAccess(info) ||
			(info.Mode().Perm()&0022 != 0 && !safeSticky(info)) {
			return false
		}
		if parent == filepath.Dir(parent) {
			return true
		}
	}
}

// POSIX mode checks for the effective account; ACLs/mount noexec and races are
// deliberately not interpreted as a guarantee that execve will succeed.
func searchAccess(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	if os.Geteuid() == 0 {
		return info.IsDir() || info.Mode().Perm()&0111 != 0
	}
	bit := os.FileMode(0001)
	if stat.Uid == uint32(os.Geteuid()) {
		bit = 0100
	} else {
		groups, err := os.Getgroups()
		if err != nil {
			return false
		}
		if stat.Gid == uint32(os.Getegid()) {
			bit = 0010
		}
		for _, gid := range groups {
			if stat.Gid == uint32(gid) {
				bit = 0010
			}
		}
	}
	return info.Mode().Perm()&bit != 0
}

func executableHeader(path string) ([]byte, bool) {
	if !cleanAbsolute(path) {
		return nil, false
	}
	// Standard distro interpreter links (/bin/sh etc.) may be used. Require
	// safe original parent and resolved path; never use PATH or configured env.
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil || !safeWorkingDirectory(parent) {
		return nil, false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !safeScriptParents(resolved) {
		return nil, false
	}
	before, err := os.Lstat(resolved)
	if err != nil || !safeExecutableFile(before) {
		return nil, false
	}
	file, err := openReadOnly(resolved)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || !safeExecutableFile(opened) {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(file, 256))
	after, statErr := file.Stat()
	if err != nil || statErr != nil || !safeExecutableFile(after) || opened.Size() != after.Size() ||
		!opened.ModTime().Equal(after.ModTime()) {
		return nil, false
	}
	return data, true
}

func safeExecutableFile(info os.FileInfo) bool {
	return info.Mode().IsRegular() && safeOwner(info) && singleLink(info) &&
		info.Mode().Perm()&0022 == 0 && searchAccess(info) &&
		info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0
}
