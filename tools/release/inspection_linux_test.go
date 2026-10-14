package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOnboardingInspectionScriptSyntaxAndEarlyRefusals(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "debian", "inspect-onboarding-host.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "inspect.sh")
	if err := os.WriteFile(script, []byte(strings.ReplaceAll(string(data), "\r\n", "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		args     []string
		success  bool
		contains string
	}{
		{[]string{"-n", script}, true, ""},
		{[]string{script, "--help"}, true, "Read-only"},
		{[]string{script}, false, "Select exactly"},
		{[]string{script, "arbitrary.service"}, false, "Invalid role"},
		{[]string{script, "agent", "--start"}, false, "Select exactly"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		command := exec.CommandContext(ctx, "/bin/sh", test.args...)
		out, err := command.CombinedOutput()
		cancel()
		if (err == nil) != test.success || !strings.Contains(string(out), test.contains) {
			t.Fatalf("%v: %v %s", test.args, err, out)
		}
	}
	// Never collect a live host during unit tests. Non-booted containers must
	// refuse before package/binary/systemctl inspection.
	pid1, _ := os.ReadFile("/proc/1/comm")
	if strings.TrimSpace(string(pid1)) != "systemd" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "/bin/sh", script, "agent").CombinedOutput()
		if err == nil || !strings.Contains(string(out), "Booted local systemd required") || strings.Contains(string(out), "CallerUID=") {
			t.Fatal(err, string(out))
		}
	}
}
