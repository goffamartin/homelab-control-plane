package command

import (
	"hlcp/internal/protocol"
	"testing"
)

func TestDispatcherCachesDuplicateCommand(t *testing.T) {
	dispatcher, calls := NewDispatcher(), 0
	dispatcher.Register("minecraft", func(action string) protocol.CommandResultPayload {
		calls++
		return protocol.CommandResultPayload{Success: action == "restart", Code: "OK"}
	})
	command := protocol.CommandPayload{CommandID: "cmd-1", ResourceID: "minecraft", Action: "restart"}
	ack, result := dispatcher.Execute(command)
	if ack.Status != "accepted" || !result.Success {
		t.Fatalf("first result = %#v / %#v", ack, result)
	}
	ack, result = dispatcher.Execute(command)
	if ack.Status != "completed" || !result.Success || calls != 1 {
		t.Fatalf("duplicate result = %#v / %#v, calls=%d", ack, result, calls)
	}
}

func TestDispatcherRejectsUnknownResource(t *testing.T) {
	ack, result := NewDispatcher().Execute(protocol.CommandPayload{CommandID: "cmd-1", ResourceID: "unknown", Action: "restart"})
	if ack.Status != "rejected" || result.Code != "TARGET_NOT_FOUND" {
		t.Fatalf("result = %#v / %#v", ack, result)
	}
}
