package controller

import (
	"hlcp/internal/protocol"
	"hlcp/internal/session"
	"time"
)

func (sessions *ActiveSessions) HandleStateUpdate(nodeID string, writer *session.Writer, update protocol.StateUpdate) bool {
	if update.Validate() != nil {
		return false
	}
	registry := sessions.registry
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	current, exists := sessions.active[nodeID]
	if !exists || current.writer != writer || writer == nil || current.sessionID != update.SessionID {
		return false
	}
	record := registry.nodes[nodeID]
	if record.SessionID != update.SessionID || record.Inventory.Revision != update.InventoryRevision || update.Revision <= record.StateRevision {
		return false
	}
	for index, state := range record.Resources.Resources {
		if state.ResourceID != update.Resource.ResourceID {
			continue
		}
		update.Resource.Stale = false
		record.Resources.Resources[index] = update.Resource
		record.StateRevision = update.Revision
		if record.receivedAt == nil {
			record.receivedAt = make(map[string]time.Time)
		}
		record.receivedAt[state.ResourceID] = registry.now()
		registry.nodes[nodeID] = record
		return true
	}
	return false
}
