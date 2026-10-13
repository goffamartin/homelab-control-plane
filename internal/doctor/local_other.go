//go:build !linux

package doctor

import "hlcp/internal/setup"

func (NativeHost) Script(string, string) ScriptStatus { return ScriptStatus{} }
func (NativeHost) InspectSetup(string) (setup.TransactionStatus, error) {
	return setup.TransactionStatus{}, setup.ErrUnsupportedPlatform
}
