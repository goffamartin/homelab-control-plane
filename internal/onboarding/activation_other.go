//go:build !linux

package onboarding

import "context"

type unavailableActivationHost struct{}

func nativeControllerActivationHost(string) ControllerActivationHost {
	return unavailableActivationHost{}
}
func (unavailableActivationHost) Inspect(context.Context) (ControllerActivationSnapshot, error) {
	return ControllerActivationSnapshot{}, ErrActivationInvalid
}
func (unavailableActivationHost) Change(context.Context, ControllerServiceAction) error {
	return ErrActivationInvalid
}
