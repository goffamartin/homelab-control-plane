//go:build !linux

package onboarding

import (
	"context"
	"io"
)

func nativeClientGrant(context.Context, string, string, bool) clientGrantReport {
	return grantFailure("UNSUPPORTED_PLATFORM", true)
}
func nativeClientGrantWithOptions(context.Context, string, string, bool, bool, bool) clientGrantReport {
	return grantFailure("UNSUPPORTED_PLATFORM", true)
}
func ClientProvisionChild(io.Reader, io.Writer) int { return 1 }
