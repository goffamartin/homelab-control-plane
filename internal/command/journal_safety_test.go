package command

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hlcp/internal/protocol"
)

func openTestJournal(t *testing.T, path string, limit int) *FileJournal {
	t.Helper()
	journal, err := OpenFileJournal(path, limit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { journal.Close() })
	return journal
}

func payload(id string) protocol.CommandPayload {
	return protocol.CommandPayload{CommandID: id, ResourceID: "catalog", Action: "restart"}
}

func TestJournalRefusesSecondProcessAndReleasesLock(t *testing.T) {
	if path := os.Getenv("HLCP_LOCK_TEST_PATH"); path != "" {
		if journal, err := OpenFileJournal(path, 16); err == nil {
			journal.Close()
			t.Fatal("second process acquired journal")
		} else if !strings.Contains(err.Error(), "locked") {
			t.Fatalf("unexpected child error: %v", err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "journal.json")
	journal := openTestJournal(t, path, 16)
	process := exec.Command(os.Args[0], "-test.run=^TestJournalRefusesSecondProcessAndReleasesLock$")
	process.Env = append(os.Environ(), "HLCP_LOCK_TEST_PATH="+path)
	if output, err := process.CombinedOutput(); err != nil {
		t.Fatalf("child: %v %s", err, output)
	}
	journal.Close()
	reopened := openTestJournal(t, path, 16)
	reopened.Close()
}

func TestJournalReleasesLockAfterAbruptProcessExit(t *testing.T) {
	if path := os.Getenv("HLCP_CRASH_TEST_PATH"); path != "" {
		journal, err := OpenFileJournal(path, 16)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := journal.Begin(payload("c")); err != nil {
			t.Fatal(err)
		}
		// Simulate a side effect after durable admission, then exit without Close.
		if err := os.WriteFile(path+".effect", []byte("1"), 0o600); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "journal.json")
	child := exec.Command(os.Args[0], "-test.run=^TestJournalReleasesLockAfterAbruptProcessExit$")
	child.Env = append(os.Environ(), "HLCP_CRASH_TEST_PATH="+path)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child failed: %v %s", err, output)
	}
	journal := openTestJournal(t, path, 16)
	entry, fresh, err := journal.Begin(payload("c"))
	if err != nil || fresh || entry.Result == nil || entry.Result.Code != "EXECUTION_UNKNOWN" {
		t.Fatalf("unsafe crash recovery: %+v %v %v", entry, fresh, err)
	}
}

func TestDispatcherStopsExecutionsAfterJournalFailure(t *testing.T) {
	journal := openTestJournal(t, filepath.Join(t.TempDir(), "journal.json"), 16)
	dispatcher := NewDispatcher(journal)
	t.Cleanup(func() { dispatcher.Close() })
	calls := 0
	dispatcher.Register("catalog", func(context.Context, string) protocol.CommandResultPayload {
		calls++
		journal.fault = func(string) error { return errors.New("disk failure") }
		return protocol.CommandResultPayload{Success: true, Code: "OK"}
	})
	_, result := dispatcher.Execute(payload("c"))
	if result.Code != "EXECUTION_UNKNOWN" {
		t.Fatalf("result=%+v", result)
	}
	journal.fault = nil
	dispatcher.Execute(payload("c"))
	dispatcher.Execute(payload("next"))
	if calls != 1 {
		t.Fatalf("repeated external side effect: %d", calls)
	}
}

func TestJournalRecoveryFailsClosed(t *testing.T) {
	for _, body := range []string{"", "{", `{}`, `{"version":99,"node_id":"","entries":[]}`, `{"version":2,"node_id":"other","entries":[]}`} {
		t.Run(body, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "journal.json")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if journal, err := OpenFileJournal(path, 16); err == nil {
				journal.Close()
				t.Fatal("accepted invalid journal")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != body {
				t.Fatal("invalid journal was modified")
			}
		})
	}
}

func TestLegacyMigrationPreservesBackupAndProtectsEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	original := `{"entries":[{"command":{"command_id":"c","resource_id":"catalog","action":"restart"},"result":{"command_id":"c","success":true,"code":"OK","message":""}}]}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	journal, err := OpenNodeJournal(path, 1, "debian-01")
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	backup, err := os.ReadFile(path + ".v1.bak")
	if err != nil || string(backup) != original {
		t.Fatal("legacy backup missing or changed")
	}
	if _, _, err := journal.Begin(payload("next")); !errors.Is(err, ErrJournalFull) {
		t.Fatalf("recent migrated entry evicted: %v", err)
	}
	data, _ := os.ReadFile(path)
	var document journalDocument
	if err := json.Unmarshal(data, &document); err != nil || document.Version != journalVersion || document.NodeID != "debian-01" {
		t.Fatalf("migration: %+v %v", document, err)
	}
}

func TestJournalRejectsResultIDMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	journal := openTestJournal(t, path, 16)
	journal.Begin(payload("c"))
	journal.Complete(protocol.CommandResultPayload{CommandID: "c", Success: true, Code: "OK"})
	journal.Close()
	data, _ := os.ReadFile(path)
	var document journalDocument
	json.Unmarshal(data, &document)
	document.Entries[0].Result.CommandID = "other"
	data, _ = json.Marshal(document)
	os.WriteFile(path, data, 0o600)
	if journal, err := OpenFileJournal(path, 16); err == nil {
		journal.Close()
		t.Fatal("accepted mismatched result")
	}
}

func TestJournalCapacityProtectsRecentAndUnknownEntries(t *testing.T) {
	for _, state := range []string{"running", "completed", "unknown"} {
		t.Run(state, func(t *testing.T) {
			journal := openTestJournal(t, filepath.Join(t.TempDir(), "journal.json"), 1)
			now := time.Now()
			journal.now = func() time.Time { return now }
			if _, _, err := journal.Begin(payload("c")); err != nil {
				t.Fatal(err)
			}
			if state != "running" {
				code := "OK"
				if state == "unknown" {
					code = "EXECUTION_UNKNOWN"
				}
				if err := journal.Complete(protocol.CommandResultPayload{CommandID: "c", Success: state == "completed", Code: code}); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := journal.Begin(payload("next")); !errors.Is(err, ErrJournalFull) {
				t.Fatalf("err=%v", err)
			}
			now = now.Add(25 * time.Hour)
			_, _, err := journal.Begin(payload("next"))
			if state == "completed" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrJournalFull) {
				t.Fatalf("unsafe entry evicted: %v", err)
			}
		})
	}
}

func TestJournalStorageFailureStopsAdmissionAndRecoversWithoutReplay(t *testing.T) {
	for _, stage := range []string{"create", "write", "sync", "close", "rename", "directory-sync"} {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "journal.json")
			journal := openTestJournal(t, path, 16)
			if _, _, err := journal.Begin(payload("c")); err != nil {
				t.Fatal(err)
			}
			journal.fault = func(current string) error {
				if current == stage {
					return errors.New("injected failure")
				}
				return nil
			}
			if err := journal.Complete(protocol.CommandResultPayload{CommandID: "c", Success: true, Code: "OK"}); err == nil {
				t.Fatal("storage failure ignored")
			}
			leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".hlcp-journal-*"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("temporary files left behind: %v %v", leftovers, err)
			}
			journal.fault = nil
			if _, _, err := journal.Begin(payload("next")); err == nil {
				t.Fatal("admission continued after storage failure")
			}
			journal.Close()
			restarted := openTestJournal(t, path, 16)
			entry, fresh, err := restarted.Begin(payload("c"))
			if err != nil || fresh || entry.Result == nil {
				t.Fatalf("unsafe recovery: %+v %v %v", entry, fresh, err)
			}
			expected := "EXECUTION_UNKNOWN"
			if stage == "directory-sync" {
				expected = "OK"
			}
			if entry.Result.Code != expected {
				t.Fatalf("result=%s expected=%s", entry.Result.Code, expected)
			}
		})
	}
}

func TestJournalByteLimitAndImmutableResult(t *testing.T) {
	journal := openTestJournal(t, filepath.Join(t.TempDir(), "journal.json"), 16)
	journal.maxBytes = 10
	if _, _, err := journal.Begin(payload("c")); !errors.Is(err, ErrJournalFull) {
		t.Fatalf("byte limit err=%v", err)
	}
	journal.maxBytes = defaultJournalBytes
	if _, _, err := journal.Begin(payload("c")); err != nil {
		t.Fatal(err)
	}
	result := protocol.CommandResultPayload{CommandID: "c", Success: true, Code: "OK"}
	if err := journal.Complete(result); err != nil {
		t.Fatal(err)
	}
	if err := journal.Complete(protocol.CommandResultPayload{CommandID: "c", Code: "EXECUTION_FAILED"}); err == nil {
		t.Fatal("terminal result changed")
	}
}
