package onboarding

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNativeSandboxProbeRefusesWithoutInstalledBootedHost(t *testing.T) {
	// Never launch a service in tests. Only exercise early native refusal where
	// the caller or PID1 cannot satisfy the production preconditions.
	pid1, err := os.ReadFile("/proc/1/comm")
	if os.Geteuid() == 0 && err == nil && strings.TrimSpace(string(pid1)) == "systemd" {
		t.Skip("booted root host: avoid reading production configuration")
	}
	h := nativeSandboxProbeHost{}
	if _, err := h.Config(context.Background()); err == nil {
		t.Fatal("config accepted")
	}
	if _, err := h.InspectProbe(context.Background()); err == nil {
		t.Fatal("snapshot accepted")
	}
	if _, err := h.RunProbe(context.Background(), SandboxProbeSnapshot{}, SandboxProbeInputs{}); err == nil {
		t.Fatal("run accepted")
	}
}

func TestNativeSandboxStartRefusesTestExecutable(t *testing.T) {
	// The current executable is a test binary, never the installed agent. The
	// adapter must refuse before dispatch even on a booted root test host.
	h := nativeSandboxProbeHost{}
	snapshot := freshProbeFake(t).snapshot
	if err := h.startVerifiedSandboxAgent(context.Background(), snapshot, [32]byte{}, time.Now().Add(time.Minute)); err == nil {
		t.Fatal("zero proof accepted")
	}
	if err := h.startVerifiedSandboxAgent(context.Background(), snapshot, [32]byte{1}, time.Now().Add(time.Minute)); err == nil {
		t.Fatal("test executable dispatched a service start")
	}
}
