package adapter

import (
	"context"
	"hlcp/internal/protocol"
	"strconv"
	"strings"
)

// Only one locally configured container, no paths, shell, exec, stdin or flags
// supplied by the controller. Callers grant access through resource actions.
func ContainerLogs(ctx context.Context, runtime, target string, q protocol.LogQuery, emit func(protocol.LogRecord) error) string {
	args, ok := containerLogArguments(runtime, target, q)
	if !ok {
		return "NOT_ALLOWED"
	}
	return containerLogsProcess(ctx, "/usr/bin/"+runtime, args, q, emit)
}
func containerLogArguments(runtime, target string, q protocol.LogQuery) ([]string, bool) {
	if (runtime != "docker" && runtime != "podman") || q.Validate() != nil || target == "" || len(target) > 256 || strings.HasPrefix(target, "-") {
		return nil, false
	}
	for _, c := range target {
		if c < 0x21 || c > 0x7e {
			return nil, false
		}
	}
	args := []string{"logs", "--timestamps", "--tail", strconv.Itoa(q.Tail)}
	if q.Since != "" {
		args = append(args, "--since", q.Since)
	}
	if q.Until != "" {
		args = append(args, "--until", q.Until)
	}
	if q.Follow {
		args = append(args, "--follow")
	}
	return append(args, "--", target), true
}
