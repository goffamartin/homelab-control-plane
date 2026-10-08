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
	pending map[string]chan protocol.CommandResultPayload
}

func NewCommandTracker() *CommandTracker {
	return &CommandTracker{pending: make(map[string]chan protocol.CommandResultPayload)}
}
func (tracker *CommandTracker) Track(commandID string) error {
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	if _, exists := tracker.pending[commandID]; exists {
		return ErrCommandAlreadyTracked
	}
	tracker.pending[commandID] = make(chan protocol.CommandResultPayload, 1)
	return nil
}
func (tracker *CommandTracker) Complete(result protocol.CommandResultPayload) bool {
	tracker.mutex.Lock()
	channel, exists := tracker.pending[result.CommandID]
	if exists {
		delete(tracker.pending, result.CommandID)
	}
	tracker.mutex.Unlock()
	if exists {
		channel <- result
		close(channel)
	}
	return exists
}
func (tracker *CommandTracker) Wait(ctx context.Context, commandID string) (protocol.CommandResultPayload, error) {
	tracker.mutex.Lock()
	channel, exists := tracker.pending[commandID]
	tracker.mutex.Unlock()
	if !exists {
		return protocol.CommandResultPayload{}, errors.New("hlcp controller: command is not tracked")
	}
	select {
	case result := <-channel:
		return result, nil
	case <-ctx.Done():
		return protocol.CommandResultPayload{}, ctx.Err()
	}
}
