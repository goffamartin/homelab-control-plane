package controller

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"hlcp/internal/protocol"
	"hlcp/internal/session"
)

func retrySession(t *testing.T, policy RetryPolicy) (*ActiveSessions, *session.Writer, net.Conn) {
	t.Helper()
	sessions := NewActiveSessions(NewCommandTracker())
	sessions.policy = policy
	connection, peer := net.Pipe()
	writer := session.NewWriter(connection, 1)
	_, cancel := context.WithCancel(context.Background())
	sessions.Publish(session.Registration{NodeID: "n", SessionID: "s", Capabilities: []string{"resource.control", "docker"}}, session.Synchronization{Inventory: protocol.InventorySnapshot{Resources: []protocol.ResourceDescriptor{{ID: "catalog", Type: "container", Actions: []string{"restart"}}}}}, time.Now(), writer, cancel)
	t.Cleanup(func() { cancel(); connection.Close(); peer.Close() })
	peer.SetDeadline(time.Now().Add(2 * time.Second))
	return sessions, writer, peer
}

func retryCommand() protocol.CommandPayload {
	return protocol.CommandPayload{CommandID: "c", ResourceID: "catalog", Action: "restart"}
}

func TestAcknowledgementStopsExecutionRetries(t *testing.T) {
	for _, status := range []string{"accepted", "in_progress", "completed", "rejected"} {
		t.Run(status, func(t *testing.T) {
			sessions, writer, peer := retrySession(t, RetryPolicy{ACKTimeout: 25 * time.Millisecond, ResultTimeout: time.Second, MaxAttempts: 3})
			done := make(chan error, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				result, err := sessions.Execute(ctx, "n", retryCommand())
				if err == nil && result.Success != (status != "rejected") {
					err = fmt.Errorf("result=%+v", result)
				}
				done <- err
			}()
			frame, err := protocol.Decode(peer)
			if err != nil || frame.Header.Type != protocol.MessageCommand {
				t.Fatalf("frame=%v err=%v", frame.Header.Type, err)
			}
			if !sessions.HandleAck("n", writer, protocol.CommandACKPayload{CommandID: "c", Status: status}) {
				t.Fatal("ACK rejected")
			}
			peer.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			if _, err := protocol.Decode(peer); err == nil {
				t.Fatal("execution was retried after ACK")
			} else {
				var timeout net.Error
				if !errors.As(err, &timeout) || !timeout.Timeout() {
					t.Fatalf("read=%v", err)
				}
			}
			progress, ok := sessions.tracker.Progress("n", "c")
			if !ok || progress.Attempts != 1 || progress.AckStatus != status || progress.AdmittedAt.IsZero() || progress.Deadline.IsZero() || progress.LastAttemptAt.IsZero() {
				t.Fatalf("progress=%+v", progress)
			}
			code := "OK"
			if status == "rejected" {
				code = "ACTION_NOT_SUPPORTED"
			}
			if !sessions.HandleResult("n", writer, protocol.CommandResultPayload{CommandID: "c", Success: status != "rejected", Code: code}) {
				t.Fatal("result rejected")
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("execution did not complete")
			}
		})
	}
}

func TestMissingACKUsesBoundedAttempts(t *testing.T) {
	sessions, _, peer := retrySession(t, RetryPolicy{ACKTimeout: 30 * time.Millisecond, ResultTimeout: time.Second, MaxAttempts: 2})
	done := make(chan error, 1)
	go func() { _, err := sessions.Execute(context.Background(), "n", retryCommand()); done <- err }()
	for i := 0; i < 2; i++ {
		frame, err := protocol.Decode(peer)
		if err != nil || frame.Header.Type != protocol.MessageCommand {
			t.Fatalf("attempt=%d err=%v", i, err)
		}
		var command protocol.CommandPayload
		if err := protocol.DecodePayload(frame.Payload, &command); err != nil || command != retryCommand() {
			t.Fatalf("command=%+v err=%v", command, err)
		}
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrACKTimeout) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("retry limit ignored")
	}
}

func TestACKWriteAndResultHaveDistinctTimeouts(t *testing.T) {
	t.Run("blocked-write", func(t *testing.T) {
		sessions, _, _ := retrySession(t, RetryPolicy{ACKTimeout: 30 * time.Millisecond, ResultTimeout: time.Second, MaxAttempts: 1})
		done := make(chan error, 1)
		go func() { _, err := sessions.Execute(context.Background(), "n", retryCommand()); done <- err }()
		select {
		case err := <-done:
			if !errors.Is(err, ErrACKTimeout) {
				t.Fatalf("err=%v", err)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatal("send exceeded ACK deadline")
		}
	})
	t.Run("missing-result", func(t *testing.T) {
		sessions, writer, peer := retrySession(t, RetryPolicy{ACKTimeout: 30 * time.Millisecond, ResultTimeout: 150 * time.Millisecond, MaxAttempts: 1})
		done := make(chan error, 1)
		go func() { _, err := sessions.Execute(context.Background(), "n", retryCommand()); done <- err }()
		if _, err := protocol.Decode(peer); err != nil {
			t.Fatal(err)
		}
		sessions.HandleAck("n", writer, protocol.CommandACKPayload{CommandID: "c", Status: "accepted"})
		select {
		case err := <-done:
			if !errors.Is(err, ErrResultTimeout) {
				t.Fatalf("err=%v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("result deadline ignored")
		}
	})
}

func TestCanceledCallerNeverSendsOrTracks(t *testing.T) {
	sessions, _, peer := retrySession(t, DefaultRetryPolicy())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sessions.Execute(ctx, "n", retryCommand()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if _, exists := sessions.tracker.Progress("n", "c"); exists {
		t.Fatal("canceled command was admitted")
	}
	peer.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	if _, err := protocol.Decode(peer); err == nil {
		t.Fatal("canceled command was sent")
	}
}

func TestTrackerSeparatesNodesAndBoundsCapacity(t *testing.T) {
	tracker := NewCommandTracker()
	for _, node := range []string{"a", "b"} {
		if err := tracker.TrackCommand(node, retryCommand(), time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if tracker.AckNode("foreign", protocol.CommandACKPayload{CommandID: "c", Status: "accepted"}) {
		t.Fatal("foreign ACK accepted")
	}
	if !tracker.CompleteNode("a", protocol.CommandResultPayload{CommandID: "c", Success: true, Code: "OK"}) {
		t.Fatal("node a result rejected")
	}
	if !tracker.PendingNode("b", "c") {
		t.Fatal("node a result completed node b command")
	}
	for i := 2; i < maxActiveCommands; i++ {
		if err := tracker.TrackNode("n", fmt.Sprintf("c-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tracker.TrackNode("n", "overflow"); !errors.Is(err, ErrCommandCapacity) {
		t.Fatalf("err=%v", err)
	}
}

func TestRetryPolicyValidation(t *testing.T) {
	for _, policy := range []RetryPolicy{{}, {ACKTimeout: -1, ResultTimeout: time.Second, MaxAttempts: 1}, {ACKTimeout: time.Second, ResultTimeout: time.Second, MaxAttempts: 101}, {ACKTimeout: 2 * time.Second, ResultTimeout: time.Second, MaxAttempts: 1}} {
		if policy.Validate() == nil {
			t.Fatalf("accepted=%+v", policy)
		}
	}
	if err := DefaultRetryPolicy().Validate(); err != nil {
		t.Fatal(err)
	}
}
