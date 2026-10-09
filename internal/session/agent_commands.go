package session

import (
	"context"
	"fmt"
	"hlcp/internal/command"
	"hlcp/internal/protocol"
	"net"
	"sync"
)

// The reader never waits for execution. Result waiters are bounded by the
// dispatcher's admitted jobs and are joined when this connection ends.
func RunAgentCommandLoop(parent context.Context, connection net.Conn, writer *Writer, dispatcher *command.Dispatcher) error {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	var waiters sync.WaitGroup
	var mutex sync.Mutex
	watching := make(map[*command.Job]bool)
	watchSlots := make(chan struct{}, command.QueueCapacity)
	defer func() { cancel(); _ = connection.Close(); waiters.Wait(); stop() }()
	for {
		frame, err := protocol.Decode(connection)
		if err != nil {
			return err
		}
		if frame.Header.Type != protocol.MessageCommand {
			return fmt.Errorf("unexpected agent message type %d", frame.Header.Type)
		}
		var payload protocol.CommandPayload
		if err := protocol.DecodePayload(frame.Payload, &payload); err != nil {
			return err
		}
		admission := dispatcher.Admit(payload)
		reserved := false
		if admission.Job != nil {
			mutex.Lock()
			if !watching[admission.Job] {
				select {
				case watchSlots <- struct{}{}:
					watching[admission.Job] = true
					reserved = true
				default:
					if admission.Ack.Status == "accepted" {
						dispatcher.AbortBeforeACK(admission.Job)
					}
					result := protocol.CommandResultPayload{CommandID: payload.CommandID, Code: "QUEUE_FULL", Message: "session result capacity is full"}
					admission = command.Admission{Ack: protocol.CommandACKPayload{CommandID: payload.CommandID, Status: "rejected"}, Result: &result}
				}
			}
			mutex.Unlock()
		}
		if err := writer.SendContext(ctx, protocol.MessageCommandACK, admission.Ack); err != nil {
			if admission.Job != nil && admission.Ack.Status == "accepted" {
				dispatcher.AbortBeforeACK(admission.Job)
			}
			return err
		}
		if admission.Result != nil {
			if err := writer.SendContext(ctx, protocol.MessageCommandResult, *admission.Result); err != nil {
				return err
			}
			continue
		}
		dispatcher.Start(admission.Job)
		if !reserved {
			continue
		}
		waiters.Add(1)
		go func(job *command.Job) {
			defer waiters.Done()
			defer func() { <-watchSlots }()
			defer func() { mutex.Lock(); delete(watching, job); mutex.Unlock() }()
			select {
			case <-ctx.Done():
				return
			case <-job.Done:
				_ = writer.SendContext(ctx, protocol.MessageCommandResult, job.Result)
			}
		}(admission.Job)
	}
}
