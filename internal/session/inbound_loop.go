package session

import (
	"context"
	"hlcp/internal/liveness"
	"hlcp/internal/protocol"
	"time"
)

// InboundHandlers handles optional command queries and live resource updates.
type InboundHandlers struct {
	CommandState func(protocol.CommandStatePayload) bool
	StateUpdate  func(protocol.StateUpdate) bool
}

// RunInboundLoop records only correlated activity belonging to this session.
// onState publishes last_seen for every valid event.
func RunInboundLoop(ctx context.Context, inbound *Inbound, tracker *liveness.Tracker, onState func(StateChange), onAck func(protocol.CommandACKPayload) bool, onResult func(protocol.CommandResultPayload) bool, handlers ...InboundHandlers) error {
	timer := time.NewTimer(time.Until(tracker.NextEvaluationAt()))
	defer timer.Stop()
	publish := func(snapshot liveness.Snapshot) {
		if onState != nil {
			onState(StateChange{State: snapshot.State, LastActivity: snapshot.LastActivity})
		}
	}
	reset := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(time.Until(tracker.NextEvaluationAt()))
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-inbound.Events:
			if !ok {
				return <-inbound.Errors
			}
			valid := event.Heartbeat != nil
			if event.Ack != nil && onAck != nil {
				valid = onAck(*event.Ack)
			}
			if event.Result != nil && onResult != nil {
				valid = onResult(*event.Result)
			}
			if event.CommandState != nil && len(handlers) > 0 && handlers[0].CommandState != nil {
				valid = handlers[0].CommandState(*event.CommandState)
			}
			if event.StateUpdate != nil && len(handlers) > 0 && handlers[0].StateUpdate != nil {
				valid = handlers[0].StateUpdate(*event.StateUpdate)
			}
			if valid {
				publish(tracker.RecordActivity(event.ReceivedAt))
				reset()
			}
		case <-timer.C:
			snapshot := tracker.Evaluate(time.Now())
			if snapshot.Changed {
				publish(snapshot)
			}
			if snapshot.State == liveness.NodeOffline {
				return ErrNodeOffline
			}
			reset()
		}
	}
}
