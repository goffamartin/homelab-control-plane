package agent

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"hlcp/internal/protocol"
	"hlcp/internal/session"
)

type observationRunner struct{ state atomic.Value }

func (runner *observationRunner) Run(_ context.Context, _ string, args ...string) (string, error) {
	if args[0] == "container" {
		return runner.state.Load().(string), nil
	}
	switch args[0] {
	case "start", "restart":
		runner.state.Store("running")
	case "stop":
		runner.state.Store("exited")
	}
	return "catalog", nil
}
func observationConfig(t *testing.T, runner *observationRunner) Config {
	t.Helper()
	return Config{NodeID: "n", JournalFile: filepath.Join(t.TempDir(), "journal.json"), runner: runner, refresh: make(chan struct{}, 1), Resources: []ResourceConfig{{ID: "catalog", Type: "container", Adapter: "docker", Target: "catalog", Actions: []string{"status", "start", "stop", "restart"}}}}
}
func observationLoop(t *testing.T, config Config) (net.Conn, context.CancelFunc) {
	t.Helper()
	agent, controller := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- config.runObservations(ctx, session.NewWriter(agent, 4), "s", 1) }()
	t.Cleanup(func() {
		cancel()
		agent.Close()
		controller.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("observation loop did not join")
		}
	})
	controller.SetDeadline(time.Now().Add(2 * time.Second))
	return controller, cancel
}
func readObservation(t *testing.T, controller net.Conn) protocol.StateUpdate {
	t.Helper()
	frame, err := protocol.Decode(controller)
	if err != nil || frame.Header.Type != protocol.MessageStateUpdate {
		t.Fatalf("type=%v err=%v", frame.Header.Type, err)
	}
	var update protocol.StateUpdate
	if err := protocol.DecodePayload(frame.Payload, &update); err != nil || update.Validate() != nil {
		t.Fatalf("update=%+v err=%v", update, err)
	}
	return update
}
func TestObservationAtConnectAndAfterAction(t *testing.T) {
	runner := new(observationRunner)
	runner.state.Store("exited")
	config := observationConfig(t, runner)
	config.ObservationInterval = time.Hour
	sync, err := config.synchronization()
	if err != nil || sync.State.Resources[0].State != "unknown" || !sync.State.Resources[0].ObservedAt.IsZero() {
		t.Fatalf("sync=%+v err=%v", sync, err)
	}
	controller, _ := observationLoop(t, config)
	first := readObservation(t, controller)
	if first.Resource.State != "stopped" || first.Revision != 1 || first.SessionID != "s" {
		t.Fatalf("first=%+v", first)
	}
	dispatcher, err := config.NewDispatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer dispatcher.Close()
	_, result := dispatcher.Execute(protocol.CommandPayload{CommandID: "c", ResourceID: "catalog", Action: "restart"})
	if !result.Success {
		t.Fatalf("result=%+v", result)
	}
	second := readObservation(t, controller)
	if second.Resource.State != "running" || second.Revision != 2 {
		t.Fatalf("second=%+v", second)
	}
}
func TestPeriodicObservationSeesExternalStop(t *testing.T) {
	runner := new(observationRunner)
	runner.state.Store("running")
	config := observationConfig(t, runner)
	config.ObservationInterval = 20 * time.Millisecond
	controller, _ := observationLoop(t, config)
	readObservation(t, controller)
	runner.state.Store("exited")
	for {
		update := readObservation(t, controller)
		if update.Resource.State == "stopped" {
			break
		}
	}
}

type blockingObservationRunner struct {
	started chan struct{}
	active  atomic.Int32
	maximum atomic.Int32
}

func (runner *blockingObservationRunner) Run(ctx context.Context, _ string, _ ...string) (string, error) {
	active := runner.active.Add(1)
	defer runner.active.Add(-1)
	for {
		maximum := runner.maximum.Load()
		if active <= maximum || runner.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	runner.started <- struct{}{}
	<-ctx.Done()
	return "", ctx.Err()
}
func TestObservationWorkersAreBoundedAndJoinOnCancellation(t *testing.T) {
	runner := &blockingObservationRunner{started: make(chan struct{}, observationWorkers)}
	config := Config{runner: runner}
	for i := 0; i < 16; i++ {
		config.Resources = append(config.Resources, ResourceConfig{ID: fmt.Sprintf("r-%d", i), Adapter: "docker", Target: "catalog"})
	}
	_, cancel := observationLoop(t, config)
	for i := 0; i < observationWorkers; i++ {
		select {
		case <-runner.started:
		case <-time.After(time.Second):
			t.Fatal("workers did not start")
		}
	}
	if runner.maximum.Load() > observationWorkers {
		t.Fatal("worker capacity exceeded")
	}
	cancel()
}

func TestSlowObservationDoesNotBlockHeartbeatWriter(t *testing.T) {
	runner := &blockingObservationRunner{started: make(chan struct{}, observationWorkers)}
	config := Config{runner: runner, Resources: []ResourceConfig{{ID: "catalog", Adapter: "docker", Target: "catalog"}}}
	agent, controller := net.Pipe()
	defer controller.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := session.NewWriter(agent, 4)
	done := make(chan error, 1)
	go func() { done <- config.runObservations(ctx, writer, "s", 1) }()
	defer func() { cancel(); agent.Close(); <-done }()
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("observer did not start")
	}
	heartbeatDone := make(chan error, 1)
	go func() {
		heartbeatDone <- writer.SendContext(ctx, protocol.MessageHeartbeat, protocol.HeartbeatPayload{Sequence: 1})
	}()
	controller.SetReadDeadline(time.Now().Add(time.Second))
	frame, err := protocol.Decode(controller)
	if err != nil || frame.Header.Type != protocol.MessageHeartbeat {
		t.Fatalf("frame=%v err=%v", frame.Header.Type, err)
	}
	if err := <-heartbeatDone; err != nil {
		t.Fatal(err)
	}
}
