package session

import (
	"context"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"hlcp/internal/command"
	"hlcp/internal/protocol"
)

func TestRepeatedReconnectJoinsCommandWaitersAndInboundReaders(t *testing.T) {
	journal, err := command.OpenFileJournal(filepath.Join(t.TempDir(), "journal.json"), 16)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := command.NewDispatcher(journal)
	defer dispatcher.Close()
	release := make(chan struct{})
	started := make(chan struct{})
	var calls atomic.Int32
	dispatcher.Register("catalog", func(ctx context.Context, _ string) protocol.CommandResultPayload {
		calls.Add(1)
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return protocol.CommandResultPayload{Code: "EXECUTION_UNKNOWN"}
		}
		return protocol.CommandResultPayload{Success: true, Code: "OK"}
	})
	payload := protocol.CommandPayload{CommandID: "reconnect", ResourceID: "catalog", Action: "restart"}
	for i := 0; i < 64; i++ {
		func() {
			agent, controller := net.Pipe()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			inbound := StartInbound(ctx, controller)
			agentWriter := NewWriter(agent, 1)
			done := make(chan error, 1)
			go func() { done <- RunAgentCommandLoop(ctx, agent, agentWriter, dispatcher) }()
			defer func() {
				cancel()
				agent.Close()
				controller.Close()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("agent reader/result waiters leaked")
				}
				select {
				case <-inbound.Done:
				case <-time.After(time.Second):
					t.Fatal("inbound reader leaked")
				}
				if len(agentWriter.slots) != 0 || len(agentWriter.gate) != 0 {
					t.Fatal("writer reservation leaked")
				}
			}()
			if err := NewWriter(controller, 1).SendContext(ctx, protocol.MessageCommand, payload); err != nil {
				t.Fatal(err)
			}
			select {
			case event := <-inbound.Events:
				want := "in_progress"
				if i == 0 {
					want = "accepted"
				}
				if event.Ack == nil || event.Ack.Status != want {
					t.Fatalf("session %d ACK=%+v", i, event.Ack)
				}
			case <-ctx.Done():
				t.Fatal("ACK stalled")
			}
			if i == 0 {
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatal("first ACK did not start worker")
				}
			}
			if i == 63 {
				close(release)
				select {
				case event := <-inbound.Events:
					if event.Result == nil || !event.Result.Success {
						t.Fatalf("result=%+v", event.Result)
					}
				case <-ctx.Done():
					t.Fatal("final result stalled")
				}
			}
		}()
	}
	if calls.Load() != 1 {
		t.Fatalf("executions=%d want=1", calls.Load())
	}
}
