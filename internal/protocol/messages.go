// Package protocol contains the transport-independent HLCP wire contract.
package protocol

// ProtocolVersion is the only HLCP wire version supported by this build.
const ProtocolVersion uint8 = 1

// MessageType identifies the meaning of a framed payload.
type MessageType uint8

const (
	MessageHello MessageType = iota + 1
	MessageWelcome
	MessageGoodbye
	MessageError
	MessageHeartbeat
	MessagePing
	MessagePong
	MessageInventorySnapshot
	MessageStateSnapshot
	MessageStateUpdate
	MessageCommand
	MessageCommandACK
	MessageCommandResult
	MessageEvent
	MessageMetric
)

// IsKnown reports whether messageType belongs to the HLCP v1 message set.
func (messageType MessageType) IsKnown() bool {
	return messageType >= MessageHello && messageType <= MessageMetric
}

// Flags are per-message transport hints. Their detailed semantics are added
// with the structured message types; unknown flags are rejected in v1.
type Flags uint16

const (
	FlagNone Flags = 0
)
