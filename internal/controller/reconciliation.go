package controller

import (
	"context"
	"sort"
	"time"

	"hlcp/internal/protocol"
	"hlcp/internal/session"
)

func (sessions *ActiveSessions) HandleCommandState(nodeID string, writer *session.Writer, state protocol.CommandStatePayload) bool {
	if state.Validate() != nil {
		return false
	}
	sessions.registry.mutex.RLock()
	defer sessions.registry.mutex.RUnlock()
	if current, exists := sessions.active[nodeID]; !exists || current.writer != writer || writer == nil {
		return false
	}
	if _, exists := sessions.tracker.Progress(nodeID, state.CommandID); !exists {
		return false
	}
	switch state.State {
	case "completed":
		return sessions.tracker.CompleteNode(nodeID, *state.Result)
	case "queued", "running":
		return sessions.tracker.AckNode(nodeID, protocol.CommandACKPayload{CommandID: state.CommandID, Status: "in_progress"})
	default:
		return sessions.tracker.FinishWait(nodeID, state.CommandID, "RESULT_TIMEOUT") == nil
	}
}

// One query worker belongs to each authenticated session, not each operation.
// Query-only recovery never calls send(COMMAND), even for unacknowledged IDs.
func (sessions *ActiveSessions) Reconcile(ctx context.Context, nodeID string, writer *session.Writer) {
	interval := sessions.policy.ACKTimeout
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	cursor := 0
	for {
		operations := sessions.tracker.Unresolved(nodeID)
		sort.Slice(operations, func(i, j int) bool { return operations[i].Command.CommandID < operations[j].Command.CommandID })
		if len(operations) > 0 {
			count := len(operations)
			if count > 128 {
				count = 128
			}
			for i := 0; i < count; i++ {
				progress := operations[(cursor+i)%len(operations)]
				if progress.AckStatus == "" && progress.WaitError == "" {
					continue
				}
				if ctx.Err() != nil {
					return
				}
				if err := writer.SendContext(ctx, protocol.MessageCommandQuery, progress.Command); err != nil {
					return
				}
			}
			cursor = (cursor + count) % len(operations)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
