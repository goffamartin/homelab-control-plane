package controller

import (
	"hlcp/internal/liveness"
	"hlcp/internal/protocol"
	"hlcp/internal/session"
	"testing"
	"time"
)

func TestRegistryIgnoresStaleSessionState(t *testing.T) {
	registry, now := NewRegistry(), time.Now()
	sync := session.Synchronization{Inventory: protocol.InventorySnapshot{Revision: 1}}
	registry.Register(session.Registration{NodeID: "debian-01", SessionID: "old"}, sync, now)
	registry.Register(session.Registration{NodeID: "debian-01", SessionID: "new"}, sync, now.Add(time.Second))
	if registry.UpdateLiveness("debian-01", "old", liveness.NodeOffline, now) {
		t.Fatal("stale session updated registry")
	}
	record, found := registry.Get("debian-01")
	if !found || record.SessionID != "new" || record.State != liveness.NodeOnline {
		t.Fatalf("record = %#v, found = %t", record, found)
	}
}
