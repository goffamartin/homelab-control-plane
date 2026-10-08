package session

import (
	"fmt"
	"hlcp/internal/command"
	"hlcp/internal/protocol"
	"net"
)

// RunAgentCommandLoop receives commands and returns ACK plus terminal RESULT.
func RunAgentCommandLoop(connection net.Conn, writer *Writer, dispatcher *command.Dispatcher) error {
	for {
		frame, err := protocol.Decode(connection)
		if err != nil {
			return err
		}
		if frame.Header.Type != protocol.MessageCommand {
			return fmt.Errorf("unexpected agent message type %d", frame.Header.Type)
		}
		var commandPayload protocol.CommandPayload
		if err := protocol.DecodePayload(frame.Payload, &commandPayload); err != nil {
			return err
		}
		ack, result := dispatcher.Execute(commandPayload)
		if err := writer.Send(protocol.MessageCommandACK, ack); err != nil {
			return err
		}
		if err := writer.Send(protocol.MessageCommandResult, result); err != nil {
			return err
		}
	}
}
