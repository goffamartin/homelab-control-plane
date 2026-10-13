//go:build linux

package adapter

import (
	"context"
	"errors"
	"os/exec"
	"path"
	"syscall"
	"time"

	"hlcp/internal/protocol"
)

const scriptTerminationGrace = 250 * time.Millisecond
const scriptKillWait = time.Second

// The agent has one execution worker. Keep its process slot occupied even if
// an OS-level unkillable process outlives the bounded wait, preventing an
// unbounded accumulation of abandoned Wait goroutines/processes.
var scriptProcessSlot = make(chan struct{}, 1)

func executeScript(ctx context.Context, spec ScriptCommand) protocol.CommandResultPayload {
	if !path.IsAbs(spec.Executable) || !path.IsAbs(spec.WorkingDirectory) {
		return failure("INVALID_CONFIGURATION", "script executable and working directory must be absolute")
	}
	if ctx.Err() != nil {
		return failure("EXECUTION_NOT_STARTED", "script canceled before startup")
	}
	select {
	case scriptProcessSlot <- struct{}{}:
	default:
		return failure("EXECUTION_NOT_STARTED", "previous script process has not been reaped")
	}
	release := true
	defer func() {
		if release {
			<-scriptProcessSlot
		}
	}()
	command := exec.Command(spec.Executable, spec.Argv...)
	command.Dir = spec.WorkingDirectory
	command.Env = scriptEnvironment(spec.Environment)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = scriptTerminationGrace // Bounds inherited output pipes after leader exit.
	output := new(scriptOutput)
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		return failure("EXECUTION_NOT_STARTED", "configured script could not be started")
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	group := -command.Process.Pid
	unknown := func() protocol.CommandResultPayload {
		return failure("EXECUTION_UNKNOWN", output.message("script interrupted; external outcome is uncertain\n"))
	}
	select {
	case err := <-wait:
		// A command owns its group, including background children on normal exit.
		// A descendant escaping with setsid is not contained by this mechanism.
		_ = syscall.Kill(group, syscall.SIGKILL)
		if ctx.Err() != nil || errors.Is(err, exec.ErrWaitDelay) {
			return unknown()
		}
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
					return unknown()
				}
			}
			return failure("EXECUTION_FAILED", output.message(""))
		}
		return protocol.CommandResultPayload{Success: true, Code: "OK", Message: output.message("")}
	case <-ctx.Done():
		_ = syscall.Kill(group, syscall.SIGTERM)
		timer := time.NewTimer(scriptTerminationGrace)
		defer timer.Stop()
		select {
		case <-wait:
			_ = syscall.Kill(group, syscall.SIGKILL)
			return unknown()
		case <-timer.C:
			_ = syscall.Kill(group, syscall.SIGKILL)
		}
		timer.Reset(scriptKillWait)
		select {
		case <-wait:
		case <-timer.C:
			release = false
			go func() { <-wait; <-scriptProcessSlot }()
		}
		// Even an unkillable process cannot make the dispatcher wait indefinitely.
		// Wait's buffered channel and synchronized output allow late OS cleanup.
		return unknown()
	}
}
