package onboarding

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSandboxLauncherPipeHelper(t *testing.T) {
	if len(os.Args) < 2 {
		return
	}
	mode := os.Args[len(os.Args)-1]
	if !strings.HasPrefix(mode, "hlcp-pipe-test-") {
		return
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(2)
	}
	switch mode {
	case "hlcp-pipe-test-echo":
		os.Stdout.Write(data)
	case "hlcp-pipe-test-flood":
		os.Stdout.Write(bytes.Repeat([]byte("x"), 8192))
	case "hlcp-pipe-test-wait":
		<-time.After(10 * time.Second)
	case "hlcp-pipe-test-fail":
		os.Exit(1)
	}
	os.Exit(0)
}
func TestSandboxLauncherRealPipeWaitCancelAndOutputBounds(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"echo", "flood", "fail", "wait", "close"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			process, err := spawnSandboxClient(ctx, self, []string{"-test.run=^TestSandboxLauncherPipeHelper$", "--", "hlcp-pipe-test-" + mode})
			if err != nil {
				t.Fatal(err)
			}
			defer process.Close()
			if mode == "close" {
				process.Close()
				process.Close()
				return
			}
			exchangeCtx := ctx
			if mode == "wait" {
				var stop context.CancelFunc
				exchangeCtx, stop = context.WithTimeout(ctx, 100*time.Millisecond)
				defer stop()
			}
			output, err := process.Exchange(exchangeCtx, []byte("fixed-input"))
			if mode == "echo" {
				if err != nil || string(output) != "fixed-input" {
					t.Fatal(err, string(output))
				}
			} else if !errors.Is(err, ErrSandboxProbeIncomplete) {
				t.Fatal("failure/cancel/flood accepted", err)
			}
			if _, err := process.Exchange(ctx, []byte("repeat")); !errors.Is(err, ErrSandboxProbeIncomplete) {
				t.Fatal("process repeated", err)
			}
		})
	}
}

func TestNativeSandboxLauncherRejectsUnapprovedCommandsAndNonBootedHost(t *testing.T) {
	for _, row := range []struct {
		path string
		args []string
	}{{"/bin/sh", sandboxLaunchArgs()}, {"/usr/bin/systemd-run", []string{"--shell"}}, {"/usr/bin/systemctl", []string{"start", "hlcp-agent.service"}}} {
		if _, err := startSandboxClient(context.Background(), row.path, row.args); !errors.Is(err, ErrSandboxProbeInvalid) {
			t.Fatal("arbitrary command accepted", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := startSandboxClient(ctx, "/usr/bin/systemd-run", sandboxLaunchArgs()); !errors.Is(err, ErrSandboxProbeInvalid) {
		t.Fatal(err)
	}
	pid1, _ := os.ReadFile("/proc/1/comm")
	if string(pid1) != "systemd\n" {
		if _, err := (nativeSandboxLaunchTransport{}).Begin(context.Background()); !errors.Is(err, ErrSandboxProbeInvalid) {
			t.Fatal("non-booted host launch accepted", err)
		}
	}
}
func TestSandboxLauncherOutputBound(t *testing.T) {
	output := &sandboxOutput{limit: 4}
	if n, err := output.Write([]byte("abcdef")); n != 6 || err != nil || !output.overflow || string(output.data) != "abcd" {
		t.Fatal("bad output bound")
	}
	if n, _ := output.Write([]byte("next")); n != 4 || string(output.data) != "abcd" {
		t.Fatal("output grew after overflow")
	}
}

func TestNativeSandboxLauncherUnitSyntax(t *testing.T) {
	if os.Getenv("HLCP_SANDBOX_SYNTAX_TEST") != "disposable-container" || os.Geteuid() != 0 {
		t.Skip("disposable syntax fixture")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("not a container")
	}
	if _, err := os.Stat("/run/systemd/system"); !os.IsNotExist(err) {
		t.Fatal("booted host fixture refused")
	}
	const binary = "/usr/bin/hlcp-agent"
	file, err := os.OpenFile(binary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0755)
	if err != nil {
		t.Fatal("fixture must not replace an installed binary", err)
	}
	defer os.Remove(binary)
	data, err := os.ReadFile("/bin/true")
	if err != nil {
		file.Close()
		t.Fatal(err)
	}
	if _, err = file.Write(data); err != nil {
		file.Close()
		t.Fatal(err)
	}
	file.Close()
	unit := "[Unit]\nCollectMode=inactive-or-failed\n[Service]\nType=exec\nExecStart=/usr/bin/hlcp-agent --internal-sandbox-inspector\n"
	for _, arg := range sandboxLaunchArgs() {
		if strings.HasPrefix(arg, "--property=") {
			unit += strings.TrimPrefix(arg, "--property=") + "\n"
		}
	}
	path := filepath.Join(t.TempDir(), sandboxProbeUnit)
	if err := os.WriteFile(path, []byte(unit), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/usr/bin/systemd-analyze", "verify", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("unit syntax: %v\n%s", err, output)
	}
}
