package localapi

import (
	"encoding/binary"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// privateCredential accepts owner-only files and the exact root-owned,
// read-only, per-service-user ACL produced by systemd LoadCredential.
// Never infer trust from CREDENTIALS_DIRECTORY or a pathname prefix.
func privateCredential(file *os.File, info os.FileInfo) bool {
	if info.Mode().Perm()&0077 == 0 {
		return true
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	uid := os.Geteuid()
	if !ok || info.Mode().Perm() != 0440 || stat.Uid != 0 || stat.Gid != 0 || uid == 0 {
		return false
	}
	// Read the ACL from the already opened descriptor: no path/symlink race.
	name, _ := syscall.BytePtrFromString("system.posix_acl_access")
	var buffer [44]byte // v2 header + exactly five canonical ACL entries.
	size, _, errno := syscall.Syscall6(syscall.SYS_FGETXATTR, file.Fd(),
		uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)), 0, 0)
	runtime.KeepAlive(file)
	runtime.KeepAlive(name)
	if errno != 0 || size != uintptr(len(buffer)) {
		return false // Missing, oversized or unsupported ACLs fail closed.
	}
	return systemdReadACL(buffer[:], uint32(uid))
}

func systemdReadACL(data []byte, uid uint32) bool {
	if len(data) != 44 || binary.LittleEndian.Uint32(data[:4]) != 2 || uid == 0 {
		return false
	}
	// POSIX ACL xattr v2: owner r--, this UID r--, owning group ---,
	// mask r--, other ---. No additional users/groups/write/execute grants.
	want := []struct {
		tag, perm uint16
		id        uint32
	}{
		{1, 4, 0xffffffff}, {2, 4, uid}, {4, 0, 0xffffffff},
		{16, 4, 0xffffffff}, {32, 0, 0xffffffff},
	}
	for index, entry := range want {
		raw := data[4+index*8 : 12+index*8]
		if binary.LittleEndian.Uint16(raw[:2]) != entry.tag ||
			binary.LittleEndian.Uint16(raw[2:4]) != entry.perm ||
			binary.LittleEndian.Uint32(raw[4:]) != entry.id {
			return false
		}
	}
	return true
}
