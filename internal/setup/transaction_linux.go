//go:build linux

package setup

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"sort"
	"syscall"
	"unsafe"
)

func (source *nativeSource) lock() (func(), error) {
	if !transactionArchitectureSupported {
		return nil, ErrApplyUnavailable
	}
	if err := syscall.Flock(int(source.directory.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, ErrLocked
	}
	return func() { _ = syscall.Flock(int(source.directory.Fd()), syscall.LOCK_UN) }, nil
}

func (source *nativeSource) binding() error {
	other, err := openNativeSourceWithTrust(source.path, source.owner, source.trustOwnerGroup)
	if err != nil {
		return ErrStalePlan
	}
	defer other.Close()
	current, err := source.rootIdentity()
	if err != nil {
		return err
	}
	reopened, err := other.(*nativeSource).rootIdentity()
	if err != nil || current != reopened {
		return ErrStalePlan
	}
	return nil
}

func (source *nativeSource) rootIdentity() (FileIdentity, error) {
	var stat syscall.Stat_t
	if syscall.Fstat(int(source.directory.Fd()), &stat) != nil {
		return FileIdentity{}, ErrReadFailed
	}
	return FileIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}, nil
}

func validArea(area string) bool { return area == "" || area == transactionArea || area == backupArea }
func validEntry(area, name string) bool {
	return validArea(area) && (validName(name) || area == "" && (name == transactionArea || name == backupArea))
}

func (source *nativeSource) openArea(area string) (*os.File, error) {
	if !validArea(area) {
		return nil, ErrUnsafePath
	}
	name := area
	if name == "" {
		name = "."
	}
	fd, err := syscall.Openat(int(source.directory.Fd()), name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return nil, os.ErrNotExist
		}
		return nil, ErrUnsafePath
	}
	file := os.NewFile(uintptr(fd), "setup-area")
	var stat syscall.Stat_t
	if syscall.Fstat(fd, &stat) != nil || stat.Uid != source.owner.UID || stat.Gid != source.owner.GID || stat.Mode&0o7777 != 0o700 {
		file.Close()
		return nil, ErrUnsafePath
	}
	return file, nil
}

func (source *nativeSource) areaExists(area string) (bool, error) {
	file, err := source.openArea(area)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	file.Close()
	return true, nil
}

func (source *nativeSource) makeArea(area string) error {
	if area != transactionArea {
		return ErrUnsafePath
	}
	if err := source.binding(); err != nil {
		return err
	}
	if err := syscall.Mkdirat(int(source.directory.Fd()), area, 0o700); err != nil {
		return ErrWriteFailed
	}
	// A new directory can be root-owned when explicitly provisioning another
	// account. Apply ownership only to this just-created exclusive directory.
	fd, err := syscall.Openat(int(source.directory.Fd()), area, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return ErrWriteFailed
	}
	defer syscall.Close(fd)
	if err := syscall.Fchown(fd, int(source.owner.UID), int(source.owner.GID)); err != nil {
		return ErrWriteFailed
	}
	if err := syscall.Fchmod(fd, 0o700); err != nil {
		return ErrWriteFailed
	}
	return transactionErrorOrNil(syscall.Fsync(fd))
}

func (source *nativeSource) readArea(area, name string) (Snapshot, error) {
	if !validEntry(area, name) {
		return Snapshot{}, ErrUnsafePath
	}
	directory, err := source.openArea(area)
	if err != nil {
		return Snapshot{}, err
	}
	defer directory.Close()
	reader := &nativeSource{directory: directory, owner: source.owner}
	return reader.Read(name, MaximumFileBytes)
}

func (source *nativeSource) writeExclusive(area, name string, data []byte, mode fs.FileMode) (created Snapshot, resultErr error) {
	if area == "" || !validEntry(area, name) || !validMode(mode) || len(data) > MaximumFileBytes {
		return Snapshot{}, ErrUnsafePath
	}
	directory, err := source.openArea(area)
	if err != nil {
		return Snapshot{}, err
	}
	defer directory.Close()
	fd, err := syscall.Openat(int(directory.Fd()), name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return Snapshot{}, ErrWriteFailed
	}
	file := os.NewFile(uintptr(fd), "setup-staged-file")
	defer file.Close()
	var identity syscall.Stat_t
	if syscall.Fstat(fd, &identity) != nil {
		return Snapshot{}, ErrWriteFailed
	}
	created = Snapshot{Exists: true, Identity: FileIdentity{Device: uint64(identity.Dev), Inode: identity.Ino}, Mode: 0o600, Owner: source.owner}
	if file.Chown(int(source.owner.UID), int(source.owner.GID)) != nil {
		return created, ErrWriteFailed
	}
	written, err := file.Write(data)
	if err != nil || written != len(data) {
		return created, ErrWriteFailed
	}
	if file.Chmod(mode) != nil || file.Sync() != nil {
		return created, ErrWriteFailed
	}
	if err := file.Close(); err != nil {
		return created, ErrWriteFailed
	}
	if err := directory.Sync(); err != nil {
		return created, ErrWriteFailed
	}
	current, err := source.readArea(area, name)
	if err != nil {
		return created, err
	}
	if current.Identity != created.Identity {
		return created, ErrRecoveryConflict
	}
	return current, nil
}

func (source *nativeSource) rename(fromArea, fromName, toArea, toName string, flags uintptr) error {
	if !validEntry(fromArea, fromName) || !validEntry(toArea, toName) {
		return ErrUnsafePath
	}
	from, err := source.openArea(fromArea)
	if err != nil {
		return err
	}
	defer from.Close()
	to, err := source.openArea(toArea)
	if err != nil {
		return err
	}
	defer to.Close()
	oldPointer, err := syscall.BytePtrFromString(fromName)
	if err != nil {
		return ErrUnsafePath
	}
	newPointer, err := syscall.BytePtrFromString(toName)
	if err != nil {
		return ErrUnsafePath
	}
	// Linux UAPI: NOREPLACE=1, EXCHANGE=2. No fallback to overwriting rename.
	_, _, errno := syscall.Syscall6(renameat2Number, uintptr(from.Fd()), uintptr(unsafe.Pointer(oldPointer)), uintptr(to.Fd()), uintptr(unsafe.Pointer(newPointer)), flags, 0)
	if errno != 0 {
		return ErrWriteFailed
	}
	return nil
}

func (source *nativeSource) move(fromArea, fromName, toArea, toName string) error {
	return source.rename(fromArea, fromName, toArea, toName, 1)
}
func (source *nativeSource) exchange(fromArea, fromName, toArea, toName string) error {
	return source.rename(fromArea, fromName, toArea, toName, 2)
}

func (source *nativeSource) syncArea(area string) error {
	file, err := source.openArea(area)
	if err != nil {
		return err
	}
	defer file.Close()
	return transactionErrorOrNil(file.Sync())
}

func (source *nativeSource) syncFile(area, name string) error {
	previous, err := source.readArea(area, name)
	if err != nil || !previous.Exists {
		return ErrReadFailed
	}
	directory, err := source.openArea(area)
	if err != nil {
		return err
	}
	defer directory.Close()
	fd, err := syscall.Openat(int(directory.Fd()), name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return ErrWriteFailed
	}
	defer syscall.Close(fd)
	var info syscall.Stat_t
	if syscall.Fstat(fd, &info) != nil || (FileIdentity{Device: uint64(info.Dev), Inode: info.Ino}) != previous.Identity {
		return ErrStalePlan
	}
	return transactionErrorOrNil(syscall.Fsync(fd))
}

func (source *nativeSource) listArea(area string) ([]string, error) {
	file, err := source.openArea(area)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	names, err := file.Readdirnames(MaximumFiles + 3)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, ErrReadFailed
	}
	if len(names) > MaximumFiles+2 {
		return nil, ErrRecoveryConflict
	}
	sort.Strings(names)
	return names, nil
}

func (source *nativeSource) unlinkFile(area, name string) error {
	if area == "" || !validEntry(area, name) {
		return ErrUnsafePath
	}
	current, err := source.readArea(area, name)
	if err != nil {
		return err
	}
	if !current.Exists {
		return nil
	}
	file, err := source.openArea(area)
	if err != nil {
		return err
	}
	defer file.Close()
	return transactionErrorOrNil(syscall.Unlinkat(int(file.Fd()), name))
}

func (source *nativeSource) removeArea(area string) error {
	if area != transactionArea && area != backupArea {
		return ErrUnsafePath
	}
	file, err := source.openArea(area)
	if err != nil {
		return err
	}
	file.Close()
	pointer, err := syscall.BytePtrFromString(area)
	if err != nil {
		return ErrUnsafePath
	}
	_, _, errno := syscall.Syscall(syscall.SYS_UNLINKAT, uintptr(source.directory.Fd()), uintptr(unsafe.Pointer(pointer)), 0x200) // AT_REMOVEDIR, empty directory only.
	if errno != 0 {
		return ErrWriteFailed
	}
	return nil
}
