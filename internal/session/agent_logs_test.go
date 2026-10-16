package session

import (
	"context"
	"fmt"
	"hlcp/internal/command"
	"hlcp/internal/protocol"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentLogsBoundJobsAndCancelAll(t *testing.T) {
	a, c := net.Pipe()
	defer a.Close()
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := newAgentLogs(ctx, NewWriter(a, 1), []LogHandler{func(ctx context.Context, _ protocol.LogRequest, _ func(protocol.LogRecord) error) string {
		<-ctx.Done()
		return "CANCELED"
	}})
	q := protocol.DefaultLogQuery()
	q.Follow = true
	for i := 0; i < 2; i++ {
		if err := l.request(protocol.LogRequest{RequestID: fmt.Sprint("r", i), ResourceID: "app", Query: q}); err != nil {
			t.Fatal(err)
		}
		frame, err := protocol.Decode(c)
		if err != nil {
			t.Fatal(err)
		}
		var event protocol.LogEvent
		if protocol.DecodePayload(frame.Payload, &event) != nil || event.Kind != "start" {
			t.Fatal(event)
		}
	}
	done := make(chan error, 1)
	go func() { done <- l.request(protocol.LogRequest{RequestID: "third", ResourceID: "app", Query: q}) }()
	frame, err := protocol.Decode(c)
	if err != nil {
		t.Fatal(err)
	}
	var event protocol.LogEvent
	if protocol.DecodePayload(frame.Payload, &event) != nil || event.Code != "CAPACITY" || !event.Gap || event.Validate() != nil {
		t.Fatal(event)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	cancel()
	joined := make(chan struct{})
	go func() { l.workers.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("workers leaked")
	}
}

func TestAgentLogCancellationAndCommandIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	journal, err := command.OpenFileJournal(path, 16)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := command.NewDispatcher(journal)
	defer dispatcher.Close()
	before, _ := os.ReadFile(path)
	a, c := net.Pipe()
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	writer := NewWriter(a, 1)
	peer := NewWriter(c, 1)
	done := make(chan error, 1)
	go func() {
		done <- RunAgentCommandLoop(ctx, a, writer, dispatcher, func(ctx context.Context, r protocol.LogRequest, emit func(protocol.LogRecord) error) string {
			if err := emit(protocol.LogRecord{Stream: "stdout", Text: "secret log text"}); err != nil {
				return "CANCELED"
			}
			<-ctx.Done()
			return "CANCELED"
		})
	}()
	q := protocol.DefaultLogQuery()
	q.Follow = true
	if peer.Send(protocol.MessageLogRequest, protocol.LogRequest{RequestID: "read-1", ResourceID: "app", Query: q}) != nil {
		t.Fatal("send")
	}
	for _, kind := range []string{"start", "record"} {
		frame, err := protocol.Decode(c)
		if err != nil {
			t.Fatal(err)
		}
		var event protocol.LogEvent
		if protocol.DecodePayload(frame.Payload, &event) != nil || event.Kind != kind {
			t.Fatal(event)
		}
	}
	heartbeat := make(chan error, 1)
	go func() { heartbeat <- writer.Send(protocol.MessageHeartbeat, protocol.HeartbeatPayload{Sequence: 1}) }()
	frame, err := protocol.Decode(c)
	if err != nil || frame.Header.Type != protocol.MessageHeartbeat {
		t.Fatal(frame, err)
	}
	if <-heartbeat != nil {
		t.Fatal("heartbeat blocked")
	}
	if peer.Send(protocol.MessageLogCancel, protocol.LogCancel{RequestID: "read-1"}) != nil {
		t.Fatal("cancel send")
	}
	frame, err = protocol.Decode(c)
	if err != nil {
		t.Fatal(err)
	}
	var event protocol.LogEvent
	protocol.DecodePayload(frame.Payload, &event)
	if event.Kind != "end" || event.Code != "CANCELED" {
		t.Fatal(event)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("log read changed command journal")
	}
	cancel()
	c.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("log worker did not join")
	}
}
