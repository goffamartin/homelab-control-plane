package controller

import (
	"context"
	"errors"
	"hlcp/internal/protocol"
	"sync"
	"time"
)

var (
	ErrCommandAlreadyTracked = errors.New("hlcp controller: command already tracked")
	ErrCommandCapacity       = errors.New("hlcp controller: active command capacity is full")
)

const maxActiveCommands = 128

type commandKey struct{ nodeID, commandID string }

// CommandProgress is a snapshot, not a durable operation record yet.
type CommandProgress struct {
	NodeID        string
	Command       protocol.CommandPayload
	AdmittedAt    time.Time
	Deadline      time.Time
	AckStatus     string
	Attempts      int
	LastAttemptAt time.Time
}

type CommandTracker struct {
	mutex   sync.Mutex
	pending map[commandKey]*commandWaiter
}

type commandWaiter struct {
	progress CommandProgress
	result   *protocol.CommandResultPayload
	changed  chan struct{}
}

func NewCommandTracker() *CommandTracker {
	return &CommandTracker{pending: make(map[commandKey]*commandWaiter)}
}
func (tracker *CommandTracker) Track(commandID string) error {
	return tracker.TrackNode("", commandID)
}
func (tracker *CommandTracker) TrackNode(nodeID, commandID string) error {
	return tracker.track(nodeID, protocol.CommandPayload{CommandID: commandID}, time.Time{})
}
func (tracker *CommandTracker) TrackCommand(nodeID string, command protocol.CommandPayload, deadline time.Time) error {
	if err := command.Validate(); err != nil {
		return err
	}
	return tracker.track(nodeID, command, deadline)
}
func (tracker *CommandTracker) track(nodeID string, command protocol.CommandPayload, deadline time.Time) error {
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	key := commandKey{nodeID, command.CommandID}
	if _, exists := tracker.pending[key]; exists {
		return ErrCommandAlreadyTracked
	}
	if len(tracker.pending) >= maxActiveCommands {
		return ErrCommandCapacity
	}
	tracker.pending[key] = &commandWaiter{progress: CommandProgress{NodeID: nodeID, Command: command, AdmittedAt: time.Now(), Deadline: deadline}, changed: make(chan struct{})}
	return nil
}
func notify(waiter *commandWaiter) {
	close(waiter.changed)
	waiter.changed = make(chan struct{})
}
func (tracker *CommandTracker) Progress(nodeID, commandID string) (CommandProgress, bool) {
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	waiter, exists := tracker.pending[commandKey{nodeID, commandID}]
	if !exists {
		return CommandProgress{}, false
	}
	return waiter.progress, true
}
func (tracker *CommandTracker) AttemptNode(nodeID, commandID string) {
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	if waiter := tracker.pending[commandKey{nodeID, commandID}]; waiter != nil {
		waiter.progress.Attempts++
		waiter.progress.LastAttemptAt = time.Now()
	}
}
func (tracker *CommandTracker) AckNode(nodeID string, ack protocol.CommandACKPayload) bool {
	if err := ack.Validate(); err != nil {
		return false
	}
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	waiter := tracker.pending[commandKey{nodeID, ack.CommandID}]
	if waiter == nil || waiter.result != nil {
		return false
	}
	// ACK is evidence of admission, not completion. Never regress after a
	// terminal ACK or downgrade in_progress to an earlier accepted ACK.
	switch waiter.progress.AckStatus {
	case "rejected", "completed":
		return waiter.progress.AckStatus == ack.Status
	case "in_progress":
		if ack.Status == "accepted" {
			return true
		}
	}
	waiter.progress.AckStatus = ack.Status
	notify(waiter)
	return true
}
func (tracker *CommandTracker) Complete(result protocol.CommandResultPayload) bool {
	return tracker.CompleteNode("", result)
}
func (tracker *CommandTracker) PendingNode(nodeID, commandID string) bool {
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	waiter := tracker.pending[commandKey{nodeID, commandID}]
	return waiter != nil && waiter.result == nil
}
func (tracker *CommandTracker) CompleteNode(nodeID string, result protocol.CommandResultPayload) bool {
	if err := result.Validate(); err != nil {
		return false
	}
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	waiter := tracker.pending[commandKey{nodeID, result.CommandID}]
	if waiter == nil || waiter.result != nil {
		return false
	}
	copy := result
	waiter.result = &copy
	notify(waiter)
	return true
}
func (tracker *CommandTracker) Cancel(commandID string) { tracker.CancelNode("", commandID) }
func (tracker *CommandTracker) CancelNode(nodeID, commandID string) {
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	key := commandKey{nodeID, commandID}
	if waiter := tracker.pending[key]; waiter != nil {
		delete(tracker.pending, key)
		notify(waiter)
	}
}
func (tracker *CommandTracker) Wait(ctx context.Context, commandID string) (protocol.CommandResultPayload, error) {
	return tracker.WaitNode(ctx, "", commandID)
}
func (tracker *CommandTracker) WaitNode(ctx context.Context, nodeID, commandID string) (protocol.CommandResultPayload, error) {
	tracker.mutex.Lock()
	original := tracker.pending[commandKey{nodeID, commandID}]
	tracker.mutex.Unlock()
	for {
		tracker.mutex.Lock()
		waiter := tracker.pending[commandKey{nodeID, commandID}]
		if waiter == nil || waiter != original {
			tracker.mutex.Unlock()
			return protocol.CommandResultPayload{}, errors.New("hlcp controller: command is not tracked")
		}
		if waiter.result != nil {
			result := *waiter.result
			tracker.mutex.Unlock()
			return result, nil
		}
		changed := waiter.changed
		tracker.mutex.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return protocol.CommandResultPayload{}, ctx.Err()
		}
	}
}
