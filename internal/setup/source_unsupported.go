//go:build !linux

package setup

func openNativeSource(string, Owner) (Source, error) { return nil, ErrUnsupportedPlatform }
func openNativeSourceWithTrust(string, Owner, bool) (Source, error) {
	return nil, ErrUnsupportedPlatform
}
