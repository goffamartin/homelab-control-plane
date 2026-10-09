package session

import (
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"hlcp/internal/liveness"
	"hlcp/internal/protocol"
)

var (
	ErrNodeOffline              = errors.New("hlcp session: node became offline")
	ErrUnexpectedSessionMessage = errors.New("hlcp session: unexpected message in heartbeat loop")
	ErrNonIncreasingHeartbeat   = errors.New("hlcp session: heartbeat sequence did not increase")
)

// StateChange is emitted only when liveness changes, allowing callers to
// persist state and publish events without polling or duplicate notifications.
type StateChange struct {
	State        liveness.NodeState
	LastActivity time.Time
}

// RunHeartbeatLoop receives heartbeats until the connection closes or the
// node crosses the offline threshold. This legacy heartbeat-only helper is
// retained for transport tests; production uses StartInbound/RunInboundLoop.
// connection is expected to be the already
// authenticated and synchronized controller-side TLS connection.
func RunHeartbeatLoop(connection net.Conn, tracker *liveness.Tracker, onStateChange func(StateChange)) error {
	if tracker == nil {
		return errors.New("hlcp session: liveness tracker is required")
	}
	var lastSequence uint64

	for {
		if err := connection.SetReadDeadline(tracker.NextEvaluationAt()); err != nil {
			if errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
				return io.EOF
			}
			return fmt.Errorf("set heartbeat read deadline: %w", err)
		}
		frame, err := protocol.Decode(connection)
		if err != nil {
			if isTimeout(err) {
				snapshot := tracker.Evaluate(time.Now())
				if snapshot.Changed && onStateChange != nil {
					onStateChange(StateChange{State: snapshot.State, LastActivity: snapshot.LastActivity})
				}
				if snapshot.State == liveness.NodeOffline {
					return ErrNodeOffline
				}
				continue
			}
			if errors.Is(err, io.EOF) {
				return io.EOF
			}
			return fmt.Errorf("read heartbeat frame: %w", err)
		}
		if frame.Header.Type != protocol.MessageHeartbeat {
			return fmt.Errorf("%w: %d", ErrUnexpectedSessionMessage, frame.Header.Type)
		}

		var heartbeat protocol.HeartbeatPayload
		if err := protocol.DecodePayload(frame.Payload, &heartbeat); err != nil {
			return fmt.Errorf("decode heartbeat: %w", err)
		}
		if err := heartbeat.Validate(); err != nil {
			return fmt.Errorf("validate heartbeat: %w", err)
		}
		if heartbeat.Sequence <= lastSequence {
			return fmt.Errorf("%w: previous=%d received=%d", ErrNonIncreasingHeartbeat, lastSequence, heartbeat.Sequence)
		}
		lastSequence = heartbeat.Sequence

		snapshot := tracker.RecordActivity(time.Now())
		if snapshot.Changed && onStateChange != nil {
			onStateChange(StateChange{State: snapshot.State, LastActivity: snapshot.LastActivity})
		}
	}
}

func isTimeout(err error) bool {
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}
