//go:build linux

package setup

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type nativeSource struct {
	directory       *os.File
	owner           Owner
	path            string
	trustOwnerGroup bool
}

func openNativeSource(directory string, owner Owner) (Source, error) {
	return openNativeSourceWithTrust(directory, owner, false)
}
func openNativeSourceWithTrust(directory string, owner Owner, trust bool) (Source, error) {
	if trust && (owner.UID == 0 || owner.GID == 0) {
		return nil, ErrUnsafePath
	}
	if len(directory) > 4096 || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || directory == "/" || strings.ContainsRune(directory, 0) {
		return nil, ErrUnsafePath
	}
	if os.Geteuid() != 0 && uint32(os.Geteuid()) != owner.UID {
		return nil, ErrUnsafePath
	}
	account, err := user.LookupId(strconv.FormatUint(uint64(owner.UID), 10))
	if err != nil || account.Gid != strconv.FormatUint(uint64(owner.GID), 10) {
		return nil, ErrUnsafePath
	}
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrReadFailed
	}
	for _, component := range strings.Split(strings.TrimPrefix(directory, "/"), "/") {
		next, openErr := syscall.Openat(fd, component, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		_ = syscall.Close(fd)
		if openErr != nil {
			if errors.Is(openErr, syscall.ENOENT) {
				return nil, ErrMissingDirectory
			}
			if errors.Is(openErr, syscall.ELOOP) || errors.Is(openErr, syscall.ENOTDIR) {
				return nil, ErrUnsafePath
			}
			return nil, ErrReadFailed
		}
		fd = next
		var stat syscall.Stat_t
		if syscall.Fstat(fd, &stat) != nil {
			_ = syscall.Close(fd)
			return nil, ErrReadFailed
		}
		trustedOwner := stat.Uid == 0 || stat.Uid == owner.UID
		writable := stat.Mode&0o022 != 0
		if trust && stat.Uid == owner.UID && stat.Gid == owner.GID && stat.Mode&0002 == 0 {
			writable = false
		}
		sticky := stat.Uid == 0 && stat.Mode&syscall.S_ISVTX != 0
		if !trustedOwner || writable && !sticky {
			_ = syscall.Close(fd)
			return nil, ErrUnsafePath
		}
	}
	var stat syscall.Stat_t
	if syscall.Fstat(fd, &stat) != nil {
		_ = syscall.Close(fd)
		return nil, ErrReadFailed
	}
	if stat.Uid != owner.UID || stat.Gid != owner.GID || stat.Mode&0o7777 != 0o700 {
		_ = syscall.Close(fd)
		return nil, ErrUnsafePath
	}
	return &nativeSource{directory: os.NewFile(uintptr(fd), "setup-directory"), owner: owner, path: directory, trustOwnerGroup: trust}, nil
}

func (source *nativeSource) Owner() Owner { return source.owner }
func (source *nativeSource) Close() error { return source.directory.Close() }

func (source *nativeSource) Read(name string, limit int64) (Snapshot, error) {
	if !validName(name) || limit <= 0 || limit > MaximumFileBytes {
		return Snapshot{}, ErrUnsafePath
	}
	var directory syscall.Stat_t
	if syscall.Fstat(int(source.directory.Fd()), &directory) != nil {
		return Snapshot{}, ErrReadFailed
	}
	if directory.Uid != source.owner.UID || directory.Gid != source.owner.GID || directory.Mode&0o7777 != 0o700 {
		return Snapshot{}, ErrUnsafePath
	}
	fd, err := syscall.Openat(int(source.directory.Fd()), name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if errors.Is(err, syscall.ENOENT) {
		return Snapshot{}, nil
	}
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return Snapshot{}, ErrUnsafePath
		}
		return Snapshot{}, ErrReadFailed
	}
	file := os.NewFile(uintptr(fd), "setup-input")
	defer file.Close()
	var stat syscall.Stat_t
	if syscall.Fstat(fd, &stat) != nil {
		return Snapshot{}, ErrReadFailed
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFREG || stat.Mode&0o7022 != 0 || stat.Nlink != 1 || stat.Uid != source.owner.UID || stat.Gid != source.owner.GID {
		return Snapshot{}, ErrUnsafePath
	}
	if stat.Size > limit {
		return Snapshot{}, ErrInvalidExisting
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return Snapshot{}, ErrReadFailed
	}
	if int64(len(data)) > limit {
		return Snapshot{}, ErrInvalidExisting
	}
	return Snapshot{Exists: true, Data: data, Mode: fs.FileMode(stat.Mode & 0o777), Owner: Owner{UID: stat.Uid, GID: stat.Gid}, Identity: FileIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}}, nil
}
