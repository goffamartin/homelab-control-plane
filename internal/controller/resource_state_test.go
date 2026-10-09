package controller

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"hlcp/internal/liveness"
	"hlcp/internal/protocol"
	"hlcp/internal/session"
)

func TestStateUpdateSessionRevisionAndFreshness(t *testing.T) {
	sessions := NewActiveSessions(NewCommandTracker())
	now := time.Now()
	sessions.registry.now = func() time.Time { return now }
	connection, peer := net.Pipe()
	defer connection.Close()
	defer peer.Close()
	writer := session.NewWriter(connection, 1)
	sync := session.Synchronization{Inventory: protocol.InventorySnapshot{Revision: 1, Resources: []protocol.ResourceDescriptor{{ID: "catalog", Type: "container", Actions: []string{"status"}}}}, State: protocol.StateSnapshot{InventoryRevision: 1, Resources: []protocol.ResourceState{{ResourceID: "catalog", State: "unknown", Code: "NOT_OBSERVED"}}}}
	sessions.Publish(session.Registration{NodeID: "n", SessionID: "s"}, sync, now, writer, func() {})
	record, _ := sessions.registry.Get("n")
	if !record.Resources.Resources[0].Stale {
		t.Fatal("unobserved initial state marked fresh")
	}
	update := protocol.StateUpdate{SessionID: "s", InventoryRevision: 1, Revision: 1, Resource: protocol.ResourceState{ResourceID: "catalog", State: "running", Code: "OK", ObservedAt: now.Add(24 * time.Hour)}}
	for _, change := range []func(*protocol.StateUpdate){func(u *protocol.StateUpdate) { u.SessionID = "old" }, func(u *protocol.StateUpdate) { u.InventoryRevision = 2 }, func(u *protocol.StateUpdate) { u.Resource.ResourceID = "unknown" }} {
		invalid := update
		change(&invalid)
		if sessions.HandleStateUpdate("n", writer, invalid) {
			t.Fatal("invalid update accepted")
		}
	}
	if !sessions.HandleStateUpdate("n", writer, update) {
		t.Fatal("current update rejected")
	}
	if sessions.HandleStateUpdate("n", writer, update) {
		t.Fatal("repeated revision accepted")
	}
	record, _ = sessions.registry.Get("n")
	if record.Resources.Resources[0].Stale {
		t.Fatal("new observation stale")
	}
	now = now.Add(61 * time.Second)
	record, _ = sessions.registry.Get("n")
	if !record.Resources.Resources[0].Stale {
		t.Fatal("future agent timestamp prevented freshness expiration")
	}
	update.Revision = 2
	update.Resource.State = "stopped"
	if !sessions.HandleStateUpdate("n", writer, update) {
		t.Fatal("external stop rejected")
	}
	record, _ = sessions.registry.Get("n")
	if record.Resources.Resources[0].State != "stopped" || record.Resources.Resources[0].Stale {
		t.Fatalf("record=%+v", record)
	}
	sessions.Remove("n", writer)
	record, _ = sessions.registry.Get("n")
	if !record.Resources.Resources[0].Stale {
		t.Fatal("offline resource marked fresh")
	}
	sessions.Publish(session.Registration{NodeID: "n", SessionID: "new"}, sync, now, writer, func() {})
	if sessions.HandleStateUpdate("n", writer, update) {
		t.Fatal("old session updated reconnected inventory")
	}
	record, _ = sessions.registry.Get("n")
	if record.Resources.Resources[0].State != "unknown" || !record.Resources.Resources[0].Stale || record.StateRevision != 0 {
		t.Fatal("reconnect restored old state")
	}
}

func TestStateUpdateWireToAPI(t *testing.T) {
	sessions := NewActiveSessions(NewCommandTracker())
	controller, agent := net.Pipe()
	defer controller.Close()
	defer agent.Close()
	writer := session.NewWriter(controller, 1)
	sync := session.Synchronization{Inventory: protocol.InventorySnapshot{Revision: 1, Resources: []protocol.ResourceDescriptor{{ID: "catalog", Type: "container", Actions: []string{"status"}}}}, State: protocol.StateSnapshot{InventoryRevision: 1, Resources: []protocol.ResourceState{{ResourceID: "catalog", State: "unknown", Code: "NOT_OBSERVED"}}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sessions.Publish(session.Registration{NodeID: "n", SessionID: "s"}, sync, time.Now(), writer, cancel)
	inbound := session.StartInbound(ctx, controller)
	tracker, _ := liveness.NewTracker(liveness.Policy{HeartbeatInterval: time.Second, DegradedAfter: 2, OfflineAfter: 3}, time.Now())
	done := make(chan error, 1)
	accepted := make(chan struct{}, 1)
	go func() {
		done <- session.RunInboundLoop(ctx, inbound, tracker, nil, nil, nil, session.InboundHandlers{StateUpdate: func(update protocol.StateUpdate) bool {
			ok := sessions.HandleStateUpdate("n", writer, update)
			if ok {
				accepted <- struct{}{}
			}
			return ok
		}})
	}()
	defer func() { cancel(); controller.Close(); agent.Close(); <-done; <-inbound.Done }()
	update := protocol.StateUpdate{SessionID: "s", InventoryRevision: 1, Revision: 1, Resource: protocol.ResourceState{ResourceID: "catalog", State: "stopped", ObservedAt: time.Now().UTC(), Code: "OK"}}
	if err := session.NewWriter(agent, 4).SendContext(ctx, protocol.MessageStateUpdate, update); err != nil {
		t.Fatal(err)
	}
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("wire update not applied")
	}
	server := &Server{registry: sessions.registry, sessions: sessions}
	response := httptest.NewRecorder()
	server.HTTPHandler(testAPIToken).ServeHTTP(response, apiRequest(http.MethodGet, "/v1/nodes/n", ""))
	var record NodeRecord
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &record) != nil || record.Resources.Resources[0].State != "stopped" || record.Resources.Resources[0].Stale || record.StateRevision != 1 {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}
