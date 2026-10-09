package adapter

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestContainerTypedObservationAndStatus(t *testing.T) {
	for _, runtime := range []string{"docker", "podman"} {
		for raw, want := range map[string]string{"running": "running", "created": "stopped", "initialized": "stopped", "exited": "stopped", "stopped": "stopped", "paused": "unknown", "restarting": "unknown", "surprise": "unknown", "dead": "error"} {
			runner := &fakeRunner{output: raw}
			adapter := Container{Runtime: runtime, Name: "catalog", Runner: runner}
			state := adapter.Observe(context.Background())
			if state.State != want || state.ObservedAt.IsZero() || runner.name != runtime || len(runner.args) != 5 || runner.args[0] != "container" || runner.args[1] != "inspect" || runner.args[4] != "catalog" {
				t.Fatalf("runtime=%s raw=%s state=%+v args=%v", runtime, raw, state, runner.args)
			}
			if result := adapter.Execute(context.Background(), "status"); result.Success != (want == "running" || want == "stopped") {
				t.Fatalf("result=%+v", result)
			}
		}
	}
}
func TestContainerLifecycleArguments(t *testing.T) {
	for _, runtime := range []string{"docker", "podman"} {
		for _, action := range []string{"start", "stop", "restart"} {
			runner := &fakeRunner{}
			result := (Container{Runtime: runtime, Name: "catalog", Runner: runner}).Execute(context.Background(), action)
			if !result.Success || runner.name != runtime || len(runner.args) != 2 || runner.args[0] != action || runner.args[1] != "catalog" {
				t.Fatalf("result=%+v runner=%+v", result, runner)
			}
		}
	}
}
func TestObservationFailuresDoNotClaimStopped(t *testing.T) {
	for message, want := range map[string]string{"No such object: catalog": "TARGET_NOT_FOUND", "no container with name or ID catalog found": "TARGET_NOT_FOUND", "permission denied": "PERMISSION_DENIED", "Cannot connect to the Docker daemon": "RUNTIME_UNAVAILABLE", "other exit failure": "OBSERVATION_FAILED"} {
		state := (Container{Runtime: "docker", Name: "catalog", Runner: &fakeRunner{output: message, err: errors.New("exit")}}).Observe(context.Background())
		if state.State != "error" || state.Code != want {
			t.Fatalf("state=%+v", state)
		}
	}
	state := (Container{Runtime: "podman", Name: "catalog", Runner: &fakeRunner{err: exec.ErrNotFound}}).Observe(context.Background())
	if state.Code != "RUNTIME_UNAVAILABLE" {
		t.Fatalf("state=%+v", state)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state = (Container{Runtime: "docker", Name: "catalog", Runner: &fakeRunner{err: context.Canceled}}).Observe(ctx)
	if state.Code != "TIMEOUT" {
		t.Fatalf("state=%+v", state)
	}
	state = (Container{Runtime: "docker", Name: "catalog", Runner: &fakeRunner{output: strings.Repeat("€", 2000)}}).Observe(context.Background())
	if len(state.Message) > 4096 || !utf8.ValidString(state.Message) {
		t.Fatal("diagnostic is not bounded UTF8")
	}
}
func TestSystemdInactiveAndMissingUnitAreDistinct(t *testing.T) {
	for output, want := range map[string]string{"LoadState=loaded\nActiveState=active": "running", "ActiveState=inactive\nLoadState=loaded": "stopped", "LoadState=not-found\nActiveState=inactive": "error", "LoadState=loaded\nActiveState=failed": "error"} {
		state := (Systemd{Unit: "catalog.service", Runner: &fakeRunner{output: output}}).Observe(context.Background())
		if state.State != want {
			t.Fatalf("state=%+v output=%q", state, output)
		}
	}
}
