//go:build linux

package adapter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

func testScript(t *testing.T, body string, args ...string) Script {
	t.Helper()
	directory := t.TempDir()
	filename := filepath.Join(directory, "script.sh")
	if err := os.WriteFile(filename, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return Script{Commands: map[string]ScriptCommand{"diagnose": {Executable: "/bin/bash", Argv: append([]string{filename}, args...), WorkingDirectory: directory, Timeout: time.Second}}}
}

func TestScriptExitAndBoundedOutput(t *testing.T) {
	for _, test := range []struct {
		name, body, code   string
		success, truncated bool
	}{
		{"success", "printf 'stdout'; printf 'stderr' >&2", "OK", true, false},
		{"failed", "printf failure >&2; exit 7", "EXECUTION_FAILED", false, false},
		{"signal", "kill -KILL $$", "EXECUTION_UNKNOWN", false, false},
		{"flood", "for ((i=0;i<10000;i++)); do printf 'abcdefghij'; printf 'stderr' >&2; done", "OK", true, true},
		{"utf8", "for ((i=0;i<4096;i++)); do printf 'ž'; done", "OK", true, true},
		{"invalid-utf8", "printf '\\377hello'", "OK", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := testScript(t, test.body).Execute(context.Background(), "diagnose")
			if result.Code != test.code || result.Success != test.success || len(result.Message) > 4096 || !utf8.ValidString(result.Message) || strings.Contains(result.Message, "[output truncated]") != test.truncated {
				t.Fatalf("result=%+v", result)
			}
			if test.name == "success" && result.Message != "stdoutstderr" {
				t.Fatalf("combined output=%q", result.Message)
			}
		})
	}
}

func TestScriptUsesFixedArgumentsCwdAndCleanEnvironment(t *testing.T) {
	t.Setenv("HLCP_API_TOKEN", "must-not-inherit")
	t.Setenv("SSH_AUTH_SOCK", "must-not-inherit")
	literal := "$(touch unsafe); echo remote"
	script := testScript(t, `printf '%s\n%s\n%s\n%s\n%s' "$1" "$PWD" "${HLCP_API_TOKEN-unset}" "${SSH_AUTH_SOCK-unset}" "$CUSTOM"`, literal)
	spec := script.Commands["diagnose"]
	spec.Environment = map[string]string{"CUSTOM": "local-value"}
	script.Commands["diagnose"] = spec
	result := script.Execute(context.Background(), "diagnose")
	want := literal + "\n" + spec.WorkingDirectory + "\nunset\nunset\nlocal-value"
	if !result.Success || result.Message != want {
		t.Fatalf("result=%+v want=%q", result, want)
	}
	if _, err := os.Stat(filepath.Join(spec.WorkingDirectory, "unsafe")); !os.IsNotExist(err) {
		t.Fatal("argv interpreted as shell")
	}
}

func TestScriptUnknownActionAndStartupFailure(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "executed")
	script := testScript(t, `printf executed > "$1"`, marker)
	if result := script.Execute(context.Background(), "unknown"); result.Code != "ACTION_NOT_SUPPORTED" {
		t.Fatalf("result=%+v", result)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("unknown action executed")
	}
	spec := script.Commands["diagnose"]
	spec.Executable = "/does-not-exist/hlcp"
	script.Commands["diagnose"] = spec
	if result := script.Execute(context.Background(), "diagnose"); result.Code != "EXECUTION_NOT_STARTED" {
		t.Fatalf("result=%+v", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := script.Execute(ctx, "diagnose"); result.Code != "EXECUTION_NOT_STARTED" {
		t.Fatalf("result=%+v", result)
	}
}

func TestScriptUnreapedProcessCapacityFailsBeforeStartup(t *testing.T) {
	script := testScript(t, "printf unexpected")
	scriptProcessSlot <- struct{}{}
	defer func() { <-scriptProcessSlot }()
	result := script.Execute(context.Background(), "diagnose")
	if result.Code != "EXECUTION_NOT_STARTED" || result.Success || strings.Contains(result.Message, "unexpected") {
		t.Fatalf("result=%+v", result)
	}
}

func TestScriptTimeoutKillsDescendants(t *testing.T) {
	directory := t.TempDir()
	pidFile := filepath.Join(directory, "child.pid")
	script := testScript(t, `trap '' TERM
(
  trap '' TERM
  while :; do sleep 0.05; done
) &
echo $! > "$1"
wait`, pidFile)
	spec := script.Commands["diagnose"]
	spec.Timeout = 200 * time.Millisecond
	script.Commands["diagnose"] = spec
	start := time.Now()
	result := script.Execute(context.Background(), "diagnose")
	if result.Code != "EXECUTION_UNKNOWN" || time.Since(start) > 2*time.Second {
		t.Fatalf("result=%+v duration=%s", result, time.Since(start))
	}
	requireDeadChild(t, pidFile)
}

func TestScriptHeldPipesAndBackgroundChildren(t *testing.T) {
	for _, closePipes := range []bool{false, true} {
		t.Run(fmt.Sprint(closePipes), func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "child.pid")
			redirect := ""
			if closePipes {
				redirect = " >/dev/null 2>&1"
			}
			script := testScript(t, "sleep 60"+redirect+" &\necho $! > \"$1\"\nexit 0", pidFile)
			start := time.Now()
			result := script.Execute(context.Background(), "diagnose")
			want := "EXECUTION_UNKNOWN"
			if closePipes {
				want = "OK"
			}
			if result.Code != want || time.Since(start) > 2*time.Second {
				t.Fatalf("result=%+v", result)
			}
			requireDeadChild(t, pidFile)
		})
	}
}

func requireDeadChild(t *testing.T, pidFile string) {
	t.Helper()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	dead := func() bool {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if os.IsNotExist(err) {
			return true
		}
		// Orphan zombies can await PID 1's reaper in a disposable container.
		return err == nil && strings.Contains(string(data), ") Z ")
	}
	t.Cleanup(func() {
		if !dead() {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	deadline := time.Now().Add(time.Second)
	for !dead() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !dead() {
		t.Fatalf("child %d survived script group cleanup", pid)
	}
}
