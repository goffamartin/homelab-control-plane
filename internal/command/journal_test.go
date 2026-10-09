package command

import (
	"path/filepath"
	"testing"

	"hlcp/internal/protocol"
)

func TestFileJournalSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	journal, err := OpenFileJournal(path, 16)
	if err != nil {
		t.Fatal(err)
	}
	command := protocol.CommandPayload{CommandID: "cmd-1", ResourceID: "catalog", Action: "restart"}
	if _, fresh, err := journal.Begin(command); err != nil || !fresh {
		t.Fatalf("begin fresh=%v err=%v", fresh, err)
	}
	if err := journal.Complete(protocol.CommandResultPayload{CommandID: "cmd-1", Success: true, Code: "OK"}); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenFileJournal(path, 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close() })
	entry, fresh, err := restarted.Begin(command)
	if err != nil || fresh || entry.Result == nil || !entry.Result.Success {
		t.Fatalf("entry=%#v fresh=%v err=%v", entry, fresh, err)
	}
}

func TestFileJournalDoesNotRepeatIncompleteCommandAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	journal, err := OpenFileJournal(path, 16)
	if err != nil {
		t.Fatal(err)
	}
	command := protocol.CommandPayload{CommandID: "cmd-1", ResourceID: "catalog", Action: "restart"}
	if _, fresh, err := journal.Begin(command); err != nil || !fresh {
		t.Fatalf("begin fresh=%v err=%v", fresh, err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenFileJournal(path, 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close() })
	entry, fresh, err := restarted.Begin(command)
	if err != nil || fresh || entry.Result == nil || entry.Result.Code != "EXECUTION_UNKNOWN" {
		t.Fatalf("entry=%#v fresh=%v err=%v", entry, fresh, err)
	}
}
