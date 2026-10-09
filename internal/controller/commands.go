package controller

import (
	"context"
	"errors"
	"hlcp/internal/protocol"
	"sync"
)

var ErrCommandAlreadyTracked = errors.New("hlcp controller: command already tracked")

type CommandTracker struct {
	mutex   sync.Mutex
	pending map[string]*commandWaiter
}

type commandWaiter struct {
	nodeID    string
	result    chan protocol.CommandResultPayload
	completed bool
}

func NewCommandTracker() *CommandTracker {
	return &CommandTracker{pending: make(map[string]*commandWaiter)}
}
func (tracker *CommandTracker) Track(commandID string) error {
	return tracker.TrackNode("", commandID)
}

func (tracker *CommandTracker) TrackNode(nodeID, commandID string) error {
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	if _, exists := tracker.pending[commandID]; exists {
		return ErrCommandAlreadyTracked
	}
	tracker.pending[commandID] = &commandWaiter{nodeID: nodeID, result: make(chan protocol.CommandResultPayload, 1)}
	return nil
}
func (tracker *CommandTracker) Complete(result protocol.CommandResultPayload) bool {
	return tracker.CompleteNode("", result)
}

func (tracker *CommandTracker) PendingNode(nodeID, commandID string) bool {
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	waiter, exists := tracker.pending[commandID]
	return exists && waiter.nodeID == nodeID && !waiter.completed
}

func (tracker *CommandTracker) CompleteNode(nodeID string, result protocol.CommandResultPayload) bool {
	tracker.mutex.Lock()
	waiter, exists := tracker.pending[result.CommandID]
	if !exists || waiter.completed || waiter.nodeID != nodeID {
		tracker.mutex.Unlock()
		return false
	}
	waiter.completed = true
	tracker.mutex.Unlock()
	waiter.result <- result
	return true
}

func (tracker *CommandTracker) Cancel(commandID string) {
	tracker.mutex.Lock()
	delete(tracker.pending, commandID)
	tracker.mutex.Unlock()
}
func (tracker *CommandTracker) Wait(ctx context.Context, commandID string) (protocol.CommandResultPayload, error) {
	tracker.mutex.Lock()
	waiter, exists := tracker.pending[commandID]
	tracker.mutex.Unlock()
	if !exists {
		return protocol.CommandResultPayload{}, errors.New("hlcp controller: command is not tracked")
	}
	select {
	case result := <-waiter.result:
		tracker.mutex.Lock()
		if tracker.pending[commandID] == waiter {
			delete(tracker.pending, commandID)
		}
		tracker.mutex.Unlock()
		return result, nil
	case <-ctx.Done():
		return protocol.CommandResultPayload{}, ctx.Err()
	}
}
