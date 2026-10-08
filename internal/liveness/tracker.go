// Package liveness derives an agent's controller-visible state from valid
// inbound activity and a negotiated heartbeat policy.
package liveness

import (
	"errors"
	"fmt"
	"time"
)

var ErrInvalidPolicy = errors.New("hlcp liveness: invalid policy")

// NodeState is the controller's liveness view of one registered node.
type NodeState string

const (
	NodeOnline   NodeState = "online"
	NodeDegraded NodeState = "degraded"
	NodeOffline  NodeState = "offline"
)

// Policy defines how many missed heartbeat intervals move a node into the
// warning and failure states. OfflineAfter must be greater than DegradedAfter.
type Policy struct {
	HeartbeatInterval time.Duration
	DegradedAfter     uint8
	OfflineAfter      uint8
}

func (policy Policy) Validate() error {
	if policy.HeartbeatInterval <= 0 || policy.DegradedAfter == 0 || policy.OfflineAfter <= policy.DegradedAfter {
		return fmt.Errorf("%w: interval must be positive and 0 < degraded < offline", ErrInvalidPolicy)
	}
	return nil
}

// Snapshot describes a liveness evaluation. Changed is true only when the
// tracker changes state, which lets a controller emit one event per transition.
type Snapshot struct {
	State        NodeState
	Changed      bool
	LastActivity time.Time
}

// Tracker is deliberately independent from sockets and wall-clock access so
// its policy can be tested deterministically and reused by a controller loop.
type Tracker struct {
	policy       Policy
	state        NodeState
	lastActivity time.Time
}

func NewTracker(policy Policy, registeredAt time.Time) (*Tracker, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if registeredAt.IsZero() {
		return nil, fmt.Errorf("%w: registration time is required", ErrInvalidPolicy)
	}
	return &Tracker{policy: policy, state: NodeOnline, lastActivity: registeredAt}, nil
}

// RecordActivity records any valid agent-to-controller protocol message. A
// node returns to ONLINE immediately after activity resumes.
func (tracker *Tracker) RecordActivity(observedAt time.Time) Snapshot {
	if observedAt.Before(tracker.lastActivity) {
		observedAt = tracker.lastActivity
	}
	previousState := tracker.state
	tracker.lastActivity = observedAt
	tracker.state = NodeOnline
	return Snapshot{State: tracker.state, Changed: previousState != tracker.state, LastActivity: tracker.lastActivity}
}

// Evaluate advances the state only when the configured time thresholds have
// elapsed since the most recent valid inbound activity.
func (tracker *Tracker) Evaluate(now time.Time) Snapshot {
	if now.Before(tracker.lastActivity) {
		now = tracker.lastActivity
	}
	elapsed := now.Sub(tracker.lastActivity)
	nextState := NodeOnline
	if elapsed >= tracker.policy.HeartbeatInterval*time.Duration(tracker.policy.OfflineAfter) {
		nextState = NodeOffline
	} else if elapsed >= tracker.policy.HeartbeatInterval*time.Duration(tracker.policy.DegradedAfter) {
		nextState = NodeDegraded
	}
	changed := nextState != tracker.state
	tracker.state = nextState
	return Snapshot{State: tracker.state, Changed: changed, LastActivity: tracker.lastActivity}
}
