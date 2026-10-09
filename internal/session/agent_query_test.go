package session

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"hlcp/internal/command"
	"hlcp/internal/protocol"
)

func TestAgentQueryNeverAdmitsOrStartsExecution(t *testing.T) {
	journal, err := command.OpenFileJournal(filepath.Join(t.TempDir(), "journal.json"), 16)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := command.NewDispatcher(journal)
	defer dispatcher.Close()
	called := make(chan struct{}, 1)
	dispatcher.Register("catalog", func(context.Context, string) protocol.CommandResultPayload {
		called <- struct{}{}
		return protocol.CommandResultPayload{Success: true, Code: "OK"}
	})
	agent, controller := net.Pipe()
	defer controller.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunAgentCommandLoop(ctx, agent, NewWriter(agent, 1), dispatcher) }()
	defer func() { cancel(); controller.Close(); <-done }()
	controller.SetDeadline(time.Now().Add(2 * time.Second))
	writer := NewWriter(controller, 1)
	payload := protocol.CommandPayload{CommandID: "c", ResourceID: "catalog", Action: "restart"}
	query := func(want string) {
		t.Helper()
		if err := writer.Send(protocol.MessageCommandQuery, payload); err != nil {
			t.Fatal(err)
		}
		frame, err := protocol.Decode(controller)
		if err != nil || frame.Header.Type != protocol.MessageCommandState {
			t.Fatalf("type=%v err=%v", frame.Header.Type, err)
		}
		var state protocol.CommandStatePayload
		if err := protocol.DecodePayload(frame.Payload, &state); err != nil || state.State != want || state.Validate() != nil {
			t.Fatalf("state=%+v err=%v", state, err)
		}
	}
	query("not_found")
	if _, exists, err := journal.Lookup(payload); err != nil || exists {
		t.Fatal("query admitted a missing command")
	}
	admission := dispatcher.Admit(payload)
	if admission.Ack.Status != "accepted" {
		t.Fatalf("admission=%+v", admission)
	}
	query("queued")
	select {
	case <-called:
		t.Fatal("query started queued execution")
	default:
	}
	dispatcher.AbortBeforeACK(admission.Job)
	select {
	case <-admission.Job.Done:
	case <-time.After(time.Second):
		t.Fatal("abort blocked")
	}
	query("completed")
	payload.Action = "stop"
	query("unknown")
}
