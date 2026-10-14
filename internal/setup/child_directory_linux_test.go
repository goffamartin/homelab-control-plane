package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPinnedChildDirectoryIndependentDescriptor(t *testing.T) {
	owner := Owner{UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())}
	path := t.TempDir()
	os.Chmod(path, 0700)
	parent, err := OpenPlanner(path, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = parent.PlanChildDirectory("../other"); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
	child, err := parent.PlanChildDirectory("hlcp")
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	if _, err = os.Stat(filepath.Join(path, "hlcp")); !os.IsNotExist(err) {
		t.Fatal("planning wrote directory")
	}
	parent.Close()
	if _, err = parent.PlanChildDirectory("other"); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	planner, err := child.Create(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer planner.Close()
	info, err := os.Stat(filepath.Join(path, "hlcp"))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("child creation failed")
	}
}
func TestPinnedChildDirectoryRejectsParentRebinding(t *testing.T) {
	owner := Owner{UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())}
	path := t.TempDir()
	os.Chmod(path, 0700)
	parent, err := OpenPlanner(path, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	child, err := parent.PlanChildDirectory("hlcp")
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	if err = os.Rename(path, path+"-original"); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(path + "-original")
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = child.Create(context.Background(), true); !errors.Is(err, ErrStalePlan) {
		t.Fatal(err)
	}
	if _, err = parent.PlanChildDirectory("other"); err == nil {
		t.Fatal("rebound parent adopted")
	}
	for _, root := range []string{path, path + "-original"} {
		if _, err = os.Stat(filepath.Join(root, "hlcp")); !os.IsNotExist(err) {
			t.Fatal("stale plan wrote directory")
		}
	}
}
