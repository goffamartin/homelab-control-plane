package controller

import (
	"context"
	"errors"
	"hlcp/internal/protocol"
	"hlcp/internal/session"
	"time"
)

var ErrNodeNotConnected = errors.New("hlcp controller: node is not connected")

const commandRetryInterval = 5 * time.Second

type activeSession struct {
	writer    *session.Writer
	sessionID string
	cancel    context.CancelFunc
}

type ActiveSessions struct {
	// The registry lock protects both inventory and the current connection.
	registry *Registry
	active   map[string]activeSession
	tracker  *CommandTracker
}

func NewActiveSessions(tracker *CommandTracker) *ActiveSessions {
	return &ActiveSessions{registry: NewRegistry(), active: make(map[string]activeSession), tracker: tracker}
}

func (sessions *ActiveSessions) Publish(registration session.Registration, synchronization session.Synchronization, at time.Time, writer *session.Writer, cancel context.CancelFunc) {
	record := NodeRecord{NodeID: registration.NodeID, AgentVersion: registration.AgentVersion, Capabilities: cloneStrings(registration.Capabilities), SessionID: registration.SessionID, State: "online", LastActivity: at, Inventory: cloneInventory(synchronization.Inventory), Resources: cloneState(synchronization.State)}
	sessions.registry.mutex.Lock()
	previous, exists := sessions.active[registration.NodeID]
	sessions.active[registration.NodeID] = activeSession{writer: writer, sessionID: registration.SessionID, cancel: cancel}
	sessions.registry.nodes[registration.NodeID] = record
	sessions.registry.mutex.Unlock()
	if exists {
		previous.cancel()
		_ = previous.writer.Close()
	}
}

func (sessions *ActiveSessions) Remove(nodeID string, writer *session.Writer) {
	sessions.registry.mutex.Lock()
	defer sessions.registry.mutex.Unlock()
	if current := sessions.active[nodeID]; current.writer == writer {
		delete(sessions.active, nodeID)
		record := sessions.registry.nodes[nodeID]
		record.State = "offline"
		record.DisconnectedAt = time.Now()
		sessions.registry.nodes[nodeID] = record
	}
}

func (sessions *ActiveSessions) Send(nodeID string, command protocol.CommandPayload) error {
	if err := sessions.tracker.TrackNode(nodeID, command.CommandID); err != nil {
		return err
	}
	if err := sessions.send(context.Background(), nodeID, command); err != nil {
		sessions.tracker.Cancel(command.CommandID)
		return err
	}
	return nil
}

func (sessions *ActiveSessions) send(ctx context.Context, nodeID string, command protocol.CommandPayload) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := command.Validate(); err != nil {
		return err
	}
	sessions.registry.mutex.RLock()
	current, exists := sessions.active[nodeID]
	sessions.registry.mutex.RUnlock()
	if !exists {
		return ErrNodeNotConnected
	}
	return current.writer.SendContext(ctx, protocol.MessageCommand, command)
}

func (sessions *ActiveSessions) HandleAck(nodeID string, writer *session.Writer, ack protocol.CommandACKPayload) bool {
	sessions.registry.mutex.RLock()
	defer sessions.registry.mutex.RUnlock()
	if current := sessions.active[nodeID]; current.writer != writer {
		return false
	}
	return sessions.tracker.PendingNode(nodeID, ack.CommandID)
}

func (sessions *ActiveSessions) HandleResult(nodeID string, writer *session.Writer, result protocol.CommandResultPayload) bool {
	sessions.registry.mutex.RLock()
	defer sessions.registry.mutex.RUnlock()
	if current := sessions.active[nodeID]; current.writer != writer {
		return false
	}
	return sessions.tracker.CompleteNode(nodeID, result)
}

func (sessions *ActiveSessions) Execute(ctx context.Context, nodeID string, command protocol.CommandPayload) (protocol.CommandResultPayload, error) {
	if err := ctx.Err(); err != nil {
		return protocol.CommandResultPayload{}, err
	}
	if err := command.Validate(); err != nil {
		return protocol.CommandResultPayload{}, err
	}
	if err := sessions.tracker.TrackNode(nodeID, command.CommandID); err != nil {
		return protocol.CommandResultPayload{}, err
	}
	defer sessions.tracker.Cancel(command.CommandID)
	for {
		if err := ctx.Err(); err != nil {
			return protocol.CommandResultPayload{}, err
		}
		_ = sessions.send(ctx, nodeID, command)
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
