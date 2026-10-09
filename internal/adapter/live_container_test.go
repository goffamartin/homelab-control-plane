package adapter

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in only: the caller creates and later removes its isolated test container.
func TestLiveStandaloneContainerLifecycle(t *testing.T) {
	runtime, name := os.Getenv("HLCP_LIVE_RUNTIME"), os.Getenv("HLCP_LIVE_CONTAINER")
	if runtime == "" && name == "" {
		t.Skip("set HLCP_LIVE_RUNTIME and HLCP_LIVE_CONTAINER for isolated live verification")
	}
	if (runtime != "docker" && runtime != "podman") || !strings.HasPrefix(name, "hlcp-f7-smoke-") {
		t.Fatal("live test requires an explicitly named hlcp-f7-smoke container")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	label, err := (systemRunner{}).Run(ctx, runtime, "container", "inspect", "--format", `{{index .Config.Labels "hlcp.test"}}`, name)
	if err != nil || strings.TrimSpace(label) != "f7" {
		t.Fatalf("test ownership check failed: %v %q", err, label)
	}
	container := Container{Runtime: runtime, Name: name}
	if state := container.Observe(ctx); state.State != "stopped" {
		t.Fatalf("test container must initially be stopped: %+v", state)
	}
	for _, action := range []string{"start", "restart", "stop"} {
		result := container.Execute(ctx, action)
		want := "running"
		if action == "stop" {
			want = "stopped"
		}
		state := container.Observe(ctx)
		if !result.Success || state.State != want {
			t.Fatalf("action=%s result=%+v state=%+v", action, result, state)
		}
	}
	if result := container.Execute(ctx, "start"); !result.Success {
		t.Fatalf("start=%+v", result)
	}
	// External stop is not performed through the HLCP adapter.
	if output, err := (systemRunner{}).Run(ctx, runtime, "stop", name); err != nil {
		t.Fatalf("external stop: %v %s", err, output)
	}
	if state := container.Observe(ctx); state.State != "stopped" {
		t.Fatalf("external stop not observed: %+v", state)
	}
	if result := container.Execute(ctx, "status"); !result.Success || result.Message != "stopped" {
		t.Fatalf("stopped status=%+v", result)
	}
}
