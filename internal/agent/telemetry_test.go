package agent

import (
	"context"
	"hlcp/internal/protocol"
	"hlcp/internal/telemetry"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestMTLSHeartbeatAndCommandWithBlockedAuditSink(t *testing.T) {
	serverFiles, clientFiles := e2ePKI(t)
	entered := make(chan struct{})
	var once sync.Once
	diagnostics := telemetry.New(func(ctx context.Context, _ telemetry.Event) error {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return ctx.Err()
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := diagnostics.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	directory := t.TempDir()
	fixture := startE2EController(t, serverFiles, filepath.Join(directory, "controller.json"), diagnostics)
	runner := &e2eRunner{}
	agentDiagnostics := telemetry.New(nil)
	stopAgent := startE2EAgent(t, Config{ControllerAddress: fixture.server.Address().String(), ServerName: clientFiles.ServerName,
		CertificateFile: clientFiles.CertificateFile, PrivateKeyFile: clientFiles.PrivateKeyFile, CAFile: clientFiles.CAFile,
		NodeID: "debian-01", AgentVersion: "telemetry-e2e", Capabilities: []string{"resource.control", "docker"},
		JournalFile: filepath.Join(directory, "agent.json"), runner: runner, Telemetry: agentDiagnostics,
		Resources: []ResourceConfig{{ID: "catalog", Type: "container", Adapter: "docker", Target: "catalog", Actions: []string{"status", "restart"}}},
	})
	e2eEventually(t, "session ready", func() bool {
		node, ok := fixture.server.Registry().Get("debian-01")
		return ok && node.State == "online"
	})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("sink not blocked")
	}
	for i := 0; i < 10000; i++ {
		diagnostics.Record(telemetry.Event{Kind: telemetry.Authenticated, Code: "FLOOD"})
	}
	before, _ := fixture.server.Registry().Get("debian-01")
	e2eEventually(t, "heartbeat while sink blocked", func() bool {
		node, _ := fixture.server.Registry().Get("debian-01")
		return node.LastActivity.After(before.LastActivity) && node.State == "online"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := fixture.server.Sessions().Execute(ctx, "debian-01", protocol.CommandPayload{CommandID: "audit-op", ResourceID: "catalog", Action: "restart"})
	if err != nil || !result.Success || runner.calls.Load() != 1 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, runner.calls.Load())
	}
	s := diagnostics.Snapshot().Counters
	if s["audit_dropped"] == 0 || s["command_result"] != 1 || s["command_ack"] != 1 || s["read_frames"] < 4 || s["written_frames"] < 2 {
		t.Fatalf("counters=%v", s)
	}
	as := agentDiagnostics.Snapshot().Counters
	if as["command_running"] != 1 || as["command_result"] != 1 || as["command_ack"] != 1 {
		t.Fatalf("agent counters=%v", as)
	}
	stopAgent()
	fixture.stop(t)
	if diagnostics.Snapshot().Gauges["connections"] != 0 || diagnostics.Snapshot().Gauges["sessions"] != 0 {
		t.Fatal("connection gauges leaked after shutdown")
	}
}
