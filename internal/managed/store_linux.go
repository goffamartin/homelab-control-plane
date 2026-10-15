package managed

import (
	"os"
	"path/filepath"
	"syscall"
)

func privateInfo(info os.FileInfo, mode os.FileMode) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && stat.Gid == uint32(os.Getegid()) && info.Mode().Perm() == mode && (info.IsDir() || stat.Nlink == 1)
}
func openPrivateStore(path string) (*os.Root, *os.File, error) {
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() {
			return nil, nil, ErrStore
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (stat.Uid != 0 && stat.Uid != uint32(os.Geteuid())) || (info.Mode().Perm()&0022 != 0 && !(stat.Uid == 0 && info.Mode()&os.ModeSticky != 0)) {
			return nil, nil, ErrStore
		}
		if parent == filepath.Dir(parent) {
			break
		}
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return nil, nil, ErrStore
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || !privateInfo(info, 0700) {
		return nil, nil, ErrStore
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, nil, ErrStore
	}
	f, err := openStoreFile(root, ".managed.lock", os.O_RDWR|os.O_CREATE)
	if err != nil {
		root.Close()
		return nil, nil, ErrStore
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		root.Close()
		return nil, nil, ErrStore
	}
	return root, f, nil
}
func openStoreFile(root *os.Root, name string, flags int) (*os.File, error) {
	f, err := root.OpenFile(name, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !privateInfo(info, 0600) {
		f.Close()
		return nil, ErrStore
	}
	return f, nil
}
