package protocol

import (
	"errors"
	"testing"
)

func TestHelloPayloadCBORRoundTrip(t *testing.T) {
	original := HelloPayload{
		ProtocolVersions: []uint8{ProtocolVersion},
		NodeID:           "debian-01",
		AgentVersion:     "0.1.0",
		Capabilities:     []string{"system.metrics", "resource.control", "systemd"},
	}

	encoded, err := EncodePayload(original)
	if err != nil {
		t.Fatalf("EncodePayload() error = %v", err)
	}

	var decoded HelloPayload
	if err := DecodePayload(encoded, &decoded); err != nil {
		t.Fatalf("DecodePayload() error = %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("decoded.Validate() error = %v", err)
	}
	if decoded.NodeID != original.NodeID || decoded.AgentVersion != original.AgentVersion {
		t.Fatalf("decoded = %#v, want %#v", decoded, original)
	}
}

func TestWelcomePayloadRejectsUnofferedVersion(t *testing.T) {
	hello := HelloPayload{
		ProtocolVersions: []uint8{ProtocolVersion},
		NodeID:           "debian-01",
		AgentVersion:     "0.1.0",
	}
	welcome := WelcomePayload{
		ProtocolVersion:          ProtocolVersion + 1,
		SessionID:                "session-123",
		HeartbeatIntervalMillis:  5000,
		MissedHeartbeatThreshold: 3,
	}

	err := welcome.ValidateAgainst(hello)
	if !errors.Is(err, ErrProtocolNotOffered) {
		t.Fatalf("ValidateAgainst() error = %v, want ErrProtocolNotOffered", err)
	}
}

func TestHelloPayloadRejectsMissingNodeID(t *testing.T) {
	payload := HelloPayload{
		ProtocolVersions: []uint8{ProtocolVersion},
		AgentVersion:     "0.1.0",
	}

	err := payload.Validate()
	if !errors.Is(err, ErrInvalidNodeID) {
		t.Fatalf("Validate() error = %v, want ErrInvalidNodeID", err)
	}
}

func TestHeartbeatPayloadRejectsZeroSequence(t *testing.T) {
	err := (HeartbeatPayload{}).Validate()
	if !errors.Is(err, ErrInvalidHeartbeatSequence) {
		t.Fatalf("Validate() error = %v, want ErrInvalidHeartbeatSequence", err)
	}
}

func TestCommandPayloadRoundTrip(t *testing.T) {
	original := CommandPayload{CommandID: "cmd-001", ResourceID: "minecraft", Action: "restart"}
	encoded, err := EncodePayload(original)
	if err != nil {
		t.Fatalf("EncodePayload() error = %v", err)
	}
	var decoded CommandPayload
	if err := DecodePayload(encoded, &decoded); err != nil {
		t.Fatalf("DecodePayload() error = %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if decoded != original {
		t.Fatalf("decoded = %#v, want %#v", decoded, original)
	}
}

func TestCommandRejectsUnsafeIdentifier(t *testing.T) {
	payload := CommandPayload{CommandID: "cmd/../../other", ResourceID: "catalog", Action: "restart"}
	if err := payload.Validate(); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("Validate() error = %v, want ErrInvalidCommand", err)
	}
}

func TestCommandAcknowledgementAndResultValidation(t *testing.T) {
	if err := (CommandACKPayload{CommandID: "cmd-1", Status: "accepted"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (CommandACKPayload{CommandID: "cmd-1", Status: "maybe"}).Validate(); !errors.Is(err, ErrInvalidCommandACK) {
		t.Fatalf("ACK validation error = %v", err)
	}
	if err := (CommandResultPayload{CommandID: "cmd-1", Success: true, Code: "EXECUTION_FAILED"}).Validate(); !errors.Is(err, ErrInvalidCommandResult) {
		t.Fatalf("result validation error = %v", err)
	}
}
