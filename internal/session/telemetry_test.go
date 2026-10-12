package session

import (
	"context"
	"errors"
	"hlcp/internal/protocol"
	"hlcp/internal/telemetry"
	"net"
	"testing"
)

func TestWriteQueueCapacityIsCountedBeforeSocketIO(t *testing.T) {
	agent, peer := net.Pipe()
	defer agent.Close()
	defer peer.Close()
	diagnostics := telemetry.New(nil)
	writer := NewWriter(telemetry.Wrap(agent, diagnostics), 1)
	for i := 0; i < cap(writer.slots); i++ {
		writer.slots <- struct{}{}
	}
	err := writer.SendContext(context.Background(), protocol.MessageHeartbeat, protocol.HeartbeatPayload{Sequence: 1})
	if !errors.Is(err, ErrWriteQueueFull) {
		t.Fatal(err)
	}
	s := diagnostics.Snapshot().Counters
	if s["write_queue_full"] != 1 || s["written_bytes"] != 0 || s["written_frames"] != 0 {
		t.Fatalf("counters=%v", s)
	}
}
