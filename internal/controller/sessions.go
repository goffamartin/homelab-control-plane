package controller

import (
	"context"
	"errors"
	"hlcp/internal/protocol"
	"hlcp/internal/session"
	"sync"
	"time"
)

var ErrNodeNotConnected = errors.New("hlcp controller: node is not connected")

const commandRetryInterval = 5 * time.Second

type ActiveSessions struct {
	mutex   sync.RWMutex
	writers map[string]*session.Writer
	tracker *CommandTracker
}

func NewActiveSessions(tracker *CommandTracker) *ActiveSessions {
	return &ActiveSessions{writers: make(map[string]*session.Writer), tracker: tracker}
}
func (sessions *ActiveSessions) Register(nodeID string, writer *session.Writer) {
	sessions.mutex.Lock()
	sessions.writers[nodeID] = writer
	sessions.mutex.Unlock()
}
func (sessions *ActiveSessions) Remove(nodeID string, writer *session.Writer) {
	sessions.mutex.Lock()
	if sessions.writers[nodeID] == writer {
		delete(sessions.writers, nodeID)
	}
	sessions.mutex.Unlock()
}
func (sessions *ActiveSessions) Send(nodeID string, command protocol.CommandPayload) error {
	if err := sessions.tracker.Track(command.CommandID); err != nil {
		return err
	}
	if err := sessions.send(nodeID, command); err != nil {
		sessions.tracker.Cancel(command.CommandID)
		return err
	}
	return nil
}

func (sessions *ActiveSessions) send(nodeID string, command protocol.CommandPayload) error {
	sessions.mutex.RLock()
	writer := sessions.writers[nodeID]
	sessions.mutex.RUnlock()
	if writer == nil {
		return ErrNodeNotConnected
	}
	return session.SendCommand(writer, command)
}
func (sessions *ActiveSessions) HandleResult(result protocol.CommandResultPayload) bool {
	return sessions.tracker.Complete(result)
}

func (sessions *ActiveSessions) Execute(ctx context.Context, nodeID string, command protocol.CommandPayload) (protocol.CommandResultPayload, error) {
	if err := sessions.tracker.Track(command.CommandID); err != nil {
		return protocol.CommandResultPayload{}, err
	}
	defer sessions.tracker.Cancel(command.CommandID)
	for {
		// A failed write or a disconnected node is retried until the caller's
		// deadline. The agent recognizes the stable command ID after reconnect.
		_ = sessions.send(nodeID, command)
		waitContext, cancel := context.WithTimeout(ctx, commandRetryInterval)
		result, err := sessions.tracker.Wait(waitContext, command.CommandID)
		cancel()
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil {
			return protocol.CommandResultPayload{}, ctx.Err()
		}
	}
}
