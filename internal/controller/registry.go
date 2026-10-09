package controller

import (
	"sort"
	"sync"
	"time"

	"hlcp/internal/liveness"
	"hlcp/internal/protocol"
	"hlcp/internal/session"
)

type NodeRecord struct {
	NodeID         string                     `json:"node_id"`
	AgentVersion   string                     `json:"agent_version"`
	Capabilities   []string                   `json:"capabilities"`
	SessionID      string                     `json:"session_id"`
	State          liveness.NodeState         `json:"state"`
	LastActivity   time.Time                  `json:"last_activity"`
	DisconnectedAt time.Time                  `json:"disconnected_at,omitempty"`
	Inventory      protocol.InventorySnapshot `json:"inventory"`
	Resources      protocol.StateSnapshot     `json:"resources"`
	StateRevision  uint64                     `json:"state_revision"`
	receivedAt     map[string]time.Time
}

// Registry protects the latest session per node. Stale connections cannot
// overwrite state established by a newer reconnect.
type Registry struct {
	mutex       sync.RWMutex
	nodes       map[string]NodeRecord
	stateMaxAge time.Duration
	now         func() time.Time
}

func NewRegistry() *Registry {
	return &Registry{nodes: make(map[string]NodeRecord), stateMaxAge: time.Minute, now: time.Now}
}

func receivedObservations(snapshot protocol.StateSnapshot, at time.Time) map[string]time.Time {
	result := make(map[string]time.Time, len(snapshot.Resources))
	for _, state := range snapshot.Resources {
		if !state.ObservedAt.IsZero() {
			result[state.ResourceID] = at
		}
	}
	return result
}

func (registry *Registry) Register(registration session.Registration, synchronization session.Synchronization, registeredAt time.Time) NodeRecord {
	record := NodeRecord{NodeID: registration.NodeID, AgentVersion: registration.AgentVersion, Capabilities: cloneStrings(registration.Capabilities), SessionID: registration.SessionID, State: liveness.NodeOnline, LastActivity: registeredAt, Inventory: cloneInventory(synchronization.Inventory), Resources: cloneState(synchronization.State)}
	record.receivedAt = receivedObservations(record.Resources, registeredAt)
	registry.mutex.Lock()
	registry.nodes[record.NodeID] = record
	copy := registry.cloneRecord(record)
	registry.mutex.Unlock()
	return copy
}

func (registry *Registry) UpdateLiveness(nodeID, sessionID string, state liveness.NodeState, lastActivity time.Time) bool {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	record, exists := registry.nodes[nodeID]
	if !exists || record.SessionID != sessionID {
		return false
	}
	record.State, record.LastActivity = state, lastActivity
	registry.nodes[nodeID] = record
	return true
}

func (registry *Registry) Get(nodeID string) (NodeRecord, bool) {
	registry.mutex.RLock()
	record, exists := registry.nodes[nodeID]
	copy := registry.cloneRecord(record)
	registry.mutex.RUnlock()
	return copy, exists
}

func (registry *Registry) List() []NodeRecord {
	registry.mutex.RLock()
	records := make([]NodeRecord, 0, len(registry.nodes))
	for _, record := range registry.nodes {
		records = append(records, registry.cloneRecord(record))
	}
	registry.mutex.RUnlock()
	sort.Slice(records, func(left, right int) bool { return records[left].NodeID < records[right].NodeID })
	return records
}

func (registry *Registry) Disconnect(nodeID, sessionID string, at time.Time) bool {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	record, exists := registry.nodes[nodeID]
	if !exists || record.SessionID != sessionID {
		return false
	}
	record.State = liveness.NodeOffline
	record.DisconnectedAt = at
	registry.nodes[nodeID] = record
	return true
}

func (registry *Registry) cloneRecord(record NodeRecord) NodeRecord {
	record.Capabilities = cloneStrings(record.Capabilities)
	record.Inventory = cloneInventory(record.Inventory)
	record.Resources = cloneState(record.Resources)
	for index := range record.Resources.Resources {
		state := &record.Resources.Resources[index]
		received := record.receivedAt[state.ResourceID]
		state.Stale = record.State != liveness.NodeOnline || state.ObservedAt.IsZero() || received.IsZero() || !registry.now().Before(received.Add(registry.stateMaxAge))
	}
	record.receivedAt = nil
	return record
}
func cloneInventory(inventory protocol.InventorySnapshot) protocol.InventorySnapshot {
	inventory.Resources = append([]protocol.ResourceDescriptor(nil), inventory.Resources...)
	for index := range inventory.Resources {
		inventory.Resources[index].Actions = cloneStrings(inventory.Resources[index].Actions)
	}
	return inventory
}
func cloneState(state protocol.StateSnapshot) protocol.StateSnapshot {
	state.Resources = append([]protocol.ResourceState(nil), state.Resources...)
	return state
}
func cloneStrings(values []string) []string { return append([]string(nil), values...) }
