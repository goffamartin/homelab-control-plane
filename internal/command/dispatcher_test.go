package command

import (
	"context"
	"hlcp/internal/protocol"
	"testing"
	"time"
)

func TestDispatcherCachesDuplicateCommand(t *testing.T) {
	dispatcher, calls := newTestDispatcher(t), 0
	dispatcher.Register("minecraft", func(_ context.Context, action string) protocol.CommandResultPayload {
		calls++
		return protocol.CommandResultPayload{Success: action == "restart", Code: "OK"}
	})
	command := protocol.CommandPayload{CommandID: "cmd-1", ResourceID: "minecraft", Action: "restart"}
	ack, result := dispatcher.Execute(command)
	if ack.Status != "accepted" || !result.Success {
		t.Fatalf("first result = %#v / %#v", ack, result)
	}
	ack, result = dispatcher.Execute(command)
	if ack.Status != "completed" || !result.Success || calls != 1 {
		t.Fatalf("duplicate result = %#v / %#v, calls=%d", ack, result, calls)
	}
}

func TestDispatcherMutatingStatusCancellationIsUnknown(t *testing.T) {
	for _, mutating := range []bool{false, true} {
		dispatcher := newTestDispatcher(t)
		started := make(chan struct{})
		execute := func(ctx context.Context, _ string) protocol.CommandResultPayload {
			close(started)
			<-ctx.Done()
			return protocol.CommandResultPayload{Code: "EXECUTION_UNKNOWN"}
		}
		if mutating {
			dispatcher.RegisterMutating("task", execute, "status")
		} else {
			dispatcher.Register("task", execute, "status")
		}
		admission := dispatcher.Admit(protocol.CommandPayload{CommandID: "status-check", ResourceID: "task", Action: "status"})
		dispatcher.Start(admission.Job)
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("executor not started")
		}
		if err := dispatcher.Close(); err != nil {
			t.Fatal(err)
		}
		want := "TIMEOUT"
		if mutating {
			want = "EXECUTION_UNKNOWN"
		}
		if admission.Job.Result.Code != want {
			t.Fatalf("mutating=%v result=%+v", mutating, admission.Job.Result)
		}
	}
}

func TestDispatcherRejectsUnknownResource(t *testing.T) {
	ack, result := newTestDispatcher(t).Execute(protocol.CommandPayload{CommandID: "cmd-1", ResourceID: "unknown", Action: "restart"})
	if ack.Status != "rejected" || result.Code != "TARGET_NOT_FOUND" {
		t.Fatalf("result = %#v / %#v", ack, result)
	}
}

func newTestDispatcher(t *testing.T) *Dispatcher {
	t.Helper()
	journal, err := OpenFileJournal(t.TempDir()+"/journal.json", 16)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := NewDispatcher(journal)
	t.Cleanup(func() { dispatcher.Close() })
	return dispatcher
}
