package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

type nativeDirectoryPlan struct {
	parent                 *os.File
	parentPath, path, name string
	owner                  Owner
	stage                  string
	fault                  func(string) error
}

func pinParent(path string, owner Owner) (*os.File, error) {
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrReadFailed
	}
	if path != "/" {
		for _, component := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
			next, err := syscall.Openat(fd, component, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
			syscall.Close(fd)
			if err != nil {
				if errors.Is(err, syscall.ENOENT) {
					return nil, ErrMissingParent
				}
				return nil, ErrUnsafePath
			}
			fd = next
			if !safeDirectoryParent(fd, owner) {
				syscall.Close(fd)
				return nil, ErrUnsafePath
			}
		}
	}
	if !safeDirectoryParent(fd, owner) {
		syscall.Close(fd)
		return nil, ErrUnsafePath
	}
	return os.NewFile(uintptr(fd), "setup-parent"), nil
}
func safeDirectoryParent(fd int, owner Owner) bool {
	var stat syscall.Stat_t
	if syscall.Fstat(fd, &stat) != nil {
		return false
	}
	return (stat.Uid == 0 || stat.Uid == owner.UID) &&
		(stat.Mode&0022 == 0 || (stat.Uid == 0 && stat.Mode&syscall.S_ISVTX != 0))
}
func planDirectory(path string, owner Owner) (directorySource, error) {
	if len(path) > 4096 || !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || strings.ContainsRune(path, 0) ||
		uint32(os.Geteuid()) != owner.UID || uint32(os.Getegid()) != owner.GID {
		return nil, ErrUnsafePath
	}
	account, err := user.LookupId(strconv.FormatUint(uint64(owner.UID), 10))
	if err != nil || account.Gid != strconv.FormatUint(uint64(owner.GID), 10) {
		return nil, ErrUnsafePath
	}
	parentPath := filepath.Dir(path)
	parent, err := pinParent(parentPath, owner)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(path)
	if !absentChild(parent, name) {
		parent.Close()
		return nil, ErrStalePlan
	}
	digest := sha256.Sum256([]byte(name))
	stage := ".hlcp-directory-" + hex.EncodeToString(digest[:])
	if !absentChild(parent, stage) {
		parent.Close()
		return nil, ErrRecoveryRequired
	}
	return &nativeDirectoryPlan{parent: parent, parentPath: parentPath, path: path, name: name, owner: owner, stage: stage}, nil
}
func absentChild(parent *os.File, name string) bool {
	// O_PATH|NOFOLLOW inspects existence without opening FIFOs/devices or following links.
	fd, err := syscall.Openat(int(parent.Fd()), name, 0x200000|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err == nil {
		syscall.Close(fd)
		return false
	}
	return errors.Is(err, syscall.ENOENT)
}
func (p *nativeDirectoryPlan) close() error { return p.parent.Close() }
func (p *nativeDirectoryPlan) create(ctx context.Context) (Source, error) {
	if !transactionArchitectureSupported {
		return nil, ErrApplyUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, err := pinParent(p.parentPath, p.owner)
	if err != nil {
		return nil, err
	}
	before, e1 := p.parent.Stat()
	after, e2 := current.Stat()
	current.Close()
	if e1 != nil || e2 != nil || !os.SameFile(before, after) || !safeDirectoryParent(int(p.parent.Fd()), p.owner) {
		return nil, ErrStalePlan
	}
	if !absentChild(p.parent, p.name) {
		return nil, ErrStalePlan
	}
	if err := syscall.Mkdirat(int(p.parent.Fd()), p.stage, 0700); err != nil {
		if errors.Is(err, syscall.EEXIST) {
			return nil, ErrRecoveryRequired
		}
		return nil, ErrWriteFailed
	}
	// Only the newly created entry is touched. Never adopt/chmod an EEXIST entry.
	fd, err := syscall.Openat(int(p.parent.Fd()), p.stage, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrWriteFailed
	}
	file := os.NewFile(uintptr(fd), "created-setup-directory")
	keep := false
	published := false
	defer func() {
		if !keep {
			if !published {
				_ = p.removeEmptyStage(file)
			}
			file.Close()
		}
	}()
	var stat syscall.Stat_t
	if syscall.Fstat(fd, &stat) != nil || stat.Uid != p.owner.UID {
		return nil, ErrUnsafePath
	}
	// Clear inherited setgid and set the explicitly validated primary group.
	if syscall.Fchown(fd, -1, int(p.owner.GID)) != nil || syscall.Fchmod(fd, 0700) != nil {
		return nil, ErrWriteFailed
	}
	if syscall.Fstat(fd, &stat) != nil || stat.Uid != p.owner.UID || stat.Gid != p.owner.GID || stat.Mode&07777 != 0700 {
		return nil, ErrUnsafePath
	}
	if file.Sync() != nil || p.parent.Sync() != nil {
		return nil, ErrWriteFailed
	}
	if p.fault != nil {
		if err := p.fault("before-publish"); err != nil {
			return nil, ErrWriteFailed
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, err = pinParent(p.parentPath, p.owner)
	if err != nil {
		return nil, err
	}
	after, e2 = current.Stat()
	current.Close()
	if e2 != nil || !os.SameFile(before, after) {
		return nil, ErrStalePlan
	}
	if !p.matchesStage(file) {
		return nil, ErrStalePlan
	}
	oldPointer, _ := syscall.BytePtrFromString(p.stage)
	newPointer, _ := syscall.BytePtrFromString(p.name)
	_, _, errno := syscall.Syscall6(renameat2Number, uintptr(p.parent.Fd()), uintptr(unsafe.Pointer(oldPointer)), uintptr(p.parent.Fd()), uintptr(unsafe.Pointer(newPointer)), 1, 0)
	if errno != 0 {
		if errno == syscall.EEXIST {
			return nil, ErrStalePlan
		}
		return nil, ErrWriteFailed
	}
	published = true
	if p.fault != nil {
		if err := p.fault("after-publish"); err != nil {
			return nil, ErrWriteFailed
		}
	}
	source := &nativeSource{directory: file, owner: p.owner, path: p.path}
	if err := source.binding(); err != nil {
		return nil, ErrStalePlan
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if file.Sync() != nil || p.parent.Sync() != nil {
		return nil, ErrWriteFailed
	}
	keep = true
	return source, nil
}

func (p *nativeDirectoryPlan) removeEmptyStage(file *os.File) error {
	if !safeDirectoryParent(int(p.parent.Fd()), p.owner) {
		return ErrRecoveryRequired
	}
	fd, err := syscall.Openat(int(p.parent.Fd()), p.stage, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return ErrRecoveryRequired
	}
	named := os.NewFile(uintptr(fd), "staging-directory")
	defer named.Close()
	a, e1 := file.Stat()
	b, e2 := named.Stat()
	if e1 != nil || e2 != nil || !os.SameFile(a, b) || !p.privateDirectory(a) {
		return ErrRecoveryRequired
	}
	pointer, _ := syscall.BytePtrFromString(p.stage)
	_, _, errno := syscall.Syscall(syscall.SYS_UNLINKAT, uintptr(p.parent.Fd()), uintptr(unsafe.Pointer(pointer)), 0x200)
	if errno != 0 {
		return ErrRecoveryRequired
	}
	if p.parent.Sync() != nil {
		return ErrWriteFailed
	}
	return nil
}

func (p *nativeDirectoryPlan) privateDirectory(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == p.owner.UID && stat.Gid == p.owner.GID && stat.Mode&07777 == 0700
}
func (p *nativeDirectoryPlan) matchesStage(file *os.File) bool {
	fd, err := syscall.Openat(int(p.parent.Fd()), p.stage, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	named := os.NewFile(uintptr(fd), "staging-identity")
	defer named.Close()
	a, e1 := file.Stat()
	b, e2 := named.Stat()
	return e1 == nil && e2 == nil && os.SameFile(a, b) && p.privateDirectory(a)
}
