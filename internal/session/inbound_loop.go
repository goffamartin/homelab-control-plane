package session

import (
	"context"
	"hlcp/internal/liveness"
	"hlcp/internal/protocol"
	"time"
)

// RunInboundLoop records only activity belonging to this session and a known
// command. onState publishes last_seen for every valid event.
func RunInboundLoop(ctx context.Context, inbound *Inbound, tracker *liveness.Tracker, onState func(StateChange), onAck func(protocol.CommandACKPayload) bool, onResult func(protocol.CommandResultPayload) bool, onCommandState ...func(protocol.CommandStatePayload) bool) error {
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
			if event.CommandState != nil && len(onCommandState) > 0 && onCommandState[0] != nil {
				valid = onCommandState[0](*event.CommandState)
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
