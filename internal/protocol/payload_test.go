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
