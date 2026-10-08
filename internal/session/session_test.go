package session

import (
	"errors"
	"net"
	"testing"
	"time"

	"hlcp/internal/protocol"
)

func TestStartAndAcceptRegisterAgent(t *testing.T) {
	controllerConnection, agentConnection := net.Pipe()
	defer controllerConnection.Close()
	defer agentConnection.Close()

	result := make(chan struct {
		registration Registration
		err          error
	}, 1)
	go func() {
		registration, err := Accept(controllerConnection, ControllerConfig{
			HeartbeatInterval:        2 * time.Second,
			MissedHeartbeatThreshold: 4,
			NewSessionID:             func() (string, error) { return "session-001", nil },
		})
		result <- struct {
			registration Registration
			err          error
		}{registration, err}
	}()

	hello := protocol.HelloPayload{
		ProtocolVersions: []uint8{protocol.ProtocolVersion},
		NodeID:           "debian-01",
		AgentVersion:     "0.1.0",
		Capabilities:     []string{"resource.control", "systemd"},
	}
	welcome, err := Start(agentConnection, hello, time.Second)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	registration := <-result
	if registration.err != nil {
		t.Fatalf("Accept() error = %v", registration.err)
	}
	if registration.registration.NodeID != hello.NodeID {
		t.Fatalf("registered node = %q, want %q", registration.registration.NodeID, hello.NodeID)
	}
	if registration.registration.SessionID != "session-001" || welcome.SessionID != "session-001" {
		t.Fatalf("session IDs = %q/%q, want session-001", registration.registration.SessionID, welcome.SessionID)
	}
	if welcome.HeartbeatIntervalMillis != 2000 || welcome.MissedHeartbeatThreshold != 4 {
		t.Fatalf("welcome = %#v, want 2 s interval and threshold 4", welcome)
	}
}

func TestAcceptRejectsNonHelloMessage(t *testing.T) {
	controllerConnection, peerConnection := net.Pipe()
	defer controllerConnection.Close()
	defer peerConnection.Close()

	result := make(chan error, 1)
	go func() {
		_, err := Accept(controllerConnection, ControllerConfig{})
		result <- err
	}()

	frame, err := protocol.NewFrame(protocol.MessageHeartbeat, 1, nil)
	if err != nil {
		t.Fatalf("NewFrame() error = %v", err)
	}
	if err := protocol.WriteFrame(peerConnection, frame); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	if err := <-result; !errors.Is(err, ErrExpectedHello) {
		t.Fatalf("Accept() error = %v, want ErrExpectedHello", err)
	}
}
