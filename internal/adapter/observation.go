package adapter

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"hlcp/internal/protocol"
)

func diagnostic(message string) string {
	message = strings.TrimSpace(strings.ToValidUTF8(message, "?"))
	if len(message) > 4096 {
		message = message[:4096]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	return message
}
func observation(state, code, message string) protocol.ResourceState {
	return protocol.ResourceState{State: state, Code: code, Message: diagnostic(message), ObservedAt: time.Now().UTC()}
}
func observationFailure(ctx context.Context, err error, output string) protocol.ResourceState {
	message := diagnostic(output)
	if message == "" {
		message = diagnostic(err.Error())
	}
	code := "OBSERVATION_FAILED"
	switch {
	case ctx.Err() != nil || errors.Is(err, exec.ErrWaitDelay):
		code = "TIMEOUT"
	case errors.Is(err, exec.ErrNotFound):
		code = "RUNTIME_UNAVAILABLE"
	case errors.Is(err, os.ErrPermission):
		code = "PERMISSION_DENIED"
	default:
		lower := strings.ToLower(message)
		switch {
		case strings.Contains(lower, "permission denied") || strings.Contains(lower, "access denied"):
			code = "PERMISSION_DENIED"
		case strings.Contains(lower, "no such object") || strings.Contains(lower, "no such container") || strings.Contains(lower, "no container with name or id"):
			code = "TARGET_NOT_FOUND"
		case strings.Contains(lower, "cannot connect") || strings.Contains(lower, "can't connect") || strings.Contains(lower, "connection refused") || strings.Contains(lower, "error during connect"):
			code = "RUNTIME_UNAVAILABLE"
		}
	}
	return observation("error", code, message)
}
func (adapter Container) Observe(ctx context.Context) protocol.ResourceState {
	if adapter.Name == "" || (adapter.Runtime != "docker" && adapter.Runtime != "podman") {
		return observation("error", "INVALID_CONFIGURATION", "container runtime and name are required")
	}
	if adapter.Runner == nil {
		adapter.Runner = systemRunner{}
	}
	// Scope inspect explicitly to containers; an image with the same name is not
	// a configured container resource.
	output, err := adapter.Runner.Run(ctx, adapter.Runtime, "container", "inspect", "--format", "{{.State.Status}}", adapter.Name)
	if err != nil {
		return observationFailure(ctx, err, output)
	}
	switch strings.TrimSpace(output) {
	case "running":
		return observation("running", "OK", "")
	case "created", "configured", "initialized", "exited", "stopped":
		return observation("stopped", "OK", "")
	case "dead":
		return observation("error", "RUNTIME_STATE_ERROR", "runtime reports dead container")
	default:
		return observation("unknown", "RUNTIME_STATE_UNKNOWN", output)
	}
}
func (adapter Systemd) Observe(ctx context.Context) protocol.ResourceState {
	if adapter.Unit == "" {
		return observation("error", "INVALID_CONFIGURATION", "systemd unit is required")
	}
	if adapter.Runner == nil {
		adapter.Runner = systemRunner{}
	}
	output, err := adapter.Runner.Run(ctx, "systemctl", "show", "--property=LoadState", "--property=ActiveState", adapter.Unit)
	if err != nil {
		return observationFailure(ctx, err, output)
	}
	properties := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || properties[key] != "" {
			return observation("error", "OBSERVATION_FAILED", "invalid systemd observation")
		}
		properties[key] = value
	}
	if properties["LoadState"] == "not-found" {
		return observation("error", "TARGET_NOT_FOUND", "systemd unit was not found")
	}
	if properties["LoadState"] != "loaded" && properties["LoadState"] != "masked" && properties["LoadState"] != "merged" {
		return observation("error", "OBSERVATION_FAILED", output)
	}
	switch properties["ActiveState"] {
	case "active":
		return observation("running", "OK", "")
	case "inactive":
		return observation("stopped", "OK", "")
	case "failed":
		return observation("error", "RUNTIME_STATE_ERROR", "systemd reports failed unit")
	default:
		return observation("unknown", "RUNTIME_STATE_UNKNOWN", output)
	}
}
func statusResult(state protocol.ResourceState) protocol.CommandResultPayload {
	if state.State == "running" || state.State == "stopped" {
		return protocol.CommandResultPayload{Success: true, Code: "OK", Message: state.State}
	}
	return failure(state.Code, state.Message)
}
