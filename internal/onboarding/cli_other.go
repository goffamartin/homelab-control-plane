//go:build !linux

package onboarding

import (
	"hlcp/internal/setup"
	"io"
)

func (nativeSetupHost) Owner() (setup.Owner, error) {
	return setup.Owner{}, setup.ErrUnsupportedPlatform
}
func terminalInput(io.Reader) bool { return false }
