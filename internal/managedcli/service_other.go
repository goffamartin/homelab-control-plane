//go:build !linux

package managedcli

import "context"

type serviceState struct {
	active  string
	enabled bool
}

func ensureAccount(context.Context, string) (int, int, error) { return 0, 0, ErrSetup }
func inspectService(context.Context, string, string) (serviceState, error) {
	return serviceState{}, ErrSetup
}
func activateService(context.Context, string, string, bool) error            { return ErrSetup }
func fixedProcess(context.Context, string, []string, string) ([]byte, error) { return nil, ErrSetup }
func discoverContainers(context.Context, string) ([]byte, error)             { return nil, ErrSetup }
