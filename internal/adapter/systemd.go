// Package adapter contains agent-local implementations behind configured resources.
package adapter

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"hlcp/internal/protocol"
)

type CommandRunner interface {
	Run(context.Context, string, ...string) (string, error)
}
type systemRunner struct{}

func (systemRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	return string(output), err
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
		return failure("EXECUTION_FAILED", strings.TrimSpace(output))
	}
	return protocol.CommandResultPayload{Success: true, Code: "OK", Message: strings.TrimSpace(output)}
}
func failure(code, message string) protocol.CommandResultPayload {
	return protocol.CommandResultPayload{Code: code, Message: message}
}
