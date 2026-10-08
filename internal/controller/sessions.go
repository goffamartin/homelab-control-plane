package controller

import (
	"errors"
	"hlcp/internal/protocol"
	"hlcp/internal/session"
	"sync"
)

var ErrNodeNotConnected = errors.New("hlcp controller: node is not connected")

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
	sessions.mutex.RLock()
	writer := sessions.writers[nodeID]
	sessions.mutex.RUnlock()
	if writer == nil {
		return ErrNodeNotConnected
	}
	if err := sessions.tracker.Track(command.CommandID); err != nil {
		return err
	}
	if err := session.SendCommand(writer, command); err != nil {
		return err
	}
	return nil
}
func (sessions *ActiveSessions) HandleResult(result protocol.CommandResultPayload) bool {
	return sessions.tracker.Complete(result)
}
