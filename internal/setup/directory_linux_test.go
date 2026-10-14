package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func directoryFixture(t *testing.T) (string, Owner) {
	t.Helper()
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(parent, "config"), Owner{UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())}
}
func TestDirectoryPreviewConsentAndPrivateCreation(t *testing.T) {
	path, owner := directoryFixture(t)
	plan, err := PlanDirectory(path, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 0 {
		t.Fatal("preview writes")
	}
	if _, err := plan.Create(context.Background(), false); !errors.Is(err, ErrConsentRequired) {
		t.Fatal("consent")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := plan.Create(ctx, true); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel")
	}
	planner, err := plan.Create(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer planner.Close()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("private mode")
	}
	entries, _ = os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatal("staging retained after success")
	}
	if _, err := plan.Create(context.Background(), true); !errors.Is(err, ErrStalePlan) {
		t.Fatal("repeat adoption")
	}
}
func TestDirectoryRacesDoNotOverwriteOrRepair(t *testing.T) {
	for _, point := range []string{"before-create", "before-publish"} {
		t.Run(point, func(t *testing.T) {
			path, owner := directoryFixture(t)
			plan, err := PlanDirectory(path, owner)
			if err != nil {
				t.Fatal(err)
			}
			defer plan.Close()
			writeRacer := func() {
				if err := os.Mkdir(path, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, "manual"), []byte("PRIVATE manual"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if point == "before-create" {
				writeRacer()
			} else {
				plan.source.(*nativeDirectoryPlan).fault = func(at string) error {
					if at == "before-publish" {
						writeRacer()
					}
					return nil
				}
			}
			if _, err := plan.Create(context.Background(), true); !errors.Is(err, ErrStalePlan) {
				t.Fatalf("%v", err)
			}
			data, _ := os.ReadFile(filepath.Join(path, "manual"))
			info, _ := os.Stat(path)
			if string(data) != "PRIVATE manual" || info.Mode().Perm() != 0755 {
				t.Fatal("racer modified")
			}
			entries, _ := os.ReadDir(filepath.Dir(path))
			if len(entries) != 1 {
				t.Fatal("normal failure did not remove its empty staging dir")
			}
		})
	}
}
func TestDirectoryMissingUnsafeAndReboundParent(t *testing.T) {
	path, owner := directoryFixture(t)
	if _, err := PlanDirectory(filepath.Join(path, "child"), owner); !errors.Is(err, ErrMissingParent) {
		t.Fatal("recursive create")
	}
	link := filepath.Join(filepath.Dir(path), "link")
	os.Symlink(filepath.Dir(path), link)
	if _, err := PlanDirectory(filepath.Join(link, "child"), owner); err == nil {
		t.Fatal("symlink parent")
	}
	plan, err := PlanDirectory(path, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	parent := filepath.Dir(path)
	if err := os.Rename(parent, parent+"-old"); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(parent + "-old")
	os.Mkdir(parent, 0700)
	if _, err := plan.Create(context.Background(), true); !errors.Is(err, ErrStalePlan) {
		t.Fatal("rebound parent")
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 0 {
		t.Fatal("rebound mutated")
	}
}
func TestDirectoryInterruptedPublishRetainedAndOrphanBlocksRetry(t *testing.T) {
	path, owner := directoryFixture(t)
	plan, err := PlanDirectory(path, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	native := plan.source.(*nativeDirectoryPlan)
	native.fault = func(at string) error {
		if at == "after-publish" {
			return errors.New("fault")
		}
		return nil
	}
	if _, err := plan.Create(context.Background(), true); err == nil {
		t.Fatal("fault ignored")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("published directory not retained")
	}
	// Simulate a pre-publish hard crash: never adopt/remove a previous stage.
	other := filepath.Join(filepath.Dir(path), "other")
	orphan, err := PlanDirectory(other, owner)
	if err != nil {
		t.Fatal(err)
	}
	stage := orphan.source.(*nativeDirectoryPlan).stage
	orphan.Close()
	os.Mkdir(filepath.Join(filepath.Dir(path), stage), 0700)
	if _, err := PlanDirectory(other, owner); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatal("orphan adopted")
	}
}

func TestDirectoryManualStagingContentIsNotRemoved(t *testing.T) {
	path, owner := directoryFixture(t)
	plan, err := PlanDirectory(path, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	native := plan.source.(*nativeDirectoryPlan)
	native.fault = func(at string) error {
		if at == "before-publish" {
			os.WriteFile(filepath.Join(filepath.Dir(path), native.stage, "manual"), []byte("PRIVATE manual"), 0600)
			return errors.New("fault")
		}
		return nil
	}
	if _, err := plan.Create(context.Background(), true); err == nil {
		t.Fatal("fault ignored")
	}
	data, _ := os.ReadFile(filepath.Join(filepath.Dir(path), native.stage, "manual"))
	if string(data) != "PRIVATE manual" {
		t.Fatal("unknown content removed")
	}
	if _, err := PlanDirectory(path, owner); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatal("retained stage did not block")
	}
}
