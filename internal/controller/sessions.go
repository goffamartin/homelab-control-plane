package controller

import (
	"context"
	"errors"
	"hlcp/internal/localapi"
	"hlcp/internal/protocol"
	"hlcp/internal/session"
	"hlcp/internal/telemetry"
	"time"
)

var ErrNodeNotConnected = errors.New("hlcp controller: node is not connected")

type activeSession struct {
	writer    *session.Writer
	sessionID string
	cancel    context.CancelFunc
	admitted  func() bool
}

type ActiveSessions struct {
	// The registry lock protects both inventory and the current connection.
	registry *Registry
	active   map[string]activeSession
	tracker  *CommandTracker
	policy   RetryPolicy
	logs     *containerLogBroker
}

func NewActiveSessions(tracker *CommandTracker) *ActiveSessions {
	return &ActiveSessions{registry: NewRegistry(), active: make(map[string]activeSession), tracker: tracker, policy: DefaultRetryPolicy(), logs: newContainerLogBroker()}
}

func (sessions *ActiveSessions) Publish(registration session.Registration, synchronization session.Synchronization, at time.Time, writer *session.Writer, cancel context.CancelFunc) {
	sessions.PublishGuarded(registration, synchronization, at, writer, cancel, nil)
}

func (sessions *ActiveSessions) PublishGuarded(registration session.Registration, synchronization session.Synchronization, at time.Time, writer *session.Writer, cancel context.CancelFunc, admitted func() bool) {
	record := NodeRecord{NodeID: registration.NodeID, AgentVersion: registration.AgentVersion, Capabilities: cloneStrings(registration.Capabilities), SessionID: registration.SessionID, State: "online", LastActivity: at, Inventory: cloneInventory(synchronization.Inventory), Resources: cloneState(synchronization.State)}
	record.receivedAt = receivedObservations(record.Resources, at)
	sessions.registry.mutex.Lock()
	previous, exists := sessions.active[registration.NodeID]
	sessions.active[registration.NodeID] = activeSession{writer: writer, sessionID: registration.SessionID, cancel: cancel, admitted: admitted}
	sessions.registry.nodes[registration.NodeID] = record
	sessions.tracker.telemetry.SetGauge(telemetry.Sessions, len(sessions.active))
	sessions.registry.mutex.Unlock()
	if exists {
		sessions.logs.failWriter(previous.writer)
		sessions.tracker.telemetry.Record(telemetry.Event{Kind: telemetry.SessionReplaced, NodeID: registration.NodeID, SessionID: registration.SessionID})
		previous.cancel()
		_ = previous.writer.Close()
	}
}

func (sessions *ActiveSessions) Remove(nodeID string, writer *session.Writer) {
	sessions.registry.mutex.Lock()
	defer sessions.registry.mutex.Unlock()
	if current := sessions.active[nodeID]; current.writer == writer {
		sessions.logs.failWriter(writer)
		delete(sessions.active, nodeID)
		record := sessions.registry.nodes[nodeID]
		record.State = "offline"
		record.DisconnectedAt = time.Now()
		sessions.registry.nodes[nodeID] = record
		sessions.tracker.telemetry.SetGauge(telemetry.Sessions, len(sessions.active))
	}
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
	if !exists || (current.admitted != nil && !current.admitted()) {
		return ErrNodeNotConnected
	}
	return current.writer.SendContext(ctx, protocol.MessageCommand, command)
}

func (sessions *ActiveSessions) Disconnect(nodeID string) {
	sessions.registry.mutex.RLock()
	current, ok := sessions.active[nodeID]
	sessions.registry.mutex.RUnlock()
	if ok {
		current.cancel()
		_ = current.writer.Close()
		sessions.Remove(nodeID, current.writer)
	}
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

func (sessions *ActiveSessions) Execute(ctx context.Context, nodeID string, command protocol.CommandPayload) (result protocol.CommandResultPayload, returnedErr error) {
	if err := ctx.Err(); err != nil {
		return protocol.CommandResultPayload{}, err
	}
	if err := command.Validate(); err != nil {
		return protocol.CommandResultPayload{}, err
	}
	operationContext, stop := context.WithTimeout(ctx, sessions.policy.ResultTimeout)
	defer stop()
	deadline, _ := operationContext.Deadline()
	sessions.registry.mutex.RLock()
	admissionSessionID := sessions.active[nodeID].sessionID
	sessions.registry.mutex.RUnlock()
	var fresh bool
	err := localapi.GuardAdmission(ctx, nodeID, command.ResourceID, func() error {
		var admissionErr error
		fresh, admissionErr = sessions.tracker.admit(nodeID, command, deadline, telemetry.Actor(ctx), admissionSessionID)
		return admissionErr
	})
	if err != nil {
		return protocol.CommandResultPayload{}, err
	}
	if !fresh {
		return sessions.tracker.WaitNode(operationContext, nodeID, command.CommandID)
	}
	defer func() {
		if returnedErr != nil {
			if progress, exists := sessions.tracker.Progress(nodeID, command.CommandID); exists && progress.Attempts == 0 && progress.Result == nil {
				if sessions.tracker.CompleteNode(nodeID, protocol.CommandResultPayload{CommandID: command.CommandID, Code: "EXECUTION_NOT_STARTED", Message: "operation ended before any delivery attempt"}) {
					return
				}
			}
			reason := "RESULT_TIMEOUT"
			if errors.Is(returnedErr, ErrACKTimeout) {
				reason = "ACK_TIMEOUT"
			}
			if errors.Is(returnedErr, context.Canceled) {
				reason = "CANCELED"
			}
			if err := sessions.tracker.FinishWait(nodeID, command.CommandID, reason); err != nil {
				returnedErr = err
			}
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return protocol.CommandResultPayload{}, err
		}
		progress, _ := sessions.tracker.Progress(nodeID, command.CommandID)
		if progress.AckStatus != "" {
			// Once acknowledged, never resend execution just to obtain a result.
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
		if err := sessions.tracker.AttemptNode(nodeID, command.CommandID); err != nil {
			cancel()
			if errors.Is(err, errCommandStateChanged) {
				result, waitErr := sessions.tracker.WaitNode(operationContext, nodeID, command.CommandID)
				if errors.Is(waitErr, context.DeadlineExceeded) && ctx.Err() == nil {
					waitErr = ErrResultTimeout
				}
				return result, waitErr
			}
			return protocol.CommandResultPayload{}, err
		}
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
