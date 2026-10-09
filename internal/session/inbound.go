package session

import (
	"fmt"
	"hlcp/internal/protocol"
	"net"
)

// Inbound owns the sole reader for a controller-side session.
type Inbound struct {
	Heartbeats chan protocol.HeartbeatPayload
	Acks       chan protocol.CommandACKPayload
	Results    chan protocol.CommandResultPayload
	Errors     chan error
}

func StartInbound(connection net.Conn) *Inbound {
	inbound := &Inbound{Heartbeats: make(chan protocol.HeartbeatPayload, 16), Acks: make(chan protocol.CommandACKPayload, 16), Results: make(chan protocol.CommandResultPayload, 16), Errors: make(chan error, 1)}
	go func() {
		defer close(inbound.Heartbeats)
		defer close(inbound.Acks)
		defer close(inbound.Results)
		for {
			frame, err := protocol.Decode(connection)
			if err != nil {
				inbound.Errors <- err
				return
			}
			switch frame.Header.Type {
			case protocol.MessageHeartbeat:
				var value protocol.HeartbeatPayload
				err = protocol.DecodePayload(frame.Payload, &value)
				if err == nil {
					err = value.Validate()
				}
				if err == nil {
					inbound.Heartbeats <- value
				}
			case protocol.MessageCommandACK:
				var value protocol.CommandACKPayload
				err = protocol.DecodePayload(frame.Payload, &value)
				if err == nil {
					err = value.Validate()
				}
				if err == nil {
					inbound.Acks <- value
				}
			case protocol.MessageCommandResult:
				var value protocol.CommandResultPayload
				err = protocol.DecodePayload(frame.Payload, &value)
				if err == nil {
					err = value.Validate()
				}
				if err == nil {
					inbound.Results <- value
				}
			default:
				err = fmt.Errorf("unexpected inbound message type %d", frame.Header.Type)
			}
			if err != nil {
				inbound.Errors <- err
				return
			}
		}
	}()
	return inbound
}
