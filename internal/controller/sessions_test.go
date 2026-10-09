package controller

import (
	"context"
	"crypto/tls"
	"hlcp/internal/liveness"
	"hlcp/internal/protocol"
	"hlcp/internal/session"
	"net"
	"testing"
	"time"
)

func TestReplacementClosesOldSessionAndRejectsItsResult(t *testing.T) {
	sessions := NewActiveSessions(NewCommandTracker())
	old, oldPeer := net.Pipe()
	defer old.Close()
	defer oldPeer.Close()
	newer, newPeer := net.Pipe()
	defer newer.Close()
	defer newPeer.Close()
	oldWriter, newWriter := session.NewWriter(old, 1), session.NewWriter(newer, 1)
	oldContext, cancelOld := context.WithCancel(context.Background())
	defer cancelOld()
	_, cancelNew := context.WithCancel(context.Background())
	defer cancelNew()
	sync := session.Synchronization{Inventory: protocol.InventorySnapshot{Revision: 1}}
	sessions.Publish(session.Registration{NodeID: "n", SessionID: "old"}, sync, time.Now(), oldWriter, cancelOld)
	if err := sessions.tracker.TrackNode("n", "c"); err != nil {
		t.Fatal(err)
	}
	sessions.Publish(session.Registration{NodeID: "n", SessionID: "new"}, sync, time.Now(), newWriter, cancelNew)
	if oldContext.Err() == nil {
		t.Fatal("old context still active")
	}
	oldPeer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := oldPeer.Read(make([]byte, 1)); err == nil {
		t.Fatal("old socket still open")
	}
	result := protocol.CommandResultPayload{CommandID: "c", Success: true, Code: "OK"}
	if sessions.HandleResult("n", oldWriter, result) {
		t.Fatal("accepted stale result")
	}
	if sessions.HandleResult("other", newWriter, result) {
		t.Fatal("accepted foreign node result")
	}
	if !sessions.HandleResult("n", newWriter, result) {
		t.Fatal("reconnected node result rejected")
	}
	sessions.Remove("n", oldWriter)
	record, _ := sessions.registry.Get("n")
	if record.SessionID != "new" || record.State != liveness.NodeOnline {
		t.Fatalf("record=%+v", record)
	}
	lastSeen := record.LastActivity
	sessions.Remove("n", newWriter)
	record, _ = sessions.registry.Get("n")
	if !record.LastActivity.Equal(lastSeen) || record.DisconnectedAt.IsZero() {
		t.Fatal("disconnect overwrote last_seen")
	}
}

func TestServerCloseWaitsForHandshakeWorkers(t *testing.T) {
	server, err := Listen("127.0.0.1:0", Config{TLSConfig: &tls.Config{}, LivenessPolicy: liveness.Policy{HeartbeatInterval: time.Second, DegradedAfter: 2, OfflineAfter: 3}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	connection, err := net.Dial("tcp", server.Address().String())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	deadline := time.Now().Add(time.Second)
	for {
		server.connectionMutex.Lock()
		accepted := len(server.connections) == 1
		server.connectionMutex.Unlock()
		if accepted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("connection was not accepted")
		}
		time.Sleep(time.Millisecond)
	}
	closed := make(chan struct{})
	go func() { server.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close blocked on handshake")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not stop")
	}
}
