//go:build linux && (amd64 || arm64)

package setup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

const oldTransactionData = `{"value":"private-old"}`
const newTransactionData = `{"value":"private-new"}`

func transactionFixture(t *testing.T) (string, *Planner, *Plan) {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "a.json"), []byte(oldTransactionData), 0o600); err != nil {
		t.Fatal(err)
	}
	planner := nativePlanner(t, directory)
	return directory, planner, transactionPlan(t, planner)
}

func TestNativeTransactionExplicitUserOwnership(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-only explicit user provisioning fixture")
	}
	account, err := user.Lookup("nobody")
	if err != nil {
		t.Skip("registered nobody account not present")
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	gid, err := strconv.ParseUint(account.Gid, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(directory, int(uid), int(gid)); err != nil {
		t.Fatal(err)
	}
	owner := Owner{UID: uint32(uid), GID: uint32(gid)}
	planner, err := OpenPlanner(directory, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer planner.Close()
	plan, err := planner.Build(context.Background(), []Intent{jsonIntent("config.json", []byte(`{}`))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planner.Apply(context.Background(), plan, true); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"config.json", backupArea, filepath.Join(backupArea, manifestName)} {
		info, err := os.Stat(filepath.Join(directory, relative))
		if err != nil {
			t.Fatal(err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		if stat.Uid != owner.UID || stat.Gid != owner.GID {
			t.Fatal("explicit account ownership was not applied")
		}
	}
}

func transactionPlan(t *testing.T, planner *Planner) *Plan {
	t.Helper()
	intent := jsonIntent("a.json", []byte(newTransactionData))
	intent.AllowReplace = true
	plan, err := planner.Build(context.Background(), []Intent{intent, jsonIntent("b.json", []byte(`{"created":true}`))})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func requireTransactionData(t *testing.T, directory string, committed bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, "a.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := oldTransactionData
	if committed {
		want = newTransactionData
	}
	if string(data) != want {
		t.Fatalf("unexpected root data: %q", data)
	}
	_, err = os.Stat(filepath.Join(directory, "b.json"))
	if committed && err != nil || !committed && !os.IsNotExist(err) {
		t.Fatalf("create state: %v", err)
	}
}

func TestNativeTransactionConsentSuccessNoopAndRetention(t *testing.T) {
	directory, planner, plan := transactionFixture(t)
	if _, err := planner.Apply(context.Background(), plan, false); err != ErrConsentRequired {
		t.Fatalf("consent=%v", err)
	}
	entries, _ := os.ReadDir(directory)
	if len(entries) != 1 {
		t.Fatal("unconfirmed transaction wrote files")
	}
	before, _ := os.Stat(filepath.Join(directory, "a.json"))
	result, err := planner.Apply(context.Background(), plan, true)
	if err != nil || result.Changed != 2 || !result.BackupRetained {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	requireTransactionData(t, directory, true)
	backup, err := os.Stat(filepath.Join(directory, backupArea))
	if err != nil || backup.Mode().Perm() != 0o700 {
		t.Fatalf("backup=%v %v", backup, err)
	}
	original, err := os.Stat(filepath.Join(directory, backupArea, slot(0)))
	if err != nil || !os.SameFile(before, original) {
		t.Fatal("original inode not retained")
	}
	manifest, err := os.Stat(filepath.Join(directory, backupArea, manifestName))
	if err != nil || manifest.Mode().Perm() != 0o600 {
		t.Fatal("manifest permissions")
	}
	status, err := planner.Inspect(context.Background())
	if err != nil || status.State != "committed" || status.Changed != 2 {
		t.Fatalf("inspect=%+v %v", status, err)
	}
	encoded, _ := json.Marshal(status)
	if strings.Contains(string(encoded), "private-") || strings.Contains(string(encoded), "Digest") {
		t.Fatal("status leaked data")
	}
	noop := transactionPlan(t, planner)
	result, err = planner.Apply(context.Background(), noop, true)
	if err != nil || result.Changed != 0 || !result.BackupRetained {
		t.Fatalf("noop=%+v %v", result, err)
	}
	next := jsonIntent("a.json", []byte(`{"next":true}`))
	next.AllowReplace = true
	nextPlan, err := planner.Build(context.Background(), []Intent{next})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planner.Apply(context.Background(), nextPlan, true); err != ErrBackupRetained {
		t.Fatalf("backup overwritten: %v", err)
	}
	if err := planner.DiscardBackup(context.Background(), false); err != ErrConsentRequired {
		t.Fatal(err)
	}
	if err := planner.DiscardBackup(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	requireTransactionData(t, directory, true)
	if _, err := planner.Apply(context.Background(), nextPlan, true); err != nil {
		t.Fatal(err)
	}
}

func TestNativeTransactionFaultsRollbackOrFinishCommit(t *testing.T) {
	for _, point := range []string{"preflight", "staging", "stage:a.json", "staged:a.json", "stage:b.json", "staged:b.json", "commit:a.json", "committed-file:a.json", "commit:b.json", "committed-file:b.json", "commit-marker", "published"} {
		t.Run(point, func(t *testing.T) {
			directory, planner, plan := transactionFixture(t)
			fired := false
			planner.fault = func(current string) error {
				if current == point && !fired {
					fired = true
					return errors.New("private-secret in fault")
				}
				return nil
			}
			result, err := planner.Apply(context.Background(), plan, true)
			if !fired {
				t.Fatal("fault did not fire")
			}
			if point == "published" {
				if err != nil || !result.Recovered {
					t.Fatalf("durable commit not finished: %+v %v", result, err)
				}
			} else if err != ErrWriteFailed {
				t.Fatalf("rollback=%v", err)
			}
			requireTransactionData(t, directory, point == "published")
			if err != nil && strings.Contains(err.Error(), "private-secret") {
				t.Fatal("raw fault exposed")
			}
			if _, err := os.Stat(filepath.Join(directory, transactionArea)); !os.IsNotExist(err) {
				t.Fatalf("rollback left transaction: %v", err)
			}
		})
	}
}

func TestNativeTransactionCancellationAndStalePlans(t *testing.T) {
	directory, planner, plan := transactionFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	planner.fault = func(point string) error {
		if point == "committed-file:a.json" {
			cancel()
		}
		return nil
	}
	if _, err := planner.Apply(ctx, plan, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	requireTransactionData(t, directory, false)
	planner.fault = nil
	if err := os.WriteFile(filepath.Join(directory, "a.json"), []byte(`{"manual":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := planner.Apply(context.Background(), plan, true); err != ErrStalePlan {
		t.Fatalf("stale=%v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, transactionArea)); !os.IsNotExist(err) {
		t.Fatal("stale plan wrote transaction")
	}
}

func TestNativeTransactionPreservesManualEditAndConflicts(t *testing.T) {
	directory, planner, plan := transactionFixture(t)
	planner.fault = func(point string) error {
		if point == "committed-file:a.json" {
			if err := os.WriteFile(filepath.Join(directory, "a.json"), []byte(`{"manual":"keep"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			return errors.New("stop")
		}
		return nil
	}
	if _, err := planner.Apply(context.Background(), plan, true); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("conflict=%v", err)
	}
	data, _ := os.ReadFile(filepath.Join(directory, "a.json"))
	if string(data) != `{"manual":"keep"}` {
		t.Fatal("rollback erased manual edit")
	}
	original, _ := os.ReadFile(filepath.Join(directory, transactionArea, slot(0)))
	if string(original) != oldTransactionData {
		t.Fatal("original backup lost")
	}
	if _, err := planner.Recover(context.Background(), true); err != ErrRecoveryConflict {
		t.Fatalf("conflict bypassed: %v", err)
	}
}

func TestNativeTransactionDirectoryLockAndBinding(t *testing.T) {
	directory, planner, plan := transactionFixture(t)
	other := nativePlanner(t, directory)
	unlock, err := other.source.(transactionStore).lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planner.Apply(context.Background(), plan, true); err != ErrLocked {
		t.Fatalf("unlocked=%v", err)
	}
	unlock()
	moved := directory + "-moved"
	if err := os.Rename(directory, moved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(moved, directory) })
	if err := os.Symlink(moved, directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(directory) })
	if _, err := planner.Apply(context.Background(), plan, true); err != ErrStalePlan {
		t.Fatalf("rebound root accepted: %v", err)
	}
}

type crashSource struct {
	*nativeSource
	point string
}

type racingEditorSource struct {
	*nativeSource
	edited bool
}

func (source *racingEditorSource) exchange(fromArea, fromName, toArea, toName string) error {
	if !source.edited && toArea == "" && toName == "a.json" {
		source.edited = true
		if err := os.WriteFile(filepath.Join(source.path, toName), []byte(`{"racing":"editor"}`), 0o600); err != nil {
			return err
		}
	}
	return source.nativeSource.exchange(fromArea, fromName, toArea, toName)
}

func TestNativeTransactionRacingEditorRestoredAfterExchange(t *testing.T) {
	directory, planner, plan := transactionFixture(t)
	planner.source = &racingEditorSource{nativeSource: planner.source.(*nativeSource)}
	if _, err := planner.Apply(context.Background(), plan, true); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("race=%v", err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "a.json"))
	if err != nil || string(data) != `{"racing":"editor"}` {
		t.Fatalf("editor change not restored: %q %v", data, err)
	}
}

func (source *crashSource) exchange(fromArea, fromName, toArea, toName string) error {
	err := source.nativeSource.exchange(fromArea, fromName, toArea, toName)
	if err == nil && source.point == "after-exchange" && toArea == "" {
		os.Exit(91)
	}
	return err
}
func (source *crashSource) unlinkFile(area, name string) error {
	err := source.nativeSource.unlinkFile(area, name)
	if err == nil && (name == slot(0) && (source.point == "rollback-cleanup" && area == transactionArea || source.point == "discard-cleanup" && area == backupArea) || name == manifestName && (source.point == "rollback-manifest" && area == transactionArea || source.point == "discard-manifest" && area == backupArea)) {
		os.Exit(91)
	}
	return err
}

func TestNativeTransactionProcessCrashRecovery(t *testing.T) {
	if os.Getenv("HLCP_SETUP_CRASH_HELPER") == "1" {
		directory := os.Getenv("HLCP_SETUP_TEST_ROOT")
		point := os.Getenv("HLCP_SETUP_TEST_POINT")
		planner, err := OpenPlanner(directory, Owner{UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())})
		if err != nil {
			t.Fatal(err)
		}
		planner.source = &crashSource{nativeSource: planner.source.(*nativeSource), point: point}
		planner.fault = func(current string) error {
			if current == point {
				os.Exit(91)
			}
			if (point == "rollback-cleanup" || point == "rollback-manifest") && current == "committed-file:a.json" {
				return ErrWriteFailed
			}
			return nil
		}
		_, err = planner.Apply(context.Background(), transactionPlan(t, planner), true)
		if (point == "discard-cleanup" || point == "discard-manifest") && err == nil {
			_ = planner.DiscardBackup(context.Background(), true)
		}
		t.Fatalf("crash point did not exit: %v", err)
	}
	for _, point := range []string{"staging", "staged:a.json", "after-exchange", "committed-file:a.json", "published", "rollback-cleanup", "discard-cleanup", "rollback-manifest", "discard-manifest"} {
		t.Run(point, func(t *testing.T) {
			directory, planner, _ := transactionFixture(t)
			planner.Close()
			process := exec.Command(os.Args[0], "-test.run=^TestNativeTransactionProcessCrashRecovery$")
			process.Env = append(os.Environ(), "HLCP_SETUP_CRASH_HELPER=1", "HLCP_SETUP_TEST_ROOT="+directory, "HLCP_SETUP_TEST_POINT="+point)
			output, err := process.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 91 {
				t.Fatalf("helper=%v %s", err, output)
			}
			restarted := nativePlanner(t, directory)
			entriesBefore, _ := os.ReadDir(directory)
			if _, err := restarted.Recover(context.Background(), false); err != ErrConsentRequired {
				t.Fatal(err)
			}
			entriesAfter, _ := os.ReadDir(directory)
			if !reflect.DeepEqual(entriesBefore, entriesAfter) {
				t.Fatal("unconfirmed recovery wrote files")
			}
			status, err := restarted.Recover(context.Background(), true)
			if err != nil {
				t.Fatalf("recovery=%v", err)
			}
			committed := point == "published" || point == "discard-cleanup" || point == "discard-manifest"
			requireTransactionData(t, directory, committed)
			want := "none"
			if point == "published" {
				want = "committed"
			}
			if status.State != want {
				t.Fatalf("status=%+v", status)
			}
			if _, err := restarted.Recover(context.Background(), true); err != nil {
				t.Fatalf("repeat recovery=%v", err)
			}
		})
	}
}
