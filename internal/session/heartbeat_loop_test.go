package session

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"hlcp/internal/liveness"
)

func TestRunHeartbeatLoopTransitionsToOffline(t *testing.T) {
	controllerConnection, agentConnection := net.Pipe()
	defer controllerConnection.Close()
	defer agentConnection.Close()

	tracker, err := liveness.NewTracker(liveness.Policy{
		HeartbeatInterval: 20 * time.Millisecond,
		DegradedAfter:     1,
		OfflineAfter:      2,
	}, time.Now())
	if err != nil {
		t.Fatalf("NewTracker() error = %v", err)
	}
	changes := make(chan StateChange, 2)
	result := make(chan error, 1)
	go func() {
		result <- RunHeartbeatLoop(controllerConnection, tracker, func(change StateChange) { changes <- change })
	}()

	if err := <-result; !errors.Is(err, ErrNodeOffline) {
		t.Fatalf("RunHeartbeatLoop() error = %v, want ErrNodeOffline", err)
	}
	if first, second := <-changes, <-changes; first.State != liveness.NodeDegraded || second.State != liveness.NodeOffline {
		t.Fatalf("state changes = %#v, %#v; want degraded then offline", first, second)
	}
}

func TestRunHeartbeatLoopAcceptsIncreasingHeartbeats(t *testing.T) {
	controllerConnection, agentConnection := net.Pipe()
	defer controllerConnection.Close()
	defer agentConnection.Close()

	tracker, err := liveness.NewTracker(liveness.Policy{
		HeartbeatInterval: time.Second,
		DegradedAfter:     1,
		OfflineAfter:      2,
	}, time.Now())
	if err != nil {
		t.Fatalf("NewTracker() error = %v", err)
	}
	result := make(chan error, 1)
	go func() { result <- RunHeartbeatLoop(controllerConnection, tracker, nil) }()

	if err := SendHeartbeat(agentConnection, 4, 1, time.Second); err != nil {
		t.Fatalf("first SendHeartbeat() error = %v", err)
	}
	if err := SendHeartbeat(agentConnection, 5, 2, time.Second); err != nil {
		t.Fatalf("second SendHeartbeat() error = %v", err)
	}
	agentConnection.Close()
	if err := <-result; !errors.Is(err, io.EOF) {
		t.Fatalf("RunHeartbeatLoop() error = %v, want io.EOF", err)
	}
}

func TestRunHeartbeatLoopRejectsRepeatedSequence(t *testing.T) {
	controllerConnection, agentConnection := net.Pipe()
	defer controllerConnection.Close()
	defer agentConnection.Close()

	tracker, err := liveness.NewTracker(liveness.Policy{HeartbeatInterval: time.Second, DegradedAfter: 1, OfflineAfter: 2}, time.Now())
	if err != nil {
		t.Fatalf("NewTracker() error = %v", err)
	}
	result := make(chan error, 1)
	go func() { result <- RunHeartbeatLoop(controllerConnection, tracker, nil) }()

	if err := SendHeartbeat(agentConnection, 4, 1, time.Second); err != nil {
		t.Fatalf("first SendHeartbeat() error = %v", err)
	}
	if err := SendHeartbeat(agentConnection, 5, 1, time.Second); err != nil {
		t.Fatalf("second SendHeartbeat() error = %v", err)
	}
	if err := <-result; !errors.Is(err, ErrNonIncreasingHeartbeat) {
		t.Fatalf("RunHeartbeatLoop() error = %v, want ErrNonIncreasingHeartbeat", err)
	}
}
