//go:build !linux

package setup

func planDirectory(string, Owner) (directorySource, error) { return nil, ErrUnsupportedPlatform }
func planDirectoryWithTrust(string, Owner, bool) (directorySource, error) {
	return nil, ErrUnsupportedPlatform
}
