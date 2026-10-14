//go:build !linux

package setup

func planDirectory(string, Owner) (directorySource, error) { return nil, ErrUnsupportedPlatform }
