//go:build !linux

package onboarding

import (
	"context"
	"hlcp/internal/agent"
)

type nativeSandboxInspectorHost struct{}

func (nativeSandboxInspectorHost) Identity(context.Context, uint32, uint32) error {
	return ErrSandboxProbeInvalid
}
func (nativeSandboxInspectorHost) ScriptFiles(context.Context, agent.ScriptCommandConfig) ([32]byte, error) {
	return [32]byte{}, ErrSandboxProbeInvalid
}
