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
	NodeID       string
	AgentVersion string
	Capabilities []string
	SessionID    string
	State        liveness.NodeState
	LastActivity time.Time
	Inventory    protocol.InventorySnapshot
	Resources    protocol.StateSnapshot
}

// Registry protects the latest session per node. Stale connections cannot
// overwrite state established by a newer reconnect.
type Registry struct {
	mutex sync.RWMutex
	nodes map[string]NodeRecord
}

func NewRegistry() *Registry { return &Registry{nodes: make(map[string]NodeRecord)} }

func (registry *Registry) Register(registration session.Registration, synchronization session.Synchronization, registeredAt time.Time) NodeRecord {
	record := NodeRecord{NodeID: registration.NodeID, AgentVersion: registration.AgentVersion, Capabilities: cloneStrings(registration.Capabilities), SessionID: registration.SessionID, State: liveness.NodeOnline, LastActivity: registeredAt, Inventory: cloneInventory(synchronization.Inventory), Resources: cloneState(synchronization.State)}
	registry.mutex.Lock()
	registry.nodes[record.NodeID] = record
	registry.mutex.Unlock()
	return cloneRecord(record)
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
	registry.mutex.RUnlock()
	return cloneRecord(record), exists
}

func (registry *Registry) List() []NodeRecord {
	registry.mutex.RLock()
	records := make([]NodeRecord, 0, len(registry.nodes))
	for _, record := range registry.nodes {
		records = append(records, cloneRecord(record))
	}
	registry.mutex.RUnlock()
	sort.Slice(records, func(left, right int) bool { return records[left].NodeID < records[right].NodeID })
	return records
}

func cloneRecord(record NodeRecord) NodeRecord {
	record.Capabilities = cloneStrings(record.Capabilities)
	record.Inventory = cloneInventory(record.Inventory)
	record.Resources = cloneState(record.Resources)
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
