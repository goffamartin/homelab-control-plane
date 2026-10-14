//go:build !linux

package onboarding

import (
	"context"
	"hlcp/internal/agent"
)

type unavailableSandboxProbeHost struct{}

var nativeProbeCLIHost sandboxProbeCLIHost = unavailableSandboxProbeHost{}

func (unavailableSandboxProbeHost) Caller(context.Context) (uint32, uint32, error) {
	return 0, 0, ErrSandboxProbeInvalid
}
func (unavailableSandboxProbeHost) Config(context.Context) (agent.Config, error) {
	return agent.Config{}, ErrSandboxProbeInvalid
}
func (unavailableSandboxProbeHost) InspectProbe(context.Context) (SandboxProbeSnapshot, error) {
	return SandboxProbeSnapshot{}, ErrSandboxProbeInvalid
}
func (unavailableSandboxProbeHost) RunProbe(context.Context, SandboxProbeSnapshot, SandboxProbeInputs) (SandboxProbeEvidence, error) {
	return SandboxProbeEvidence{}, ErrSandboxProbeInvalid
}
