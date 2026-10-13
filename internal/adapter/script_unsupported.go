//go:build !linux

package adapter

import (
	"context"
	"hlcp/internal/protocol"
)

func executeScript(context.Context, ScriptCommand) protocol.CommandResultPayload {
	return failure("INVALID_CONFIGURATION", "script execution is supported only on Linux")
}
