package onboarding

import (
	"context"
	"os"
	"strings"
	"testing"
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
