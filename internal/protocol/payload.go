package protocol

import (
	"errors"
	"fmt"
	"strings"

	"github.com/fxamacker/cbor/v2"
)

const (
	maxNodeIDLength       = 128
	maxAgentVersionLength = 128
	maxSessionIDLength    = 128
	maxCapabilities       = 64
	maxCapabilityLength   = 128
)

var (
	ErrInvalidNodeID            = errors.New("hlcp: invalid node ID")
	ErrInvalidSessionID         = errors.New("hlcp: invalid session ID")
	ErrNoProtocolVersions       = errors.New("hlcp: no protocol versions supplied")
	ErrProtocolNotOffered       = errors.New("hlcp: selected protocol version was not offered")
	ErrInvalidHeartbeat         = errors.New("hlcp: invalid heartbeat configuration")
	ErrInvalidHeartbeatSequence = errors.New("hlcp: invalid heartbeat sequence")
	ErrInvalidCommand           = errors.New("hlcp: invalid command")
	ErrTooManyCapabilities      = errors.New("hlcp: too many capabilities")
)

var (
	canonicalCBOR cbor.EncMode
	boundedCBOR   cbor.DecMode
)

func init() {
	var err error
	canonicalCBOR, err = cbor.CanonicalEncOptions().EncMode()
	if err != nil {
		panic(fmt.Sprintf("create canonical CBOR encoder: %v", err))
	}

	boundedCBOR, err = (cbor.DecOptions{
		DupMapKey:        cbor.DupMapKeyEnforcedAPF,
		IndefLength:      cbor.IndefLengthForbidden,
		MaxNestedLevels:  16,
		MaxArrayElements: maxCapabilities,
		MaxMapPairs:      32,
	}).DecMode()
	if err != nil {
		panic(fmt.Sprintf("create bounded CBOR decoder: %v", err))
	}
}

// HelloPayload starts a session and states the agent's supported protocol
// versions and capabilities. NodeID is stable across reconnects.
type HelloPayload struct {
	ProtocolVersions []uint8  `cbor:"protocol_versions"`
	NodeID           string   `cbor:"node_id"`
	AgentVersion     string   `cbor:"agent_version"`
	Capabilities     []string `cbor:"capabilities"`
}

// Validate checks the limits that make HELLO safe to retain and display.
func (payload HelloPayload) Validate() error {
	if len(payload.ProtocolVersions) == 0 {
		return ErrNoProtocolVersions
	}
	if !isIdentifier(payload.NodeID, maxNodeIDLength) {
		return ErrInvalidNodeID
	}
	if len(payload.AgentVersion) == 0 || len(payload.AgentVersion) > maxAgentVersionLength {
		return fmt.Errorf("hlcp: invalid agent version")
	}
	if len(payload.Capabilities) > maxCapabilities {
		return ErrTooManyCapabilities
	}
	for _, capability := range payload.Capabilities {
		if !isIdentifier(capability, maxCapabilityLength) {
			return fmt.Errorf("hlcp: invalid capability %q", capability)
		}
	}
	return nil
}

// WelcomePayload confirms the selected protocol version and controller liveness
// policy for one newly established session.
type WelcomePayload struct {
	ProtocolVersion          uint8  `cbor:"protocol_version"`
	SessionID                string `cbor:"session_id"`
	HeartbeatIntervalMillis  uint32 `cbor:"heartbeat_interval_ms"`
	MissedHeartbeatThreshold uint8  `cbor:"missed_heartbeat_threshold"`
}

// HeartbeatPayload carries an agent-local increasing sequence number. It does
// not use wall-clock time because controller and agent clocks need not agree.
type HeartbeatPayload struct {
	Sequence uint64 `cbor:"sequence"`
}

// CommandPayload names an allow-listed resource action. It deliberately has no
// shell command or adapter target.
type CommandPayload struct {
	CommandID  string `cbor:"command_id"`
	ResourceID string `cbor:"resource_id"`
	Action     string `cbor:"action"`
}

func (payload CommandPayload) Validate() error {
	if !isIdentifier(payload.CommandID, 128) || !isIdentifier(payload.ResourceID, maxResourceIDLength) || !isIdentifier(payload.Action, maxActionLength) {
		return ErrInvalidCommand
	}
	return nil
}

// CommandACKPayload proves that the agent accepted this command for processing.
type CommandACKPayload struct {
	CommandID string `cbor:"command_id"`
	Status    string `cbor:"status"`
}

// CommandResultPayload is the immutable terminal result cached for duplicate delivery.
type CommandResultPayload struct {
	CommandID string `cbor:"command_id"`
	Success   bool   `cbor:"success"`
	Code      string `cbor:"code"`
	Message   string `cbor:"message"`
}

// Validate checks the minimal heartbeat contract for v1.
func (payload HeartbeatPayload) Validate() error {
	if payload.Sequence == 0 {
		return ErrInvalidHeartbeatSequence
	}
	return nil
}

// ValidateAgainst checks whether the controller selected an offered version and
// supplied a useful session configuration.
func (payload WelcomePayload) ValidateAgainst(hello HelloPayload) error {
	if err := hello.Validate(); err != nil {
		return err
	}
	if !isIdentifier(payload.SessionID, maxSessionIDLength) {
		return ErrInvalidSessionID
	}
	if payload.HeartbeatIntervalMillis == 0 || payload.MissedHeartbeatThreshold == 0 {
		return ErrInvalidHeartbeat
	}
	for _, version := range hello.ProtocolVersions {
		if version == payload.ProtocolVersion {
			return nil
		}
	}
	return fmt.Errorf("%w: %d", ErrProtocolNotOffered, payload.ProtocolVersion)
}

// EncodePayload emits canonical CBOR for a supported structured payload.
func EncodePayload(payload any) ([]byte, error) {
	return canonicalCBOR.Marshal(payload)
}

// DecodePayload decodes CBOR using resource limits. Call the concrete
// payload's validation method afterwards because syntax alone is not semantics.
func DecodePayload(encoded []byte, target any) error {
	if uint64(len(encoded)) > uint64(MaxPayloadLength) {
		return ErrPayloadTooLarge
	}
	return boundedCBOR.Unmarshal(encoded, target)
}

func isIdentifier(value string, maxLength int) bool {
	return len(value) > 0 && len(value) <= maxLength && value == strings.TrimSpace(value)
}
