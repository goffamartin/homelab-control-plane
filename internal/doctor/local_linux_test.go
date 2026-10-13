package doctor

import (
	"context"
	"hlcp/internal/setup"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNativeScriptChecksNeverExecute(t *testing.T) {
	directory := t.TempDir()
	os.Chmod(directory, 0700)
	script := filepath.Join(directory, "script")
	marker := filepath.Join(directory, "executed")
	data := []byte("#!/bin/sh\ntouch " + marker + "\n")
	if err := os.WriteFile(script, data, 0700); err != nil {
		t.Fatal(err)
	}
	host := NativeHost{}
	status := host.Script(script, directory)
	if !status.Executable || !status.Directory || status.Format != "interpreter" {
		t.Fatalf("%+v", status)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("script executed")
	}
	for _, header := range []struct{ data, format string }{
		{"#!/missing/interpreter\n", ""}, {"#!/usr/bin/env sh\n", "indirect"},
		{"#!/bin/sh\r\n", ""}, {"#!/bin/sh", ""}, {"not a script\n", ""},
		{"\x7fELF", "binary"},
	} {
		os.WriteFile(script, []byte(header.data), 0700)
		if got := host.Script(script, directory).Format; got != header.format {
			t.Fatalf("%q: %s", header.data, got)
		}
	}
	os.Chmod(script, 0600)
	if host.Script(script, directory).Executable {
		t.Fatal("non-executable accepted")
	}
	os.Chmod(script, 0777)
	if host.Script(script, directory).Executable {
		t.Fatal("writable executable accepted")
	}
	os.Chmod(script, 0700)
	os.Chmod(directory, 0777)
	if host.Script(script, directory).Directory || host.Script(script, directory).Executable {
		t.Fatal("unsafe directory accepted")
	}
	os.Chmod(directory, 0700)
	link := filepath.Join(directory, "cwd-link")
	os.Symlink(directory, link)
	if host.Script(script, link).Directory {
		t.Fatal("working-directory link accepted")
	}
	if host.Script(script, filepath.Join(directory, "missing")).Directory {
		t.Fatal("missing directory accepted")
	}
}

type fileEvidence struct {
	Data     string
	Mode     os.FileMode
	Modified int64
}

func snapshotTree(t *testing.T, root string) map[string]fileEvidence {
	t.Helper()
	result := map[string]fileEvidence{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		data := []byte{}
		if info.Mode().IsRegular() {
			data, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		result[path] = fileEvidence{string(data), info.Mode(), info.ModTime().UnixNano()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestNativeTransactionInspectPreservesState(t *testing.T) {
	directory := t.TempDir()
	os.Chmod(directory, 0700)
	host := NativeHost{}
	assertState := func(want string, wantError bool) {
		t.Helper()
		before := snapshotTree(t, directory)
		status, err := host.InspectSetup(directory)
		if (err != nil) != wantError || (!wantError && status.State != want) {
			t.Fatalf("%+v %v", status, err)
		}
		if !reflect.DeepEqual(before, snapshotTree(t, directory)) {
			t.Fatal("inspection changed state")
		}
	}
	assertState("none", false)
	planner, err := setup.OpenPlanner(directory, setup.Owner{UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())})
	if err != nil {
		t.Fatal(err)
	}
	defer planner.Close()
	plan, err := planner.Build(context.Background(), []setup.Intent{{Name: "config", Data: []byte("SECRET config"), Mode: 0600, Validate: func([]byte) error { return nil }}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planner.Apply(context.Background(), plan, true); err != nil {
		t.Fatal(err)
	}
	assertState("committed", false)
	backup := filepath.Join(directory, ".hlcp-setup-backup")
	pending := filepath.Join(directory, ".hlcp-setup-transaction")
	if err := os.Rename(backup, pending); err != nil {
		t.Fatal(err)
	}
	assertState("pending", false)
	if err := os.WriteFile(filepath.Join(pending, "manifest.json"), []byte("SECRET corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	assertState("", true)
	missing := filepath.Join(directory, "missing")
	if _, err := host.InspectSetup(missing); err == nil {
		t.Fatal("missing accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("directory created")
	}
}
