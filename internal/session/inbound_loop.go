package session

import (
	"hlcp/internal/liveness"
	"hlcp/internal/protocol"
	"time"
)

// RunInboundLoop derives liveness and forwards command results from the sole reader.
func RunInboundLoop(inbound *Inbound, tracker *liveness.Tracker, onState func(StateChange), onResult func(protocol.CommandResultPayload)) error {
	timer := time.NewTimer(time.Until(tracker.NextEvaluationAt()))
	defer timer.Stop()
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
		case heartbeat, ok := <-inbound.Heartbeats:
			if !ok {
				return <-inbound.Errors
			}
			_ = heartbeat
			snapshot := tracker.RecordActivity(time.Now())
			if snapshot.Changed && onState != nil {
				onState(StateChange{State: snapshot.State, LastActivity: snapshot.LastActivity})
			}
			reset()
		case _, ok := <-inbound.Acks:
			if !ok {
				return <-inbound.Errors
			}
		case result, ok := <-inbound.Results:
			if !ok {
				return <-inbound.Errors
			}
			if onResult != nil {
				onResult(result)
			}
		case err := <-inbound.Errors:
			return err
		case <-timer.C:
			snapshot := tracker.Evaluate(time.Now())
			if snapshot.Changed && onState != nil {
				onState(StateChange{State: snapshot.State, LastActivity: snapshot.LastActivity})
			}
			if snapshot.State == liveness.NodeOffline {
				return ErrNodeOffline
			}
			reset()
		}
	}
}
