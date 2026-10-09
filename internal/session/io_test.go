package session

import (
	"context"
	"errors"
	"hlcp/internal/liveness"
	"hlcp/internal/protocol"
	"net"
	"testing"
	"time"
)

func TestWriterStopsWhenPeerDoesNotRead(t *testing.T) {
	connection, peer := net.Pipe()
	defer connection.Close()
	defer peer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- NewWriter(connection, 1).SendContext(ctx, protocol.MessageHeartbeat, protocol.HeartbeatPayload{Sequence: 1})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("write unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("write ignored deadline")
	}
}

func TestWriterCanceledBeforeSendDoesNotWrite(t *testing.T) {
	connection, peer := net.Pipe()
	defer connection.Close()
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewWriter(connection, 1).SendContext(ctx, protocol.MessageHeartbeat, protocol.HeartbeatPayload{Sequence: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestWriterRejectsOverCapacityWithoutWriting(t *testing.T) {
	connection, peer := net.Pipe()
	defer connection.Close()
	defer peer.Close()
	writer := NewWriter(connection, 1)
	for index := 0; index < cap(writer.slots); index++ {
		writer.slots <- struct{}{}
	}
	if err := writer.Send(protocol.MessageHeartbeat, protocol.HeartbeatPayload{Sequence: 1}); !errors.Is(err, ErrWriteQueueFull) {
		t.Fatalf("err=%v", err)
	}
}

func TestInboundCancellationUnblocksFullQueue(t *testing.T) {
	connection, peer := net.Pipe()
	defer connection.Close()
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inbound := StartInbound(ctx, connection)
	for sequence := uint64(1); sequence <= 17; sequence++ {
		if err := NewWriter(peer, sequence).Send(protocol.MessageHeartbeat, protocol.HeartbeatPayload{Sequence: sequence}); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	select {
	case <-inbound.Done:
	case <-time.After(time.Second):
		t.Fatal("reader blocked on full queue after cancellation")
	}
}

func TestProductionInboundRejectsRepeatedHeartbeat(t *testing.T) {
	connection, peer := net.Pipe()
	defer connection.Close()
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inbound := StartInbound(ctx, connection)
	writer := NewWriter(peer, 1)
	for index := 0; index < 2; index++ {
		if err := writer.Send(protocol.MessageHeartbeat, protocol.HeartbeatPayload{Sequence: 1}); err != nil {
			t.Fatal(err)
		}
	}
	tracker, _ := liveness.NewTracker(liveness.Policy{HeartbeatInterval: time.Second, DegradedAfter: 2, OfflineAfter: 3}, time.Now())
	if err := RunInboundLoop(ctx, inbound, tracker, nil, nil, nil); !errors.Is(err, ErrNonIncreasingHeartbeat) {
		t.Fatalf("err=%v", err)
	}
}

func TestProductionInboundPublishesLastSeenForKnownCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := time.Now().Add(-100 * time.Millisecond)
	observed := time.Now()
	tracker, _ := liveness.NewTracker(liveness.Policy{HeartbeatInterval: time.Second, DegradedAfter: 2, OfflineAfter: 3}, started)
	inbound := &Inbound{Events: make(chan InboundEvent, 1), Errors: make(chan error, 1)}
	inbound.Events <- InboundEvent{ReceivedAt: observed, Ack: &protocol.CommandACKPayload{CommandID: "c", Status: "accepted"}}
	called := false
	err := RunInboundLoop(ctx, inbound, tracker, func(change StateChange) {
		called = true
		if !change.LastActivity.Equal(observed) {
			t.Error("last seen was not updated")
		}
		cancel()
	}, func(protocol.CommandACKPayload) bool { return true }, nil)
	if !called || !errors.Is(err, context.Canceled) {
		t.Fatalf("called=%v err=%v", called, err)
	}
}
