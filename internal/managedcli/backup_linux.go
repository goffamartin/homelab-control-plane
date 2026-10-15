package managedcli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hlcp/internal/managed"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type backupEntry struct {
	Path string `json:"path"`
	UID  uint32 `json:"uid"`
	GID  uint32 `json:"gid"`
	Mode uint32 `json:"mode"`
	Data []byte `json:"data"`
}
type backupArchive struct {
	Version int           `json:"version"`
	Created time.Time     `json:"created"`
	Role    string        `json:"role"`
	Scope   string        `json:"scope"`
	Files   []backupEntry `json:"files"`
}

func backupTree(path string, uid, gid int, result *[]backupEntry, total *int, depth int) error {
	if depth > 8 || len(*result) > 1024 {
		return ErrSetup
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || checkDirectory(path, info, uid, gid) != nil {
		return ErrSetup
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return ErrSetup
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return ErrSetup
	}
	entries, err := dir.ReadDir(1025)
	_ = dir.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return ErrSetup
	}
	if len(entries) > 1024 {
		return ErrSetup
	}
	for _, entry := range entries {
		if len(*result) >= 1024 {
			return ErrSetup
		}
		name := entry.Name()
		if name == ".managed.lock" {
			continue
		}
		if entry.IsDir() {
			if err := backupTree(filepath.Join(path, name), uid, gid, result, total, depth+1); err != nil {
				return err
			}
			continue
		}
		f, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			return ErrSetup
		}
		stat, e := f.Stat()
		if e != nil || !stat.Mode().IsRegular() || stat.Size() > managed.MaximumStateBytes {
			f.Close()
			return ErrSetup
		}
		native, ok := stat.Sys().(*syscall.Stat_t)
		if !ok || native.Uid != uint32(uid) || native.Gid != uint32(gid) || native.Nlink != 1 || stat.Mode().Perm() != 0600 {
			f.Close()
			return ErrSetup
		}
		data, e := io.ReadAll(io.LimitReader(f, managed.MaximumStateBytes+1))
		after, e2 := f.Stat()
		_ = f.Close()
		if e != nil || e2 != nil || len(data) > managed.MaximumStateBytes || after.Size() != stat.Size() || after.ModTime() != stat.ModTime() {
			return ErrSetup
		}
		*total += len(data)
		if *total > 64<<20 {
			return ErrSetup
		}
		*result = append(*result, backupEntry{Path: filepath.Join(path, name), UID: native.Uid, GID: native.Gid, Mode: 0600, Data: data})
	}
	return nil
}
func PackageBackup(args []string, out io.Writer) int {
	f := flag.NewFlagSet("package-backup", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	role := f.String("role", "", "controller or agent")
	scope := f.String("scope", "system", "system or user")
	output := f.String("output", "", "new private backup path")
	stopped := f.Bool("stopped-confirmed", false, "explicitly confirm services stopped")
	jsonOut := f.Bool("json", has(args, "--json"), "versioned JSON")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			if *jsonOut {
				return emitResult(out, true, Result{Operation: "package.backup", Status: "ok", Code: "HELP", Phase: "options", Message: "Usage: hlcp package-backup --role controller|agent --scope system|user --output /private/new-backup.json --stopped-confirmed [--json]. No automatic restore or journal rollback."})
			}
			fmt.Fprintln(out, "Usage: hlcp package-backup --role controller|agent --scope system|user --output /private/new-backup.json --stopped-confirmed\nBounded no-follow private snapshot. Stop the affected service first. No automatic restore or journal rollback.")
			return 0
		}
		return resultError(out, *jsonOut, "package.backup", "options", "INVALID_OPTIONS", "Invalid backup options. Nothing changed.", "Use package-backup --help.")
	}
	if f.NArg() != 0 || !*stopped || *output == "" {
		return resultError(out, *jsonOut, "package.backup", "options", "INVALID_OPTIONS", "Invalid backup options. Nothing changed.", "Use package-backup --help.")
	}
	fail := func() int {
		return resultError(out, *jsonOut, "package.backup", "inspect", "BACKUP_PREFLIGHT_FAILED", "Backup did not complete: inspect service/state lock, ownership, private paths and size. No installation should proceed.", "Inspect retained private directories; source state was not restored or reset.")
	}
	ctx, cancel := boundedContext()
	defer cancel()
	archive := backupArchive{Version: 1, Created: time.Now().UTC(), Role: *role, Scope: *scope, Files: []backupEntry{}}
	type tree struct {
		path     string
		uid, gid int
	}
	var trees []tree
	state := ""
	runtimeUID, runtimeGID := 0, 0
	if *role == "controller" && *scope == "system" && os.Geteuid() == 0 {
		uid, gid, err := account("hlcp-controller")
		if err != nil {
			return fail()
		}
		trees = []tree{{controllerDirectory, 0, 0}, {"/var/lib/hlcp-controller", uid, gid}}
		state = controllerState
		runtimeUID, runtimeGID = uid, gid
	} else if *role == "agent" {
		p, err := pathsFor(*scope)
		if err != nil {
			return fail()
		}
		uid, gid := p.uid, p.gid
		if *scope == "system" {
			uid, gid, err = account("hlcp")
			if err != nil {
				return fail()
			}
		}
		trees = []tree{{p.directory, int(p.owner.UID), int(p.owner.GID)}, {filepath.Dir(p.state), uid, gid}}
		state = p.state
		runtimeUID, runtimeGID = uid, gid
	} else {
		return resultError(out, *jsonOut, "package.backup", "options", "INVALID_ROLE_OR_SCOPE", "Invalid backup role or scope. Nothing changed.", "Use package-backup --help.")
	}
	if _, err := os.Lstat(state); err == nil {
		s, e := managed.OpenStoreAsOwner(state, runtimeUID, runtimeGID)
		if e != nil {
			return fail()
		}
		defer s.Close()
	} else if !os.IsNotExist(err) {
		return fail()
	}
	if ensurePrivateDirectory(filepath.Dir(*output), os.Geteuid(), os.Getegid()) != nil {
		return fail()
	}
	// Destination may never be inside a source tree (recursive self backup).
	for _, t := range trees {
		rel, e := filepath.Rel(t.path, *output)
		if e != nil || rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..") {
			return fail()
		}
	}
	total := 0
	for _, t := range trees {
		if backupTree(t.path, t.uid, t.gid, &archive.Files, &total, 0) != nil {
			return fail()
		}
	}
	if ctx.Err() != nil {
		return fail()
	}
	data, err := json.Marshal(archive)
	if err != nil {
		return fail()
	}
	if writeNewArchive(*output, data) != nil {
		return emitResult(out, *jsonOut, Result{Operation: "package.backup", Status: "uncertain", Code: "BACKUP_WRITE_UNVERIFIED", Phase: "write_archive", Uncertain: true, Message: "Recovery archive write did not complete cleanly. A partial or fully written private archive may remain. No installation should proceed.", Next: "Inspect the destination before retrying; never restore an older journal automatically."})
	}
	return emitResult(out, *jsonOut, Result{Operation: "package.backup", Status: "ok", Code: "BACKUP_SAVED", Phase: "write_archive", Changed: true, Completed: []string{"private_archive_saved"}, Message: "Private recovery snapshot saved. Never restore an older journal automatically.", Data: map[string]any{"path": *output}})
}
func writeNewArchive(path string, data []byte) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(data) > 96<<20 {
		return ErrSetup
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return ErrSetup
	}
	defer root.Close()
	f, err := root.OpenFile(filepath.Base(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return ErrSetup
	}
	n, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || n != len(data) {
		return ErrSetup
	}
	dir, err := root.Open(".")
	if err != nil {
		return ErrSetup
	}
	defer dir.Close()
	return dir.Sync()
}
