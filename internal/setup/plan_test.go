package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type memorySource struct {
	files  map[string]Snapshot
	reads  []string
	err    error
	closed int
}

func (source *memorySource) Owner() Owner { return Owner{UID: 1000, GID: 1000} }
func (source *memorySource) Read(name string, limit int64) (Snapshot, error) {
	if limit != MaximumFileBytes {
		return Snapshot{}, errors.New("unexpected limit")
	}
	source.reads = append(source.reads, name)
	return source.files[name], source.err
}
func (source *memorySource) Close() error { source.closed++; return source.err }

func testPlanner(t *testing.T, files map[string]Snapshot) (*Planner, *memorySource) {
	t.Helper()
	source := &memorySource{files: files}
	planner, err := NewPlanner(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = planner.Close() })
	return planner, source
}

func jsonIntent(name string, data []byte) Intent {
	return Intent{Name: name, Data: data, Mode: 0o600, Validate: func(data []byte) error {
		if !json.Valid(data) {
			return errors.New("invalid private input: " + string(data))
		}
		return nil
	}}
}

func existing(data string, mode fs.FileMode) Snapshot {
	return Snapshot{Exists: true, Data: []byte(data), Mode: mode, Owner: Owner{UID: 1000, GID: 1000}}
}

func TestPlanPreviewIsReadOnlyAndSecretFree(t *testing.T) {
	secret := `{"token":"private-token-must-never-appear"}`
	planner, source := testPlanner(t, map[string]Snapshot{"keep.json": existing(secret, 0o600), "replace.json": existing(`{"previous":"private-old-token"}`, 0o600)})
	intents := []Intent{jsonIntent("replace.json", []byte(secret)), jsonIntent("create.json", []byte(secret)), jsonIntent("keep.json", []byte(secret))}
	intents[0].AllowReplace = true
	plan, err := planner.Build(context.Background(), intents)
	if err != nil {
		t.Fatal(err)
	}
	want := Preview{ReadOnly: true, Changes: []Change{{Name: "create.json", Action: "create", Mode: 0o600}, {Name: "keep.json", Action: "unchanged", Mode: 0o600}, {Name: "replace.json", Action: "replace", Mode: 0o600}}}
	if !reflect.DeepEqual(plan.Preview(), want) {
		t.Fatalf("preview=%+v", plan.Preview())
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	valueEncoded, err := json.Marshal(*plan)
	if err != nil || !bytes.Equal(encoded, valueEncoded) {
		t.Fatal("value serialization differs from pointer preview")
	}
	for _, output := range []string{string(encoded), fmt.Sprint(plan), fmt.Sprintf("%+v", plan), fmt.Sprintf("%#v", plan), fmt.Sprintf("%+v", *plan), fmt.Sprintf("%#v", *plan)} {
		if strings.Contains(output, "private-") || strings.Contains(output, "token") {
			t.Fatalf("secret leaked in preview: %s", output)
		}
	}
	preview := plan.Preview()
	preview.Changes[0].Action = "mutated"
	intents[0].Data[0] = 'X'
	if !reflect.DeepEqual(plan.Preview(), want) || !bytes.HasPrefix(plan.files[2].data, []byte("{")) {
		t.Fatal("caller mutated stored plan")
	}
	if err := planner.Recheck(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(source.files) != 2 || string(source.files["replace.json"].Data) != `{"previous":"private-old-token"}` {
		t.Fatal("planning mutated source")
	}
}

func TestPlanRejectsInvalidExistingInsteadOfTreatingItAsAbsent(t *testing.T) {
	for _, allowReplace := range []bool{false, true} {
		planner, _ := testPlanner(t, map[string]Snapshot{"config.json": existing("invalid-secret-content", 0o600)})
		intent := jsonIntent("config.json", []byte(`{}`))
		intent.AllowReplace = allowReplace
		if _, err := planner.Build(context.Background(), []Intent{intent}); !errors.Is(err, ErrInvalidExisting) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("err=%v", err)
		}
	}
	planner, _ := testPlanner(t, map[string]Snapshot{"config.json": existing(`{"different":true}`, 0o600)})
	if _, err := planner.Build(context.Background(), []Intent{jsonIntent("config.json", []byte(`{}`))}); !errors.Is(err, ErrExistingConflict) {
		t.Fatalf("err=%v", err)
	}
}

func TestPlanRejectsMalformedInputsBeforeReadingSource(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../secret", "sub/config", "sub\\config", "/root/key", "C:secret", "config\x00", ".hlcp-setup-journal", strings.Repeat("x", 129), "space name"} {
		planner, source := testPlanner(t, nil)
		if _, err := planner.Build(context.Background(), []Intent{jsonIntent(name, []byte(`{}`))}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("name=%q err=%v", name, err)
		}
		if len(source.reads) != 0 {
			t.Fatal("invalid name read filesystem")
		}
	}
	for _, mode := range []fs.FileMode{0, 0o666, 0o700, 0o755, fs.ModeSymlink | 0o600, fs.ModeSetuid | 0o600} {
		planner, _ := testPlanner(t, nil)
		intent := jsonIntent("config", []byte(`{}`))
		intent.Mode = mode
		if _, err := planner.Build(context.Background(), []Intent{intent}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("mode=%v err=%v", mode, err)
		}
	}
	planner, source := testPlanner(t, nil)
	for _, intents := range [][]Intent{nil, {jsonIntent("same", []byte(`{}`)), jsonIntent("same", []byte(`{}`))}, {{Name: "missing-validator", Mode: 0o600}}, {jsonIntent("secret", []byte("invalid-token-value"))}} {
		if _, err := planner.Build(context.Background(), intents); !errors.Is(err, ErrInvalidInput) || strings.Contains(err.Error(), "token-value") {
			t.Fatalf("err=%v", err)
		}
	}
	if len(source.reads) != 0 {
		t.Fatal("invalid input read filesystem")
	}
}

func TestPlanRecheckRejectsDriftAndOtherPlanner(t *testing.T) {
	for _, change := range []func(*memorySource){
		func(s *memorySource) { delete(s.files, "config") },
		func(s *memorySource) { s.files["config"] = existing(`{"changed":true}`, 0o600) },
		func(s *memorySource) { s.files["config"] = existing(`{}`, 0o644) },
	} {
		planner, source := testPlanner(t, map[string]Snapshot{"config": existing(`{}`, 0o600)})
		plan, err := planner.Build(context.Background(), []Intent{jsonIntent("config", []byte(`{}`))})
		if err != nil {
			t.Fatal(err)
		}
		change(source)
		if err := planner.Recheck(context.Background(), plan); !errors.Is(err, ErrStalePlan) {
			t.Fatalf("err=%v", err)
		}
	}
	planner, source := testPlanner(t, map[string]Snapshot{})
	plan, err := planner.Build(context.Background(), []Intent{jsonIntent("config", []byte(`{}`))})
	if err != nil {
		t.Fatal(err)
	}
	source.files["config"] = existing(`{}`, 0o600)
	if err := planner.Recheck(context.Background(), plan); !errors.Is(err, ErrStalePlan) {
		t.Fatalf("err=%v", err)
	}
	other, _ := testPlanner(t, nil)
	if err := other.Recheck(context.Background(), plan); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v", err)
	}
}

func TestPlanBoundsAndCancellation(t *testing.T) {
	validate := func([]byte) error { return nil }
	for _, count := range []int{32, 33} {
		planner, _ := testPlanner(t, nil)
		intents := []Intent{}
		for index := 0; index < count; index++ {
			intents = append(intents, Intent{Name: fmt.Sprintf("file%d", index), Mode: 0o600, Validate: validate})
		}
		_, err := planner.Build(context.Background(), intents)
		if count == 32 && err != nil || count == 33 && !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("count=%d err=%v", count, err)
		}
	}
	planner, _ := testPlanner(t, nil)
	for _, length := range []int{MaximumFileBytes, MaximumFileBytes + 1} {
		_, err := planner.Build(context.Background(), []Intent{{Name: "large", Data: make([]byte, length), Mode: 0o600, Validate: validate}})
		if length == MaximumFileBytes && err != nil || length > MaximumFileBytes && !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("length=%d err=%v", length, err)
		}
	}
	intents := []Intent{}
	for index := 0; index < 9; index++ {
		intents = append(intents, Intent{Name: fmt.Sprintf("large%d", index), Data: make([]byte, MaximumFileBytes), Mode: 0o600, Validate: validate})
	}
	if _, err := planner.Build(context.Background(), intents); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("plan limit err=%v", err)
	}
	if _, err := planner.Build(context.Background(), intents[:8]); err != nil {
		t.Fatalf("exact plan budget rejected: %v", err)
	}
	files := map[string]Snapshot{}
	for _, intent := range intents {
		files[intent.Name] = Snapshot{Exists: true, Data: make([]byte, MaximumFileBytes), Mode: 0o600, Owner: Owner{UID: 1000, GID: 1000}}
	}
	existingPlanner, _ := testPlanner(t, files)
	zeroIntents := append([]Intent(nil), intents...)
	for index := range zeroIntents {
		zeroIntents[index].Data = nil
		zeroIntents[index].AllowReplace = true
	}
	if _, err := existingPlanner.Build(context.Background(), zeroIntents); !errors.Is(err, ErrInvalidExisting) {
		t.Fatalf("existing budget err=%v", err)
	}
	if _, err := existingPlanner.Build(context.Background(), zeroIntents[:8]); err != nil {
		t.Fatalf("exact existing budget rejected: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := planner.Build(ctx, []Intent{jsonIntent("config", []byte(`{}`))}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestSourceFailuresAndUnsafeMetadataAreNotMissingFiles(t *testing.T) {
	planner, source := testPlanner(t, nil)
	source.err = errors.New("read failed: secret-token")
	if _, err := planner.Build(context.Background(), []Intent{jsonIntent("config", []byte(`{}`))}); err != ErrReadFailed {
		t.Fatalf("raw error leaked: %v", err)
	}
	for _, snapshot := range []Snapshot{
		{Exists: true, Data: []byte(`{}`), Mode: 0o600, Owner: Owner{UID: 999, GID: 999}},
		{Exists: true, Data: []byte(`{}`), Mode: fs.ModeSymlink | 0o600, Owner: source.Owner()},
		{Exists: true, Data: []byte(`{}`), Mode: 0o666, Owner: source.Owner()},
	} {
		planner, _ := testPlanner(t, map[string]Snapshot{"config": snapshot})
		if _, err := planner.Build(context.Background(), []Intent{jsonIntent("config", []byte(`{}`))}); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("err=%v", err)
		}
	}
}

func TestPlannerConcurrentReadsAndClose(t *testing.T) {
	planner, source := testPlanner(t, nil)
	var workers sync.WaitGroup
	for index := 0; index < 8; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := planner.Build(context.Background(), []Intent{jsonIntent("config", []byte(`{}`))})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	if err := planner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := planner.Close(); err != nil || source.closed != 1 {
		t.Fatalf("close=%v count=%d", err, source.closed)
	}
	if _, err := planner.Build(context.Background(), []Intent{jsonIntent("config", []byte(`{}`))}); !errors.Is(err, ErrClosed) {
		t.Fatalf("err=%v", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("private-token in reader error") }

func TestReadPrivateInputIsBoundedAndDoesNotEcho(t *testing.T) {
	for _, test := range []struct {
		value string
		limit int64
		valid bool
	}{{" secret\n", 8, true}, {"123456789", 8, false}, {"", 0, false}} {
		value, err := ReadPrivateInput(strings.NewReader(test.value), test.limit)
		if test.valid {
			if err != nil || string(value) != test.value {
				t.Fatalf("value changed: %q %v", value, err)
			}
		} else if err != ErrInvalidInput {
			t.Fatalf("err=%v", err)
		}
	}
	if _, err := ReadPrivateInput(failingReader{}, 16); err != ErrInvalidInput {
		t.Fatalf("reader error leaked: %v", err)
	}
}
