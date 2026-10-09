// Package adapter contains agent-local implementations behind configured resources.
package adapter

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"

	"hlcp/internal/protocol"
)

type CommandRunner interface {
	Run(context.Context, string, ...string) (string, error)
}
type systemRunner struct{}

type boundedOutput struct {
	mutex sync.Mutex
	data  []byte
}

func (output *boundedOutput) Write(data []byte) (int, error) {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	remaining := 4096 - len(output.data)
	if remaining > len(data) {
		remaining = len(data)
	}
	output.data = append(output.data, data[:remaining]...)
	return len(data), nil
}

func (systemRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.WaitDelay = time.Second
	output := new(boundedOutput)
	command.Stdout, command.Stderr = output, output
	err := command.Run()
	if err != nil && len(output.data) == 0 {
		return err.Error(), err
	}
	return string(output.data), err
}

type Systemd struct {
	Unit   string
	Runner CommandRunner
}

func (adapter Systemd) Execute(ctx context.Context, action string) protocol.CommandResultPayload {
	if adapter.Unit == "" {
		return failure("INVALID_CONFIGURATION", "systemd unit is required")
	}
	if adapter.Runner == nil {
		adapter.Runner = systemRunner{}
	}
	var args []string
	switch action {
	case "status":
		args = []string{"is-active", "--quiet", adapter.Unit}
	case "start", "stop", "restart":
		args = []string{action, adapter.Unit}
	default:
		return failure("ACTION_NOT_SUPPORTED", "systemd action is not allowed")
	}
	output, err := adapter.Runner.Run(ctx, "systemctl", args...)
	if err != nil {
		return executionFailure(ctx, err, output, action)
	}
	return protocol.CommandResultPayload{Success: true, Code: "OK", Message: strings.TrimSpace(output)}
}
func failure(code, message string) protocol.CommandResultPayload {
	if len(message) > 4096 {
		message = message[:4096]
	}
	return protocol.CommandResultPayload{Code: code, Message: message}
}

func executionFailure(ctx context.Context, err error, output, action string) protocol.CommandResultPayload {
	message := strings.TrimSpace(output)
	if message == "" {
		message = err.Error()
	}
	if ctx.Err() != nil || errors.Is(err, exec.ErrWaitDelay) {
		if action != "status" {
			return failure("EXECUTION_UNKNOWN", "external action interrupted; outcome is uncertain")
		}
		return failure("TIMEOUT", message)
	}
	if errors.Is(err, exec.ErrNotFound) {
		return failure("RUNTIME_UNAVAILABLE", message)
	}
	return failure("EXECUTION_FAILED", message)
}
