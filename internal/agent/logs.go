package agent

import (
	"context"
	"hlcp/internal/adapter"
	"hlcp/internal/protocol"
	"runtime"
)

func (c Config) logHandler(ctx context.Context, request protocol.LogRequest, emit func(protocol.LogRecord) error) string {
	for _, r := range c.Resources {
		if r.ID == request.ResourceID && (r.Adapter == "docker" || r.Adapter == "podman") && contains(r.Actions, "logs") {
			if c.logReader != nil {
				return c.logReader(ctx, r.Adapter, r.Target, request.Query, emit)
			}
			return adapter.ContainerLogs(ctx, r.Adapter, r.Target, request.Query, emit)
		}
	}
	return "NOT_ALLOWED"
}
func (c Config) logCapabilities() []string {
	caps := append([]string(nil), c.Capabilities...)
	if runtime.GOOS == "linux" && !contains(caps, protocol.LogCapability) {
		for _, r := range c.Resources {
			if (r.Adapter == "docker" || r.Adapter == "podman") && contains(r.Actions, "logs") {
				return append(caps, protocol.LogCapability)
			}
		}
	}
	return caps
}
