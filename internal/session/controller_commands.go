package session

import (
	"fmt"
	"net"

	"hlcp/internal/protocol"
)

// SendCommand writes a validated command through the session's sole writer.
func SendCommand(writer *Writer, command protocol.CommandPayload) error {
	if err := command.Validate(); err != nil {
		return err
	}
	return writer.Send(protocol.MessageCommand, command)
}

// ReceiveCommandOutcome consumes the ACK and terminal RESULT for one command.
// It is intentionally synchronous until the controller reader multiplexer is
// added; callers must not run it beside another reader on the same connection.
func ReceiveCommandOutcome(connection net.Conn, commandID string) (protocol.CommandACKPayload, protocol.CommandResultPayload, error) {
	ackFrame, err := protocol.Decode(connection)
	if err != nil {
		return protocol.CommandACKPayload{}, protocol.CommandResultPayload{}, err
	}
	if ackFrame.Header.Type != protocol.MessageCommandACK {
		return protocol.CommandACKPayload{}, protocol.CommandResultPayload{}, fmt.Errorf("expected COMMAND_ACK, got %d", ackFrame.Header.Type)
	}
	var ack protocol.CommandACKPayload
	if err := protocol.DecodePayload(ackFrame.Payload, &ack); err != nil {
		return ack, protocol.CommandResultPayload{}, err
	}
	if err := ack.Validate(); err != nil {
		return ack, protocol.CommandResultPayload{}, err
	}
	resultFrame, err := protocol.Decode(connection)
	if err != nil {
		return ack, protocol.CommandResultPayload{}, err
	}
	if resultFrame.Header.Type != protocol.MessageCommandResult {
		return ack, protocol.CommandResultPayload{}, fmt.Errorf("expected COMMAND_RESULT, got %d", resultFrame.Header.Type)
	}
	var result protocol.CommandResultPayload
	if err := protocol.DecodePayload(resultFrame.Payload, &result); err != nil {
		return ack, result, err
	}
	if err := result.Validate(); err != nil {
		return ack, result, err
	}
	if ack.CommandID != commandID || result.CommandID != commandID {
		return ack, result, fmt.Errorf("command correlation mismatch")
	}
	return ack, result, nil
}
