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

func TestNegotiateVersionChoosesHighestCommonVersion(t *testing.T) {
	version, err := negotiateVersion([]uint8{3, 1, 2}, []uint8{1, 2})
	if err != nil || version != 2 {
		t.Fatalf("negotiateVersion() = %d, %v; want 2", version, err)
	}
}

func TestControllerConfigRejectsUnrepresentableHeartbeatInterval(t *testing.T) {
	controllerConnection, peerConnection := net.Pipe()
	defer controllerConnection.Close()
	defer peerConnection.Close()
	_, err := Accept(controllerConnection, ControllerConfig{HeartbeatInterval: time.Microsecond})
	if !errors.Is(err, ErrInvalidControllerConfig) {
		t.Fatalf("Accept() error = %v, want ErrInvalidControllerConfig", err)
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

func TestInitialSynchronizationAfterRegistration(t *testing.T) {
	controllerConnection, agentConnection := net.Pipe()
	defer controllerConnection.Close()
	defer agentConnection.Close()

	result := make(chan struct {
		synchronization Synchronization
		err             error
	}, 1)
	go func() {
		if _, err := Accept(controllerConnection, ControllerConfig{NewSessionID: func() (string, error) { return "session-001", nil }}); err != nil {
			result <- struct {
				synchronization Synchronization
				err             error
			}{err: err}
			return
		}
		synchronization, err := ReceiveInitialSynchronization(controllerConnection, time.Second)
		result <- struct {
			synchronization Synchronization
			err             error
		}{synchronization, err}
	}()

	hello := protocol.HelloPayload{ProtocolVersions: []uint8{protocol.ProtocolVersion}, NodeID: "debian-01", AgentVersion: "0.1.0"}
	if _, err := Start(agentConnection, hello, time.Second); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	synchronization := Synchronization{
		Inventory: protocol.InventorySnapshot{Revision: 3, Resources: []protocol.ResourceDescriptor{
			{ID: "minecraft", Type: "service", Actions: []string{"status", "restart"}},
		}},
		State: protocol.StateSnapshot{InventoryRevision: 3, Resources: []protocol.ResourceState{
			{ResourceID: "minecraft", State: "running"},
		}},
	}
	if err := SendInitialSynchronization(agentConnection, synchronization, time.Second); err != nil {
		t.Fatalf("SendInitialSynchronization() error = %v", err)
	}

	received := <-result
	if received.err != nil {
		t.Fatalf("ReceiveInitialSynchronization() error = %v", received.err)
	}
	if received.synchronization.Inventory.Revision != 3 || received.synchronization.State.Resources[0].State != "running" {
		t.Fatalf("received synchronization = %#v", received.synchronization)
	}
}

func TestReceiveInitialSynchronizationRejectsUnexpectedOrder(t *testing.T) {
	controllerConnection, peerConnection := net.Pipe()
	defer controllerConnection.Close()
	defer peerConnection.Close()

	result := make(chan error, 1)
	go func() {
		_, err := ReceiveInitialSynchronization(controllerConnection, time.Second)
		result <- err
	}()
	if err := writePayloadFrame(peerConnection, protocol.MessageStateSnapshot, 1, protocol.StateSnapshot{}); err != nil {
		t.Fatalf("writePayloadFrame() error = %v", err)
	}
	if err := <-result; !errors.Is(err, ErrExpectedInventorySnapshot) {
		t.Fatalf("ReceiveInitialSynchronization() error = %v, want ErrExpectedInventorySnapshot", err)
	}
}

func TestSendAndReceiveHeartbeat(t *testing.T) {
	controllerConnection, agentConnection := net.Pipe()
	defer controllerConnection.Close()
	defer agentConnection.Close()

	result := make(chan struct {
		heartbeat protocol.HeartbeatPayload
		err       error
	}, 1)
	go func() {
		heartbeat, err := ReceiveHeartbeat(controllerConnection, time.Second)
		result <- struct {
			heartbeat protocol.HeartbeatPayload
			err       error
		}{heartbeat, err}
	}()

	if err := SendHeartbeat(agentConnection, 4, 1, time.Second); err != nil {
		t.Fatalf("SendHeartbeat() error = %v", err)
	}
	received := <-result
	if received.err != nil {
		t.Fatalf("ReceiveHeartbeat() error = %v", received.err)
	}
	if received.heartbeat.Sequence != 1 {
		t.Fatalf("heartbeat sequence = %d, want 1", received.heartbeat.Sequence)
	}
}
