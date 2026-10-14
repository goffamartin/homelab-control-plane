package onboarding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"hlcp/internal/agent"
	"hlcp/internal/doctor"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Fixed local dependencies only; no exec, socket connect, writes or impersonation.
type nativeResourceHost struct{}

func resourceCaller(ctx context.Context) error {
	if ctx.Err() != nil || os.Geteuid() == 0 || os.Getegid() == 0 {
		return ErrAgentActivationResources
	}
	if _, err := (nativeSetupHost{}).Owner(); err != nil {
		return ErrAgentActivationResources
	}
	return nil
}
func resourceProof(value any) ([32]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return [32]byte{}, ErrAgentActivationResources
	}
	return sha256.Sum256(data), nil
}
func (nativeResourceHost) RuntimeExecutable(ctx context.Context, name string) ([32]byte, error) {
	if resourceCaller(ctx) != nil || (name != "docker" && name != "podman" && name != "systemctl") {
		return [32]byte{}, ErrAgentActivationResources
	}
	snapshot, err := activationFile("/usr/bin/"+name, 64<<20, true)
	if err != nil {
		return [32]byte{}, ErrAgentActivationResources
	}
	return resourceProof(snapshot)
}

// resourceMetadata deliberately excludes atime and directory mtime: merely
// reading/executing a file must not invalidate a plan. File content is hashed.
type resourceMetadata struct {
	Path          string
	Device, Inode uint64
	UID, GID      uint32
	Mode          os.FileMode
}

func resourceMeta(path string, info os.FileInfo) (resourceMetadata, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return resourceMetadata{}, ErrAgentActivationResources
	}
	return resourceMetadata{path, uint64(stat.Dev), stat.Ino, stat.Uid, stat.Gid, info.Mode()}, nil
}
func resourceAccess(info os.FileInfo, want os.FileMode) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	shift := uint(0)
	if stat.Uid == uint32(os.Geteuid()) {
		shift = 6
	} else {
		groups, err := os.Getgroups()
		if err != nil {
			return false
		}
		if stat.Gid == uint32(os.Getegid()) {
			shift = 3
		}
		for _, gid := range groups {
			if stat.Gid == uint32(gid) {
				shift = 3
			}
		}
	}
	return (info.Mode().Perm()>>shift)&want == want
}
func resourceParents(path string) ([]resourceMetadata, error) {
	result := []resourceMetadata{}
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || !resourceAccess(info, 1) {
			return nil, ErrAgentActivationResources
		}
		meta, err := resourceMeta(parent, info)
		if err != nil || (meta.UID != 0 && meta.UID != uint32(os.Geteuid())) {
			return nil, ErrAgentActivationResources
		}
		// Safe root-owned sticky /tmp permits non-root diagnostic fixtures. No user
		// writable non-sticky ancestor or symlink is accepted.
		if info.Mode().Perm()&0022 != 0 && !(meta.UID == 0 && info.Mode()&os.ModeSticky != 0) {
			return nil, ErrAgentActivationResources
		}
		result = append(result, meta)
		if parent == filepath.Dir(parent) {
			return result, nil
		}
	}
}
func resourceParentsUnchanged(before []resourceMetadata) bool {
	for _, meta := range before {
		info, err := os.Lstat(meta.Path)
		if err != nil {
			return false
		}
		current, err := resourceMeta(meta.Path, info)
		if err != nil || current != meta {
			return false
		}
	}
	return true
}

// Fingerprint one bounded regular file without following the final symlink.
// Only fixed uidmap helper paths may carry setuid; never a configured script.
func resourceFile(path string, limit int64, uidmap bool) ([]byte, [32]byte, error) {
	fail := func() ([]byte, [32]byte, error) { return nil, [32]byte{}, ErrAgentActivationResources }
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fail()
	}
	if uidmap && path != "/usr/bin/newuidmap" && path != "/usr/bin/newgidmap" {
		return fail()
	}
	parents, err := resourceParents(path)
	if err != nil {
		return fail()
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fail()
	}
	file := os.NewFile(uintptr(fd), "resource inspection")
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return fail()
	}
	meta, err := resourceMeta(path, before)
	if err != nil {
		return fail()
	}
	stat := before.Sys().(*syscall.Stat_t)
	if !before.Mode().IsRegular() || before.Size() > limit || stat.Nlink != 1 || (meta.UID != 0 && meta.UID != uint32(os.Geteuid())) || before.Mode().Perm()&0022 != 0 || !resourceAccess(before, 1) {
		return fail()
	}
	if uidmap {
		if meta.UID != 0 || meta.GID != 0 || before.Mode()&os.ModeSetuid == 0 || before.Mode()&(os.ModeSetgid|os.ModeSticky) != 0 {
			return fail()
		}
	} else if before.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return fail()
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return fail()
	}
	after, err := file.Stat()
	if err != nil {
		return fail()
	}
	afterStat, ok := after.Sys().(*syscall.Stat_t)
	bound, err := os.Lstat(path)
	if !ok || err != nil || !os.SameFile(before, bound) || after.Size() != before.Size() || after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) || afterStat.Ctim != stat.Ctim || afterStat.Uid != stat.Uid || afterStat.Gid != stat.Gid || afterStat.Nlink != 1 || !resourceParentsUnchanged(parents) {
		return fail()
	}
	proof, err := resourceProof(struct {
		File    resourceMetadata
		Parents []resourceMetadata
		Content [32]byte
	}{meta, parents, sha256.Sum256(data)})
	return data, proof, err
}

func (nativeResourceHost) ScriptFiles(ctx context.Context, command agent.ScriptCommandConfig) ([32]byte, error) {
	if resourceCaller(ctx) != nil {
		return [32]byte{}, ErrAgentActivationResources
	}
	status := (doctor.NativeHost{}).Script(command.Executable, command.WorkingDirectory)
	if !status.Executable || !status.Directory || (status.Format != "binary" && status.Format != "interpreter") {
		return [32]byte{}, ErrAgentActivationResources
	}
	resolved, err := filepath.EvalSymlinks(command.Executable)
	if err != nil {
		return [32]byte{}, ErrAgentActivationResources
	}
	data, executable, err := resourceFile(resolved, 8<<20, false)
	if err != nil {
		return [32]byte{}, err
	}
	var interpreter [32]byte
	if status.Format == "interpreter" {
		newline := bytes.IndexByte(data, '\n')
		if !bytes.HasPrefix(data, []byte("#!")) || newline < 0 || newline > 255 {
			return [32]byte{}, ErrAgentActivationResources
		}
		fields := strings.Fields(string(data[2:newline]))
		if len(fields) == 0 {
			return [32]byte{}, ErrAgentActivationResources
		}
		snapshot, err := activationFile(fields[0], 64<<20, true)
		if err != nil {
			return [32]byte{}, ErrAgentActivationResources
		}
		interpreter, err = resourceProof(snapshot)
		if err != nil {
			return [32]byte{}, err
		}
	} else if !bytes.HasPrefix(data, []byte{0x7f, 'E', 'L', 'F'}) {
		return [32]byte{}, ErrAgentActivationResources
	}
	info, err := os.Lstat(command.WorkingDirectory)
	if err != nil {
		return [32]byte{}, ErrAgentActivationResources
	}
	directory, err := resourceMeta(command.WorkingDirectory, info)
	if err != nil {
		return [32]byte{}, err
	}
	parents, err := resourceParents(command.WorkingDirectory)
	if err != nil || !resourceParentsUnchanged(parents) || !(doctor.NativeHost{}).Script(command.Executable, command.WorkingDirectory).Directory {
		return [32]byte{}, ErrAgentActivationResources
	}
	current, err := filepath.EvalSymlinks(command.Executable)
	if err != nil || current != resolved || ctx.Err() != nil {
		return [32]byte{}, ErrAgentActivationResources
	}
	bound, err := os.Lstat(command.WorkingDirectory)
	if err != nil {
		return [32]byte{}, ErrAgentActivationResources
	}
	boundMeta, err := resourceMeta(command.WorkingDirectory, bound)
	if err != nil || boundMeta != directory {
		return [32]byte{}, ErrAgentActivationResources
	}
	return resourceProof(struct {
		Executable, Interpreter [32]byte
		Directory               resourceMetadata
		Parents                 []resourceMetadata
	}{executable, interpreter, directory, parents})
}
func (nativeResourceHost) DockerSocket(ctx context.Context) ([32]byte, error) {
	if resourceCaller(ctx) != nil {
		return [32]byte{}, ErrAgentActivationResources
	}
	const path = "/run/docker.sock"
	parents, err := resourceParents(path)
	if err != nil {
		return [32]byte{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return [32]byte{}, ErrAgentActivationResources
	}
	meta, err := resourceMeta(path, info)
	if err != nil || info.Mode()&os.ModeSocket == 0 || meta.UID != 0 || info.Mode().Perm()&0007 != 0 || !resourceAccess(info, 6) || !resourceParentsUnchanged(parents) {
		return [32]byte{}, ErrAgentActivationResources
	}
	groups, err := os.Getgroups()
	if err != nil {
		return [32]byte{}, ErrAgentActivationResources
	}
	bound, err := os.Lstat(path)
	if err != nil {
		return [32]byte{}, ErrAgentActivationResources
	}
	boundMeta, err := resourceMeta(path, bound)
	if err != nil || boundMeta != meta || ctx.Err() != nil {
		return [32]byte{}, ErrAgentActivationResources
	}
	return resourceProof(struct {
		Socket  resourceMetadata
		Parents []resourceMetadata
		Groups  []int
	}{meta, parents, groups})
}
func (nativeResourceHost) PodmanMappings(ctx context.Context) ([32]byte, error) {
	if resourceCaller(ctx) != nil {
		return [32]byte{}, ErrAgentActivationResources
	}
	account, err := user.LookupId(strconv.Itoa(os.Geteuid()))
	if err != nil {
		return [32]byte{}, ErrAgentActivationResources
	}
	snapshots := []any{}
	for _, path := range []string{"/etc/subuid", "/etc/subgid"} {
		snapshot, err := activationFile(path, doctor.MaximumFileSize, false)
		if err != nil || !doctor.ValidSubordinateRange(snapshot.Data, doctor.Account{Name: account.Username, UID: account.Uid, GID: account.Gid}) {
			return [32]byte{}, ErrAgentActivationResources
		}
		snapshots = append(snapshots, snapshot)
	}
	for _, path := range []string{"/usr/bin/newuidmap", "/usr/bin/newgidmap"} {
		data, proof, err := resourceFile(path, 8<<20, true)
		if err != nil || !bytes.HasPrefix(data, []byte{0x7f, 'E', 'L', 'F'}) {
			return [32]byte{}, ErrAgentActivationResources
		}
		snapshots = append(snapshots, proof)
	}
	if ctx.Err() != nil {
		return [32]byte{}, ErrAgentActivationResources
	}
	return resourceProof(snapshots)
}
