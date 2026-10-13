package setup

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"
)

type virtualStore struct {
	areas   map[string]map[string]Snapshot
	inode   uint64
	locked  bool
	fail    string
	partial bool
	calls   []string
}

func newVirtualStore() *virtualStore {
	store := &virtualStore{areas: map[string]map[string]Snapshot{"": {}}, inode: 1}
	store.areas[""]["a.json"] = store.snapshot([]byte(`{"old":"secret"}`), 0o600)
	return store
}

func (store *virtualStore) snapshot(data []byte, mode fs.FileMode) Snapshot {
	store.inode++
	return Snapshot{Exists: true, Data: bytes.Clone(data), Mode: mode, Owner: store.Owner(), Identity: FileIdentity{Device: 7, Inode: store.inode}}
}
func (store *virtualStore) Owner() Owner { return Owner{UID: 1000, GID: 1000} }
func (store *virtualStore) Close() error { return nil }
func (store *virtualStore) Read(name string, _ int64) (Snapshot, error) {
	return store.readArea("", name)
}
func (store *virtualStore) rootIdentity() (FileIdentity, error) {
	return FileIdentity{Device: 7, Inode: 1}, nil
}
func (store *virtualStore) binding() error { return nil }
func (store *virtualStore) operation(point string) error {
	store.calls = append(store.calls, point)
	if point == store.fail {
		store.fail = ""
		return errors.New("private-secret from filesystem")
	}
	return nil
}
func (store *virtualStore) lock() (func(), error) {
	if store.locked {
		return nil, ErrLocked
	}
	store.locked = true
	return func() { store.locked = false }, nil
}
func (store *virtualStore) areaExists(area string) (bool, error) {
	_, exists := store.areas[area]
	return exists, nil
}
func (store *virtualStore) makeArea(area string) error {
	if err := store.operation("make:" + area); err != nil {
		return err
	}
	if _, exists := store.areas[area]; exists {
		return ErrWriteFailed
	}
	store.areas[area] = map[string]Snapshot{}
	return nil
}
func (store *virtualStore) readArea(area, name string) (Snapshot, error) {
	value := store.areas[area][name]
	value.Data = bytes.Clone(value.Data)
	return value, nil
}
func (store *virtualStore) writeExclusive(area, name string, data []byte, mode fs.FileMode) (Snapshot, error) {
	if _, exists := store.areas[area][name]; exists {
		return Snapshot{}, ErrWriteFailed
	}
	if err := store.operation("write:" + area + "/" + name); err != nil {
		if store.partial {
			partial := store.snapshot(data[:len(data)/2], 0o600)
			store.areas[area][name] = partial
			return partial, err
		}
		return Snapshot{}, err
	}
	value := store.snapshot(data, mode)
	store.areas[area][name] = value
	return value, nil
}
func (store *virtualStore) move(fromArea, fromName, toArea, toName string) error {
	if err := store.operation("move:" + fromName + ":" + toName); err != nil {
		return err
	}
	if fromArea == "" && fromName == transactionArea {
		if _, exists := store.areas[toName]; exists {
			return ErrWriteFailed
		}
		store.areas[toName] = store.areas[fromName]
		delete(store.areas, fromName)
		return nil
	}
	value, exists := store.areas[fromArea][fromName]
	if !exists {
		return ErrWriteFailed
	}
	if _, exists := store.areas[toArea][toName]; exists {
		return ErrWriteFailed
	}
	store.areas[toArea][toName] = value
	delete(store.areas[fromArea], fromName)
	return nil
}
func (store *virtualStore) exchange(fromArea, fromName, toArea, toName string) error {
	if err := store.operation("exchange:" + toArea + "/" + toName); err != nil {
		return err
	}
	first, ok := store.areas[fromArea][fromName]
	if !ok {
		return ErrWriteFailed
	}
	second, ok := store.areas[toArea][toName]
	if !ok {
		return ErrWriteFailed
	}
	store.areas[fromArea][fromName], store.areas[toArea][toName] = second, first
	return nil
}
func (store *virtualStore) syncArea(area string) error { return store.operation("sync:" + area) }
func (store *virtualStore) syncFile(area, name string) error {
	return store.operation("sync-file:" + area + "/" + name)
}
func (store *virtualStore) listArea(area string) ([]string, error) {
	names := []string{}
	for name := range store.areas[area] {
		names = append(names, name)
	}
	return names, nil
}
func (store *virtualStore) unlinkFile(area, name string) error {
	if err := store.operation("unlink:" + area + "/" + name); err != nil {
		return err
	}
	delete(store.areas[area], name)
	return nil
}
func (store *virtualStore) removeArea(area string) error {
	if err := store.operation("remove:" + area); err != nil {
		return err
	}
	if len(store.areas[area]) != 0 {
		return ErrWriteFailed
	}
	delete(store.areas, area)
	return nil
}

func virtualPlanner(t *testing.T) (*Planner, *virtualStore, *Plan) {
	t.Helper()
	store := newVirtualStore()
	planner, err := NewPlanner(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = planner.Close() })
	intent := jsonIntent("a.json", []byte(`{"new":"secret"}`))
	intent.AllowReplace = true
	plan, err := planner.Build(context.Background(), []Intent{intent, jsonIntent("b.json", []byte(`{}`))})
	if err != nil {
		t.Fatal(err)
	}
	return planner, store, plan
}

func TestMockedTransactionFilesystemFailuresPreserveOriginals(t *testing.T) {
	for _, point := range []string{
		"make:" + transactionArea, "write:" + transactionArea + "/" + nextManifestName, "sync:" + transactionArea,
		"write:" + transactionArea + "/" + slot(0), "write:" + transactionArea + "/" + slot(1), "sync-file:/a.json",
		"exchange:/a.json", "move:" + slot(1) + ":b.json",
	} {
		for _, partial := range []bool{false, true} {
			t.Run(point, func(t *testing.T) {
				planner, store, plan := virtualPlanner(t)
				before := store.areas[""]["a.json"]
				store.fail, store.partial = point, partial
				if _, err := planner.Apply(context.Background(), plan, true); err != ErrWriteFailed {
					t.Fatalf("failure=%v", err)
				}
				if !reflect.DeepEqual(store.areas[""]["a.json"], before) || len(store.areas[""]) != 1 {
					t.Fatal("failed transaction changed roots")
				}
				if exists, _ := store.areaExists(transactionArea); exists {
					t.Fatal("recoverable failure left staging")
				}
			})
		}
	}
}

func TestMockedInspectAndUnconfirmedOperationsNeverMutate(t *testing.T) {
	planner, store, plan := virtualPlanner(t)
	if _, err := planner.Apply(context.Background(), plan, false); err != ErrConsentRequired {
		t.Fatal(err)
	}
	if _, err := planner.Recover(context.Background(), false); err != ErrConsentRequired {
		t.Fatal(err)
	}
	if err := planner.DiscardBackup(context.Background(), false); err != ErrConsentRequired {
		t.Fatal(err)
	}
	if _, err := planner.Inspect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 0 || store.locked {
		t.Fatal("read-only/unconfirmed operation had effects")
	}
}

func TestMockedRecoveryDoesNotTrustCorruptOrUnexpectedMetadata(t *testing.T) {
	for _, corrupt := range []string{"truncated", "duplicate", "unknown", "foreign-root", "extra-file"} {
		t.Run(corrupt, func(t *testing.T) {
			planner, store, plan := virtualPlanner(t)
			if _, err := planner.Apply(context.Background(), plan, true); err != nil {
				t.Fatal(err)
			}
			store.areas[transactionArea] = store.areas[backupArea]
			delete(store.areas, backupArea)
			manifest := store.areas[transactionArea][manifestName]
			switch corrupt {
			case "truncated":
				manifest.Data = []byte(`{"secret":"unparseable`)
			case "duplicate":
				manifest.Data = bytes.Replace(manifest.Data, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
			case "unknown":
				manifest.Data = bytes.Replace(manifest.Data, []byte(`"version":1`), []byte(`"unexpected":"secret","version":1`), 1)
			case "foreign-root":
				manifest.Data = bytes.Replace(manifest.Data, []byte(`"root":{"Device":7`), []byte(`"root":{"Device":8`), 1)
			case "extra-file":
				store.areas[transactionArea]["unrecognized"] = store.snapshot([]byte("private-preserve"), 0o600)
			}
			store.areas[transactionArea][manifestName] = manifest
			before := store.areas[""]["a.json"]
			_, err := planner.Recover(context.Background(), true)
			if err != ErrInvalidTransaction && err != ErrRecoveryConflict {
				t.Fatalf("recovery accepted corruption: %v", err)
			}
			if strings.Contains(err.Error(), "secret") || !reflect.DeepEqual(store.areas[""]["a.json"], before) {
				t.Fatal("bad recovery leaked or overwrote root")
			}
		})
	}
}

func TestMockedWriteCollisionDoesNotEraseForeignStage(t *testing.T) {
	planner, store, plan := virtualPlanner(t)
	planner.fault = func(point string) error {
		if point == "stage:a.json" {
			store.areas[transactionArea][slot(0)] = store.snapshot([]byte("foreign-private-file"), 0o600)
		}
		return nil
	}
	if _, err := planner.Apply(context.Background(), plan, true); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("collision=%v", err)
	}
	if string(store.areas[transactionArea][slot(0)].Data) != "foreign-private-file" {
		t.Fatal("exclusive write collision erased existing stage")
	}
}

func TestReadOnlySourcesDoNotGainImplicitApplication(t *testing.T) {
	planner, _ := testPlanner(t, nil)
	plan, err := planner.Build(context.Background(), []Intent{jsonIntent("config", []byte(`{}`))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planner.Apply(context.Background(), plan, true); err != ErrApplyUnavailable {
		t.Fatalf("read-only source mutated: %v", err)
	}
}
