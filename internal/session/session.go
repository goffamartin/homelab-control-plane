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
	ErrExpectedHello   = errors.New("hlcp session: expected HELLO")
	ErrExpectedWelcome = errors.New("hlcp session: expected WELCOME")
	ErrNoCommonVersion = errors.New("hlcp session: no common protocol version")
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

func negotiateVersion(agentVersions, controllerVersions []uint8) (uint8, error) {
	for agentIndex := len(agentVersions) - 1; agentIndex >= 0; agentIndex-- {
		if slices.Contains(controllerVersions, agentVersions[agentIndex]) {
			return agentVersions[agentIndex], nil
		}
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
