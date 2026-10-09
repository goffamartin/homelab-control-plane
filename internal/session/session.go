// Package session implements the first HLCP application-session exchange.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"slices"
	"time"

	"hlcp/internal/protocol"
)

const defaultHandshakeTimeout = 10 * time.Second

var (
	ErrExpectedHello             = errors.New("hlcp session: expected HELLO")
	ErrExpectedWelcome           = errors.New("hlcp session: expected WELCOME")
	ErrExpectedInventorySnapshot = errors.New("hlcp session: expected INVENTORY_SNAPSHOT")
	ErrExpectedStateSnapshot     = errors.New("hlcp session: expected STATE_SNAPSHOT")
	ErrNoCommonVersion           = errors.New("hlcp session: no common protocol version")
	ErrInvalidControllerConfig   = errors.New("hlcp session: invalid controller configuration")
)

// Registration is the controller-side result of a validated HELLO/WELCOME
// exchange. The caller owns the connection after Accept returns successfully.
type Registration struct {
	NodeID       string
	AgentVersion string
	Capabilities []string
	SessionID    string
	Version      uint8
}

// Synchronization is the complete controller-visible state received directly
// after a new session is registered.
type Synchronization struct {
	Inventory protocol.InventorySnapshot
	State     protocol.StateSnapshot
}

// ControllerConfig selects the policy returned to a newly connected agent.
type ControllerConfig struct {
	SupportedVersions        []uint8
	HeartbeatInterval        time.Duration
	MissedHeartbeatThreshold uint8
	HandshakeTimeout         time.Duration
	NewSessionID             func() (string, error)
}

// Accept receives HELLO and responds with WELCOME on an already established,
// mutually authenticated TLS connection.
func Accept(connection net.Conn, config ControllerConfig) (Registration, error) {
	config = config.withDefaults()
	if err := config.validate(); err != nil {
		return Registration{}, err
	}
	if err := connection.SetDeadline(time.Now().Add(config.HandshakeTimeout)); err != nil {
		return Registration{}, fmt.Errorf("set handshake deadline: %w", err)
	}
	defer connection.SetDeadline(time.Time{})

	frame, err := protocol.Decode(connection)
	if err != nil {
		return Registration{}, fmt.Errorf("read HELLO frame: %w", err)
	}
	if frame.Header.Type != protocol.MessageHello {
		return Registration{}, ErrExpectedHello
	}

	var hello protocol.HelloPayload
	if err := protocol.DecodePayload(frame.Payload, &hello); err != nil {
		return Registration{}, fmt.Errorf("decode HELLO payload: %w", err)
	}
	if err := hello.Validate(); err != nil {
		return Registration{}, fmt.Errorf("validate HELLO payload: %w", err)
	}

	version, err := negotiateVersion(hello.ProtocolVersions, config.SupportedVersions)
	if err != nil {
		return Registration{}, err
	}
	sessionID, err := config.NewSessionID()
	if err != nil {
		return Registration{}, fmt.Errorf("create session ID: %w", err)
	}
	welcome := protocol.WelcomePayload{
		ProtocolVersion:          version,
		SessionID:                sessionID,
		HeartbeatIntervalMillis:  uint32(config.HeartbeatInterval / time.Millisecond),
		MissedHeartbeatThreshold: config.MissedHeartbeatThreshold,
	}
	payload, err := protocol.EncodePayload(welcome)
	if err != nil {
		return Registration{}, fmt.Errorf("encode WELCOME payload: %w", err)
	}
	response, err := protocol.NewFrame(protocol.MessageWelcome, 1, payload)
	if err != nil {
		return Registration{}, fmt.Errorf("create WELCOME frame: %w", err)
	}
	if err := protocol.WriteFrame(connection, response); err != nil {
		return Registration{}, err
	}

	return Registration{
		NodeID:       hello.NodeID,
		AgentVersion: hello.AgentVersion,
		Capabilities: slices.Clone(hello.Capabilities),
		SessionID:    sessionID,
		Version:      version,
	}, nil
}

// Start sends HELLO and validates the controller's WELCOME on an already
// established, mutually authenticated TLS connection.
func Start(connection net.Conn, hello protocol.HelloPayload, timeout time.Duration) (protocol.WelcomePayload, error) {
	if err := hello.Validate(); err != nil {
		return protocol.WelcomePayload{}, fmt.Errorf("validate HELLO payload: %w", err)
	}
	if timeout <= 0 {
		timeout = defaultHandshakeTimeout
	}
	if err := connection.SetDeadline(time.Now().Add(timeout)); err != nil {
		return protocol.WelcomePayload{}, fmt.Errorf("set handshake deadline: %w", err)
	}
	defer connection.SetDeadline(time.Time{})

	payload, err := protocol.EncodePayload(hello)
	if err != nil {
		return protocol.WelcomePayload{}, fmt.Errorf("encode HELLO payload: %w", err)
	}
	request, err := protocol.NewFrame(protocol.MessageHello, 1, payload)
	if err != nil {
		return protocol.WelcomePayload{}, fmt.Errorf("create HELLO frame: %w", err)
	}
	if err := protocol.WriteFrame(connection, request); err != nil {
		return protocol.WelcomePayload{}, err
	}

	response, err := protocol.Decode(connection)
	if err != nil {
		return protocol.WelcomePayload{}, fmt.Errorf("read WELCOME frame: %w", err)
	}
	if response.Header.Type != protocol.MessageWelcome {
		return protocol.WelcomePayload{}, ErrExpectedWelcome
	}
	var welcome protocol.WelcomePayload
	if err := protocol.DecodePayload(response.Payload, &welcome); err != nil {
		return protocol.WelcomePayload{}, fmt.Errorf("decode WELCOME payload: %w", err)
	}
	if err := welcome.ValidateAgainst(hello); err != nil {
		return protocol.WelcomePayload{}, fmt.Errorf("validate WELCOME payload: %w", err)
	}
	return welcome, nil
}

// SendInitialSynchronization sends the complete current inventory followed by
// a state snapshot. The caller must invoke it after Start succeeds.
func SendInitialSynchronization(connection net.Conn, synchronization Synchronization, timeout time.Duration) error {
	if err := synchronization.Inventory.Validate(); err != nil {
		return fmt.Errorf("validate inventory snapshot: %w", err)
	}
	if err := synchronization.State.ValidateAgainst(synchronization.Inventory); err != nil {
		return fmt.Errorf("validate state snapshot: %w", err)
	}
	if timeout <= 0 {
		timeout = defaultHandshakeTimeout
	}
	if err := connection.SetDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("set synchronization deadline: %w", err)
	}
	defer connection.SetDeadline(time.Time{})

	if err := writePayloadFrame(connection, protocol.MessageInventorySnapshot, 2, synchronization.Inventory); err != nil {
		return fmt.Errorf("write inventory snapshot: %w", err)
	}
	if err := writePayloadFrame(connection, protocol.MessageStateSnapshot, 3, synchronization.State); err != nil {
		return fmt.Errorf("write state snapshot: %w", err)
	}
	return nil
}

// ReceiveInitialSynchronization receives the initial inventory and state in
// wire order. The controller must call it after Accept succeeds and before it
// treats a session as ready for commands.
func ReceiveInitialSynchronization(connection net.Conn, timeout time.Duration) (Synchronization, error) {
	if timeout <= 0 {
		timeout = defaultHandshakeTimeout
	}
	if err := connection.SetDeadline(time.Now().Add(timeout)); err != nil {
		return Synchronization{}, fmt.Errorf("set synchronization deadline: %w", err)
	}
	defer connection.SetDeadline(time.Time{})

	inventory, err := readPayloadFrame[protocol.InventorySnapshot](connection, protocol.MessageInventorySnapshot)
	if err != nil {
		return Synchronization{}, err
	}
	if err := inventory.Validate(); err != nil {
		return Synchronization{}, fmt.Errorf("validate inventory snapshot: %w", err)
	}
	state, err := readPayloadFrame[protocol.StateSnapshot](connection, protocol.MessageStateSnapshot)
	if err != nil {
		return Synchronization{}, err
	}
	if err := state.ValidateAgainst(inventory); err != nil {
		return Synchronization{}, fmt.Errorf("validate state snapshot: %w", err)
	}
	return Synchronization{Inventory: inventory, State: state}, nil
}

// SendHeartbeat reports liveness after the initial synchronization completes.
// Sequence must strictly increase for one agent session; sequence semantics are
// checked by the controller dispatcher added in a later stage.
func SendHeartbeat(connection net.Conn, messageID, sequence uint64, timeout time.Duration) error {
	heartbeat := protocol.HeartbeatPayload{Sequence: sequence}
	if err := heartbeat.Validate(); err != nil {
		return err
	}
	if timeout <= 0 {
		timeout = defaultHandshakeTimeout
	}
	if err := connection.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("set heartbeat write deadline: %w", err)
	}
	defer connection.SetWriteDeadline(time.Time{})
	if err := writePayloadFrame(connection, protocol.MessageHeartbeat, messageID, heartbeat); err != nil {
		return fmt.Errorf("write heartbeat: %w", err)
	}
	return nil
}

// ReceiveHeartbeat reads and validates a single heartbeat frame. A controller
// must use its receipt time, rather than an agent wall clock, for liveness.
func ReceiveHeartbeat(connection net.Conn, timeout time.Duration) (protocol.HeartbeatPayload, error) {
	if timeout <= 0 {
		timeout = defaultHandshakeTimeout
	}
	if err := connection.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return protocol.HeartbeatPayload{}, fmt.Errorf("set heartbeat read deadline: %w", err)
	}
	defer connection.SetReadDeadline(time.Time{})
	heartbeat, err := readPayloadFrame[protocol.HeartbeatPayload](connection, protocol.MessageHeartbeat)
	if err != nil {
		return protocol.HeartbeatPayload{}, err
	}
	if err := heartbeat.Validate(); err != nil {
		return protocol.HeartbeatPayload{}, fmt.Errorf("validate heartbeat: %w", err)
	}
	return heartbeat, nil
}

func (config ControllerConfig) withDefaults() ControllerConfig {
	if len(config.SupportedVersions) == 0 {
		config.SupportedVersions = []uint8{protocol.ProtocolVersion}
	}
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = 5 * time.Second
	}
	if config.MissedHeartbeatThreshold == 0 {
		config.MissedHeartbeatThreshold = 3
	}
	if config.HandshakeTimeout <= 0 {
		config.HandshakeTimeout = defaultHandshakeTimeout
	}
	if config.NewSessionID == nil {
		config.NewSessionID = randomSessionID
	}
	return config
}

func (config ControllerConfig) validate() error {
	milliseconds := config.HeartbeatInterval / time.Millisecond
	if config.HeartbeatInterval < time.Millisecond || config.HeartbeatInterval%time.Millisecond != 0 || milliseconds > time.Duration(^uint32(0)) {
		return fmt.Errorf("%w: heartbeat interval must be an exact millisecond representable as uint32", ErrInvalidControllerConfig)
	}
	if config.MissedHeartbeatThreshold == 0 || config.HandshakeTimeout <= 0 || len(config.SupportedVersions) == 0 || config.NewSessionID == nil {
		return ErrInvalidControllerConfig
	}
	return nil
}

func negotiateVersion(agentVersions, controllerVersions []uint8) (uint8, error) {
	var selected uint8
	found := false
	for _, version := range agentVersions {
		if slices.Contains(controllerVersions, version) && (!found || version > selected) {
			selected = version
			found = true
		}
	}
	if found {
		return selected, nil
	}
	return 0, ErrNoCommonVersion
}

func randomSessionID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func writePayloadFrame(connection net.Conn, messageType protocol.MessageType, messageID uint64, payloadValue any) error {
	payload, err := protocol.EncodePayload(payloadValue)
	if err != nil {
		return err
	}
	frame, err := protocol.NewFrame(messageType, messageID, payload)
	if err != nil {
		return err
	}
	return protocol.WriteFrame(connection, frame)
}

func readPayloadFrame[T any](connection net.Conn, expectedType protocol.MessageType) (T, error) {
	var payloadValue T
	frame, err := protocol.Decode(connection)
	if err != nil {
		return payloadValue, err
	}
	if frame.Header.Type != expectedType {
		switch expectedType {
		case protocol.MessageInventorySnapshot:
			return payloadValue, ErrExpectedInventorySnapshot
		case protocol.MessageStateSnapshot:
			return payloadValue, ErrExpectedStateSnapshot
		case protocol.MessageHeartbeat:
			return payloadValue, fmt.Errorf("hlcp session: expected HEARTBEAT")
		default:
			return payloadValue, fmt.Errorf("hlcp session: expected message type %d", expectedType)
		}
	}
	if err := protocol.DecodePayload(frame.Payload, &payloadValue); err != nil {
		return payloadValue, err
	}
	return payloadValue, nil
}
