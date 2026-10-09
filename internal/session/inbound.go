package session

import (
	"context"
	"fmt"
	"hlcp/internal/protocol"
	"net"
	"time"
)

type InboundEvent struct {
	Heartbeat  *protocol.HeartbeatPayload
	Ack        *protocol.CommandACKPayload
	Result     *protocol.CommandResultPayload
	ReceivedAt time.Time
}

// Inbound preserves wire ordering and owns the only reader. Cancellation closes
// the socket and interrupts even a full dispatch queue.
type Inbound struct {
	Events chan InboundEvent
	Errors chan error
	Done   chan struct{}
}

func StartInbound(ctx context.Context, connection net.Conn) *Inbound {
	inbound := &Inbound{Events: make(chan InboundEvent, 16), Errors: make(chan error, 1), Done: make(chan struct{})}
	go func() {
		stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
		defer stop()
		defer close(inbound.Done)
		defer close(inbound.Events)
		var sequence uint64
		for {
			frame, err := protocol.Decode(connection)
			if err != nil {
				inbound.Errors <- err
				return
			}
			event := InboundEvent{ReceivedAt: time.Now()}
			switch frame.Header.Type {
			case protocol.MessageHeartbeat:
				value := new(protocol.HeartbeatPayload)
				err = protocol.DecodePayload(frame.Payload, value)
				if err == nil {
					err = value.Validate()
				}
				if err == nil && value.Sequence <= sequence {
					err = ErrNonIncreasingHeartbeat
				}
				if err == nil {
					sequence = value.Sequence
					event.Heartbeat = value
				}
			case protocol.MessageCommandACK:
				value := new(protocol.CommandACKPayload)
				err = protocol.DecodePayload(frame.Payload, value)
				if err == nil {
					err = value.Validate()
				}
				event.Ack = value
			case protocol.MessageCommandResult:
				value := new(protocol.CommandResultPayload)
				err = protocol.DecodePayload(frame.Payload, value)
				if err == nil {
					err = value.Validate()
				}
				event.Result = value
			default:
				err = fmt.Errorf("unexpected inbound message type %d", frame.Header.Type)
			}
			if err != nil {
				inbound.Errors <- err
				return
			}
			select {
			case inbound.Events <- event:
			case <-ctx.Done():
				inbound.Errors <- ctx.Err()
				return
			}
		}
	}()
	return inbound
}
