package onboarding

import (
	"errors"
	"hlcp/internal/setup"
	"path/filepath"
	"strings"
	"syscall"
)

// Runs only in the credential-dropped child. Paths are inspected descriptor by
// descriptor, without following links, and never chmod/chowned.
func inspectClientPath(directory string, owner setup.Owner, trust bool) *clientGrantReport {
	fail := func(code, path string, gid uint32) *clientGrantReport {
		r := grantFailure(code, true)
		r.Path = path
		r.Group = gid
		return &r
	}
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fail("USER_PATH_UNAVAILABLE", "/", 0)
	}
	defer func() { syscall.Close(fd) }()
	path := ""
	for _, component := range strings.Split(strings.TrimPrefix(directory, "/"), "/") {
		path += "/" + component
		next, err := syscall.Openat(fd, component, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if errors.Is(err, syscall.ENOENT) {
			return nil
		}
		if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.ENOTDIR) {
			return fail("SYMLINK_OR_NON_DIRECTORY", path, 0)
		}
		if err != nil {
			return fail("USER_PATH_UNAVAILABLE", path, 0)
		}
		syscall.Close(fd)
		fd = next
		var stat syscall.Stat_t
		if syscall.Fstat(fd, &stat) != nil {
			return fail("USER_PATH_UNAVAILABLE", path, 0)
		}
		if path == directory {
			if stat.Uid != owner.UID || stat.Gid != owner.GID || stat.Mode&07777 != 0700 {
				return fail("PRIVATE_PROFILE_PERMISSIONS", path, stat.Gid)
			}
			continue
		}
		if stat.Uid != 0 && stat.Uid != owner.UID {
			return fail("UNEXPECTED_PARENT_OWNER", path, stat.Gid)
		}
		if stat.Uid == 0 && stat.Mode&syscall.S_ISVTX != 0 {
			continue
		}
		if stat.Mode&0002 != 0 {
			return fail("WORLD_WRITABLE_PARENT", path, stat.Gid)
		}
		if stat.Mode&0020 != 0 {
			if stat.Uid != owner.UID || stat.Gid != owner.GID {
				return fail("UNTRUSTED_PARENT_GROUP", path, stat.Gid)
			}
			if !trust {
				return fail("GROUP_WRITABLE_PARENT", path, stat.Gid)
			}
		}
	}
	return nil
}

func validClientPathFailure(r clientGrantReport, directory string) bool {
	if r.Status != "failed" || !r.ReadOnly || r.Preview != nil || r.Result != nil {
		return false
	}
	switch r.Code {
	case "GROUP_WRITABLE_PARENT", "WORLD_WRITABLE_PARENT", "UNTRUSTED_PARENT_GROUP", "UNEXPECTED_PARENT_OWNER", "PRIVATE_PROFILE_PERMISSIONS", "USER_PATH_UNAVAILABLE", "SYMLINK_OR_NON_DIRECTORY":
		return r.Path != "" && filepath.Clean(r.Path) == r.Path && !strings.ContainsAny(r.Path, "\x00\r\n") && (r.Path == directory || strings.HasPrefix(directory, r.Path+"/") || r.Path == "/")
	case "PROFILE_CONFLICT", "PROFILE_PLAN_REFUSED":
		return r.Path == "" && r.Group == 0
	}
	return false
}
