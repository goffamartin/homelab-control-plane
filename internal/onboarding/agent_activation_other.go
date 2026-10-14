//go:build !linux

package onboarding

import "context"

type unavailableAgentActivationHost struct{}

var nativeAgentActivationHost AgentActivationHost = unavailableAgentActivationHost{}

func (unavailableAgentActivationHost) Caller(context.Context) (uint32, uint32, error) {
	return 0, 0, ErrAgentActivationProfile
}
func (unavailableAgentActivationHost) InspectAgent(context.Context, AgentServiceProfile) (AgentActivationSnapshot, error) {
	return AgentActivationSnapshot{}, ErrAgentActivationProfile
}
func (unavailableAgentActivationHost) ChangeAgent(context.Context, AgentServiceProfile, ControllerServiceAction) error {
	return ErrAgentActivationProfile
}
