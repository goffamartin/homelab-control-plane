//go:build linux

package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

func nativePlanner(t *testing.T, directory string) *Planner {
	t.Helper()
	// testing.TempDir's leaf can be 0755 under the container umask. Provision the
	// private fixture explicitly; OpenPlanner must never fix permissions itself.
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	planner, err := OpenPlanner(directory, Owner{UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = planner.Close() })
	return planner
}

func TestNativePlanningDoesNotWriteOrCreateDirectory(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	if err := os.WriteFile(path, []byte(`{"private":"existing"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	planner := nativePlanner(t, directory)
	intent := jsonIntent("config.json", []byte(`{"private":"replacement"}`))
	intent.AllowReplace = true
	plan, err := planner.Build(context.Background(), []Intent{intent, jsonIntent("api.token", []byte(`"new-secret"`))})
	if err != nil {
		t.Fatal(err)
	}
	if err := planner.Recheck(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		t.Fatalf("planning created files: %v", entries)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"private":"existing"}` || !os.SameFile(before, after) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("planning modified existing file")
	}
	absent := filepath.Join(directory, "absent")
	if _, err := OpenPlanner(absent, Owner{UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())}); !errors.Is(err, ErrMissingDirectory) {
		t.Fatalf("absent root err=%v", err)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatal("planning created target directory")
	}
}

func TestNativeSourceRejectsSymlinksAndUnsafeParents(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	owner := Owner{UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())}
	for _, directory := range []string{link, filepath.Join(link, "child"), "relative", "/", base + "/../other"} {
		if planner, err := OpenPlanner(directory, owner); err == nil {
			planner.Close()
			t.Fatalf("unsafe root accepted: %s", directory)
		}
	}
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPlanner(target, owner); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("public root err=%v", err)
	}
	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(base, 0o700) })
	if _, err := OpenPlanner(target, owner); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("writable parent err=%v", err)
	}
}

func TestNativeSourceRejectsNonregularHardlinkedAndOversizedFiles(t *testing.T) {
	directory := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"symlink", "dangling"} {
		target := outside
		if name == "dangling" {
			target = outside + "-missing"
		}
		if err := os.Symlink(target, filepath.Join(directory, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(outside, filepath.Join(directory, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(directory, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(directory, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "writable"), []byte(`{}`), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(directory, "writable"), 0o666); err != nil {
		t.Fatal(err)
	}
	large, err := os.Create(filepath.Join(directory, "oversized"))
	if err != nil {
		t.Fatal(err)
	}
	if err := large.Chmod(0o600); err != nil {
		t.Fatal(err)
	}
	if err := large.Truncate(MaximumFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := large.Close(); err != nil {
		t.Fatal(err)
	}
	planner := nativePlanner(t, directory)
	for _, name := range []string{"symlink", "dangling", "hardlink", "fifo", "directory", "writable", "oversized"} {
		start := time.Now()
		if _, err := planner.Build(context.Background(), []Intent{jsonIntent(name, []byte(`{}`))}); err == nil {
			t.Fatalf("unsafe input accepted: %s", name)
		}
		if time.Since(start) > time.Second {
			t.Fatalf("nonregular input blocked: %s", name)
		}
	}
}

func TestNativeSourceRemainsAnchoredWhenDirectoryPathIsReplaced(t *testing.T) {
	base := t.TempDir()
	original := filepath.Join(base, "root")
	moved := filepath.Join(base, "moved")
	if err := os.Mkdir(original, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(original, "config"), []byte(`{"original":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	planner := nativePlanner(t, original)
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "config"), []byte(`{"redirected":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, original); err != nil {
		t.Fatal(err)
	}
	plan, err := planner.Build(context.Background(), []Intent{jsonIntent("config", []byte(`{"original":true}`))})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Preview().Changes, []Change{{Name: "config", Action: "unchanged", Mode: 0o600}}) {
		t.Fatal("read redirected through replacement path")
	}
}

func TestNativeSourceRecheckDetectsFileReplacement(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	planner := nativePlanner(t, directory)
	plan, err := planner.Build(context.Background(), []Intent{jsonIntent("config", []byte(`{}`))})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"edited":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := planner.Recheck(context.Background(), plan); !errors.Is(err, ErrStalePlan) {
		t.Fatalf("err=%v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", path); err != nil {
		t.Fatal(err)
	}
	if err := planner.Recheck(context.Background(), plan); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("dangling symlink became absent: %v", err)
	}
}

func TestNativeSourceRecheckRejectsDirectoryPermissionDrift(t *testing.T) {
	directory := t.TempDir()
	planner := nativePlanner(t, directory)
	plan, err := planner.Build(context.Background(), []Intent{jsonIntent("config", []byte(`{}`))})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := planner.Recheck(context.Background(), plan); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("err=%v", err)
	}
}

func TestNativeSourceRecheckRejectsSameContentNewInode(t *testing.T) {
	directory := t.TempDir()
	planner := nativePlanner(t, directory)
	path := filepath.Join(directory, "config")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := planner.Build(context.Background(), []Intent{jsonIntent("config", []byte(`{}`))})
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(directory, "replacement")
	if err := os.WriteFile(replacement, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if err := planner.Recheck(context.Background(), plan); !errors.Is(err, ErrStalePlan) {
		t.Fatalf("identical replacement not detected: %v", err)
	}
}
