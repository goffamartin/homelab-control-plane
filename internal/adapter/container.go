package adapter

import (
	"context"
	"hlcp/internal/protocol"
	"strings"
)

// Container controls one locally configured Docker or Podman container.
type Container struct {
	Runtime string
	Name    string
	Runner  CommandRunner
}

func (adapter Container) Execute(ctx context.Context, action string) protocol.CommandResultPayload {
	if adapter.Name == "" || (adapter.Runtime != "docker" && adapter.Runtime != "podman") {
		return failure("INVALID_CONFIGURATION", "container runtime and name are required")
	}
	if adapter.Runner == nil {
		adapter.Runner = systemRunner{}
	}
	var args []string
	switch action {
	case "status":
		args = []string{"inspect", "--format", "{{.State.Status}}", adapter.Name}
	case "start", "stop", "restart":
		args = []string{action, adapter.Name}
	default:
		return failure("ACTION_NOT_SUPPORTED", "container action is not allowed")
	}
	output, err := adapter.Runner.Run(ctx, adapter.Runtime, args...)
	if err != nil {
		return failure("EXECUTION_FAILED", strings.TrimSpace(output))
	}
	return protocol.CommandResultPayload{Success: true, Code: "OK", Message: strings.TrimSpace(output)}
}
