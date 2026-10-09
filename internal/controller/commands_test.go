package controller

import (
	"context"
	"hlcp/internal/protocol"
	"testing"
	"time"
)

func TestCommandTrackerCorrelatesResult(t *testing.T) {
	tracker := NewCommandTracker()
	if err := tracker.Track("cmd-1"); err != nil {
		t.Fatal(err)
	}
	go tracker.Complete(protocol.CommandResultPayload{CommandID: "cmd-1", Success: true, Code: "OK"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := tracker.Wait(ctx, "cmd-1")
	if err != nil || !result.Success {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
