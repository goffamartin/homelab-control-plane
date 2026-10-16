//go:build !linux

package adapter

import (
	"context"
	"hlcp/internal/protocol"
)

func containerLogsProcess(context.Context, string, []string, protocol.LogQuery, func(protocol.LogRecord) error) string {
	return "UNSUPPORTED"
}
