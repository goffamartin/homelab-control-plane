package session

import (
	"context"
	"fmt"
	"hlcp/internal/command"
	"hlcp/internal/protocol"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestAgentAcknowledgesBeforeExecutionAndDeduplicatesAcrossReconnect(t *testing.T) {
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
		}
		return protocol.CommandResultPayload{Success: true, Code: "OK"}
	})
	payload := protocol.CommandPayload{CommandID: "c", ResourceID: "catalog", Action: "restart"}
	connect := func() (net.Conn, *Writer, context.CancelFunc, chan error) {
		agent, controller := net.Pipe()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		writer := NewWriter(agent, 1)
		go func() { done <- RunAgentCommandLoop(ctx, agent, writer, dispatcher) }()
		controller.SetDeadline(time.Now().Add(2 * time.Second))
		return controller, writer, cancel, done
	}
	controller, agentWriter, cancel, done := connect()
	defer controller.Close()
	defer cancel()
	if err := NewWriter(controller, 1).Send(protocol.MessageCommand, payload); err != nil {
		t.Fatal(err)
	}
	frame, err := protocol.Decode(controller)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Header.Type != protocol.MessageCommandACK {
		t.Fatalf("first frame=%v", frame.Header.Type)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("ACK did not release worker")
	}
	if err := NewWriter(controller, 2).Send(protocol.MessageCommand, payload); err != nil {
		t.Fatal(err)
	}
	frame, err = protocol.Decode(controller)
	if err != nil {
		t.Fatal(err)
	}
	var ack protocol.CommandACKPayload
	protocol.DecodePayload(frame.Payload, &ack)
	if frame.Header.Type != protocol.MessageCommandACK || ack.Status != "in_progress" {
		t.Fatalf("duplicate=%+v", ack)
	}
	heartbeatDone := make(chan error, 1)
	go func() {
		heartbeatDone <- agentWriter.Send(protocol.MessageHeartbeat, protocol.HeartbeatPayload{Sequence: 1})
	}()
	frame, err = protocol.Decode(controller)
	if err != nil || frame.Header.Type != protocol.MessageHeartbeat {
		t.Fatalf("heartbeat frame=%v err=%v", frame.Header.Type, err)
	}
	if err := <-heartbeatDone; err != nil {
		t.Fatal(err)
	}
	cancel()
	controller.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("old reader did not stop")
	}
	newer, _, cancelNew, newDone := connect()
	defer newer.Close()
	defer cancelNew()
	if err := NewWriter(newer, 1).Send(protocol.MessageCommand, payload); err != nil {
		t.Fatal(err)
	}
	frame, err = protocol.Decode(newer)
	if err != nil || frame.Header.Type != protocol.MessageCommandACK {
		t.Fatalf("new ACK=%v %v", frame.Header.Type, err)
	}
	close(release)
	frame, err = protocol.Decode(newer)
	if err != nil || frame.Header.Type != protocol.MessageCommandResult {
		t.Fatalf("result frame=%v err=%v", frame.Header.Type, err)
	}
	var result protocol.CommandResultPayload
	protocol.DecodePayload(frame.Payload, &result)
	if !result.Success || calls.Load() != 1 {
		t.Fatalf("result=%+v calls=%d", result, calls.Load())
	}
	cancelNew()
	newer.Close()
	<-newDone
}

func TestAgentFullQueueStillAcknowledgesDuplicatesAndRejectsExcess(t *testing.T) {
	journal, err := command.OpenFileJournal(filepath.Join(t.TempDir(), "journal.json"), 64)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := command.NewDispatcher(journal)
	defer dispatcher.Close()
	dispatcher.Register("catalog", func(ctx context.Context, _ string) protocol.CommandResultPayload {
		<-ctx.Done()
		return protocol.CommandResultPayload{Code: "EXECUTION_UNKNOWN"}
	})
	agent, controller := net.Pipe()
	defer controller.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunAgentCommandLoop(ctx, agent, NewWriter(agent, 1), dispatcher) }()
	defer func() { cancel(); controller.Close(); <-done }()
	controller.SetDeadline(time.Now().Add(5 * time.Second))
	writer := NewWriter(controller, 1)
	send := func(id string) protocol.CommandACKPayload {
		t.Helper()
		if err := writer.Send(protocol.MessageCommand, protocol.CommandPayload{CommandID: id, ResourceID: "catalog", Action: "restart"}); err != nil {
			t.Fatal(err)
		}
		frame, err := protocol.Decode(controller)
		if err != nil || frame.Header.Type != protocol.MessageCommandACK {
			t.Fatalf("frame=%v err=%v", frame.Header.Type, err)
		}
		var ack protocol.CommandACKPayload
		if err := protocol.DecodePayload(frame.Payload, &ack); err != nil {
			t.Fatal(err)
		}
		return ack
	}
	for i := 0; i < command.QueueCapacity; i++ {
		if ack := send(fmt.Sprintf("c-%d", i)); ack.Status != "accepted" {
			t.Fatalf("ack=%+v", ack)
		}
	}
	if ack := send("c-0"); ack.Status != "in_progress" {
		t.Fatalf("duplicate ACK=%+v", ack)
	}
	if ack := send("extra"); ack.Status != "rejected" {
		t.Fatalf("excess ACK=%+v", ack)
	}
	frame, err := protocol.Decode(controller)
	if err != nil || frame.Header.Type != protocol.MessageCommandResult {
		t.Fatalf("frame=%v err=%v", frame.Header.Type, err)
	}
	var result protocol.CommandResultPayload
	if err := protocol.DecodePayload(frame.Payload, &result); err != nil || result.Code != "QUEUE_FULL" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
