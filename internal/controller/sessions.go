package controller

import (
	"context"
	"errors"
	"hlcp/internal/protocol"
	"hlcp/internal/session"
	"time"
)

var ErrNodeNotConnected = errors.New("hlcp controller: node is not connected")

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
	policy   RetryPolicy
}

func NewActiveSessions(tracker *CommandTracker) *ActiveSessions {
	return &ActiveSessions{registry: NewRegistry(), active: make(map[string]activeSession), tracker: tracker, policy: DefaultRetryPolicy()}
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
	if err := command.Validate(); err != nil {
		return err
	}
	if err := sessions.tracker.TrackNode(nodeID, command.CommandID); err != nil {
		return err
	}
	if err := sessions.send(context.Background(), nodeID, command); err != nil {
		sessions.tracker.CancelNode(nodeID, command.CommandID)
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
	if current, exists := sessions.active[nodeID]; !exists || current.writer != writer || writer == nil {
		return false
	}
	return sessions.tracker.AckNode(nodeID, ack)
}

func (sessions *ActiveSessions) HandleResult(nodeID string, writer *session.Writer, result protocol.CommandResultPayload) bool {
	sessions.registry.mutex.RLock()
	defer sessions.registry.mutex.RUnlock()
	if current, exists := sessions.active[nodeID]; !exists || current.writer != writer || writer == nil {
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
	operationContext, stop := context.WithTimeout(ctx, sessions.policy.ResultTimeout)
	defer stop()
	deadline, _ := operationContext.Deadline()
	if err := sessions.tracker.TrackCommand(nodeID, command, deadline); err != nil {
		return protocol.CommandResultPayload{}, err
	}
	defer sessions.tracker.CancelNode(nodeID, command.CommandID)
	for {
		if err := ctx.Err(); err != nil {
			return protocol.CommandResultPayload{}, err
		}
		progress, _ := sessions.tracker.Progress(nodeID, command.CommandID)
		if progress.AckStatus != "" {
			// Once acknowledged, never resend execution just to obtain a result.
			// Read-only reconnect reconciliation is the next F6 ledger slice.
			result, err := sessions.tracker.WaitNode(operationContext, nodeID, command.CommandID)
			if err == nil {
				return result, nil
			}
			if ctx.Err() != nil {
				return protocol.CommandResultPayload{}, ctx.Err()
			}
			return protocol.CommandResultPayload{}, ErrResultTimeout
		}
		if progress.Attempts >= sessions.policy.MaxAttempts || operationContext.Err() != nil {
			return protocol.CommandResultPayload{}, ErrACKTimeout
		}
		attemptContext, cancel := context.WithTimeout(operationContext, sessions.policy.ACKTimeout)
		sessions.tracker.AttemptNode(nodeID, command.CommandID)
		_ = sessions.send(attemptContext, nodeID, command)
		result, err := sessions.tracker.WaitNode(attemptContext, nodeID, command.CommandID)
		cancel()
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil {
			return protocol.CommandResultPayload{}, ctx.Err()
		}
	}
}
