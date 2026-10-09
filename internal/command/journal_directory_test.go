//go:build linux

package command

import (
	"path/filepath"
	"testing"
)

func TestJournalCreatesAndPersistsNestedDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "agent", "journal.json")
	journal, err := OpenFileJournal(path, 16)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := journal.Begin(payload("c")); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenFileJournal(path, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	entry, fresh, err := restarted.Begin(payload("c"))
	if err != nil || fresh || entry.Result == nil {
		t.Fatalf("nested journal recovery: %+v %v %v", entry, fresh, err)
	}
}
