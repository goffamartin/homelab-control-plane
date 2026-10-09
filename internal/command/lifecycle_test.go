package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"hlcp/internal/protocol"
)

func TestDispatcherBoundsQueueAndDoesNotExecuteBeforeStart(t *testing.T) {
	journal := openTestJournal(t, filepath.Join(t.TempDir(), "journal.json"), 64)
	dispatcher := NewDispatcher(journal)
	defer dispatcher.Close()
	started := make(chan struct{}, 1)
	dispatcher.Register("catalog", func(ctx context.Context, _ string) protocol.CommandResultPayload {
		started <- struct{}{}
		return protocol.CommandResultPayload{Success: true, Code: "OK"}
	})
	for index := 0; index < QueueCapacity; index++ {
		command := payload(fmt.Sprintf("c-%d", index))
		if admission := dispatcher.Admit(command); admission.Ack.Status != "accepted" {
			t.Fatalf("admission=%+v", admission)
		}
	}
	select {
	case <-started:
		t.Fatal("executed before ACK permission")
	default:
	}
	rejected := dispatcher.Admit(payload("extra"))
	if rejected.Result == nil || rejected.Result.Code != "QUEUE_FULL" {
		t.Fatalf("admission=%+v", rejected)
	}
	duplicate := dispatcher.Admit(payload("c-0"))
	if duplicate.Ack.Status != "in_progress" || duplicate.Result != nil {
		t.Fatalf("duplicate=%+v", duplicate)
	}
}

func TestRunningPersistenceFailureNeverInvokesExecutor(t *testing.T) {
	for _, stage := range []string{"create", "write", "sync", "close", "rename", "directory-sync"} {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "journal.json")
			journal := openTestJournal(t, path, 16)
			dispatcher := NewDispatcher(journal)
			defer dispatcher.Close()
			called := make(chan struct{}, 1)
			dispatcher.Register("catalog", func(context.Context, string) protocol.CommandResultPayload {
				called <- struct{}{}
				return protocol.CommandResultPayload{Success: true, Code: "OK"}
			})
			admission := dispatcher.Admit(payload("c"))
			journal.fault = func(current string) error {
				if current == stage {
					return errors.New("injected running persistence failure")
				}
				return nil
			}
			dispatcher.Start(admission.Job)
			select {
			case <-admission.Job.Done:
			case <-time.After(time.Second):
				t.Fatal("worker did not finish")
			}
			select {
			case <-called:
				t.Fatal("executed without durable running transition")
			default:
			}
			if admission.Job.Result.Success || dispatcher.Admit(payload("next")).Ack.Status != "rejected" {
				t.Fatal("journal failure did not fail closed")
			}
			if err := dispatcher.Close(); err != nil {
				t.Fatal(err)
			}
			restarted := openTestJournal(t, path, 16)
			entry, _, err := restarted.Lookup(payload("c"))
			want := "EXECUTION_NOT_STARTED"
			if stage == "directory-sync" {
				want = "EXECUTION_UNKNOWN"
			}
			if err != nil || entry.Result == nil || entry.Result.Code != want {
				t.Fatalf("recovered=%+v err=%v want=%s", entry, err, want)
			}
		})
	}
}

func TestVersionTwoMigrationPreservesUnknownAndBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	original := `{"version":2,"node_id":"","entries":[{"command":{"command_id":"c","resource_id":"catalog","action":"restart"},"admitted_at":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	journal := openTestJournal(t, path, 16)
	entry, exists, err := journal.Lookup(payload("c"))
	if err != nil || !exists || entry.Result == nil || entry.Result.Code != "EXECUTION_UNKNOWN" {
		t.Fatalf("entry=%+v exists=%v err=%v", entry, exists, err)
	}
	backup, err := os.ReadFile(path + ".v2.bak")
	if err != nil || string(backup) != original {
		t.Fatalf("backup err=%v", err)
	}
	data, err := os.ReadFile(path)
	var document journalDocument
	if err != nil || json.Unmarshal(data, &document) != nil || document.Version != journalVersion {
		t.Fatalf("document=%+v err=%v", document, err)
	}
}

func TestDispatcherBoundsUTF8Result(t *testing.T) {
	journal := openTestJournal(t, filepath.Join(t.TempDir(), "journal.json"), 16)
	dispatcher := NewDispatcher(journal)
	defer dispatcher.Close()
	dispatcher.Register("catalog", func(context.Context, string) protocol.CommandResultPayload {
		return protocol.CommandResultPayload{Success: true, Code: "OK", Message: strings.Repeat("€", 2000)}
	})
	_, result := dispatcher.Execute(payload("c"))
	if !result.Success || len(result.Message) > 4096 || !utf8.ValidString(result.Message) {
		t.Fatalf("result bytes=%d code=%s", len(result.Message), result.Code)
	}
}

func TestFailedACKDoesNotExecute(t *testing.T) {
	journal := openTestJournal(t, filepath.Join(t.TempDir(), "journal.json"), 16)
	dispatcher := NewDispatcher(journal)
	defer dispatcher.Close()
	dispatcher.Register("catalog", func(context.Context, string) protocol.CommandResultPayload {
		t.Error("executor called after failed ACK")
		return protocol.CommandResultPayload{Success: true, Code: "OK"}
	})
	admission := dispatcher.Admit(payload("c"))
	dispatcher.AbortBeforeACK(admission.Job)
	select {
	case <-admission.Job.Done:
	case <-time.After(time.Second):
		t.Fatal("aborted job did not complete")
	}
	if admission.Job.Result.Code != "EXECUTION_NOT_STARTED" {
		t.Fatalf("result=%+v", admission.Job.Result)
	}
}

func TestQueuedRestartIsDefinitivelyNotExecuted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	journal := openTestJournal(t, path, 16)
	if _, _, err := journal.BeginQueued(payload("c")); err != nil {
		t.Fatal(err)
	}
	journal.Close()
	restarted := openTestJournal(t, path, 16)
	entry, fresh, err := restarted.Begin(payload("c"))
	if err != nil || fresh || entry.Result.Code != "EXECUTION_NOT_STARTED" {
		t.Fatalf("entry=%+v fresh=%v err=%v", entry, fresh, err)
	}
}

func TestShutdownCancelsRunningWorkAndRecordsUnknown(t *testing.T) {
	journal := openTestJournal(t, filepath.Join(t.TempDir(), "journal.json"), 16)
	dispatcher := NewDispatcher(journal)
	started := make(chan struct{})
	dispatcher.Register("catalog", func(ctx context.Context, _ string) protocol.CommandResultPayload {
		close(started)
		<-ctx.Done()
		return protocol.CommandResultPayload{Success: true, Code: "OK"}
	})
	admission := dispatcher.Admit(payload("c"))
	dispatcher.Start(admission.Job)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	closed := make(chan struct{})
	go func() { dispatcher.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	if admission.Job.Result.Code != "EXECUTION_UNKNOWN" {
		t.Fatalf("result=%+v", admission.Job.Result)
	}
}
